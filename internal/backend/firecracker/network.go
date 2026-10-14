//go:build linux

package firecracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/egressproxy"
)

// Network enforces egress on the host, where the guest cannot reach it. Each
// sandbox gets a tap device on its own /30 and nothing else:
//
//   - the guest's tcp/80 and tcp/443 are redirected to one proxy, which
//     decides by name (TLS SNI or Host) with that sandbox's allowlist;
//   - its DNS goes to a stub that answers allowlisted names and never forwards;
//   - everything else from the tap is dropped, and nothing is ever forwarded.
//
// Two rules came out of measuring this (rewrite M3). Every packet the guest may
// send the host arrives DNAT'd, so a firewall the operator already runs — which
// typically accepts DNAT'd traffic and rejects the rest — lets exactly that
// through and no more; this table never assumes it is the only firewall. And the
// guest never addresses the proxy or the resolver directly: a connection to
// their ports that was not redirected is dropped, so neither is reachable from
// another machine either.
//
// Policy lives in two places, both updated atomically: nftables decides whether
// a guest is redirected at all (an `allowlist` guest is in the egress_on set, a
// `none` guest is not, so its packets meet the drop), and the proxy and the
// resolver hold its allowlist.
//
// Open egress (Open, sandboxd --egress-open) is the same shape with a third
// listener. An `open` guest is in the egress_open set instead: all its TCP is
// redirected to the forwarder (egressproxy.Forwarder), which connects from
// the host to where each connection was going, if that is a public address
// and not the host's; its DNS goes to the same resolver, which answers it
// with real addresses. Still nothing is forwarded, so open egress needs no
// ip_forward on the host and meets no other firewall's forward chain. UDP
// other than DNS, and ICMP, are dropped as for every guest.
//
// Each VM has a network namespace of its own (sbx<n>), and its tap is in it.
// A snapshot records the guest's address and its tap's name, so a fork must
// find both again: every namespace has the same tap, tap0, and every guest
// the same address, 10.0.0.2 behind 10.0.0.1, which collide with nothing
// because no two share a namespace. A veth pair joins the namespace to the
// host — sbx<n> on the host's side, on the slot's /30 — and the namespace
// rewrites the guest's packets to the slot's address (ns.nft). So the host
// sees each VM, a fork among them, at an address of its own on an interface
// of its own, exactly as it saw a tap before: these rules, the proxy and the
// resolver tell sandboxes apart as they always did. This is the scheme
// Firecracker documents for networking snapshot clones.
//
// A VM started before namespaces (its kept record says no netns) keeps its tap
// in the host's namespace, and cannot be snapshotted: its snapshot would carry
// a slot's address and tap name, which a fork could not have.
type Network struct {
	// ProxyPort and DNSPort are where redirected traffic arrives on the host;
	// zero means DefaultProxyPort and DefaultDNSPort.
	ProxyPort, DNSPort int
	// Open offers network mode open; the operator's choice, since a node
	// whose policy names no network defaults to open (spec.DefaultPolicy).
	// OpenPort is the forwarder's port; zero means DefaultOpenPort.
	Open     bool
	OpenPort int
	// Subnet is the pool tap /30s are carved from; default 172.16.0.0/16.
	Subnet *net.IPNet
	Logf   func(format string, a ...any)

	table string
	mu    sync.Mutex
	used  map[int]bool
	byIP  map[string]*tap // guest IP -> tap
	proxy *egressproxy.Server
	dns   *egressproxy.DNS
	fwd   *egressproxy.Forwarder
	// listening are the proxy's, the resolver's and the forwarder's sockets,
	// closed with the table, so a Network can be started again in the same
	// process (the VM tests start one per test).
	listening []io.Closer
}

// The host ports the proxy and the resolver listen on. They bind every
// address, so they must not be a port another service on the host already
// holds: sandboxd would refuse to start. The resolver was on 5353 once, which
// is mDNS: avahi-daemon holds it on most Linux desktops and on EL-family
// servers by default. 7353 is in no common services list and sits below the
// kernel's ephemeral range, so an outgoing socket cannot be holding it. The
// guest never sees either port: it sends to 53, 80 and 443, and nftables
// redirects. An operator whose host does use one moves it with
// --egress-proxy-port or --egress-dns-port.
const (
	DefaultProxyPort = 3128
	DefaultDNSPort   = 7353
	DefaultOpenPort  = 7354
)

// The host table's sets: the guests whose traffic is redirected, by mode.
const (
	setAllowlist = "egress_on"
	setOpen      = "egress_open"
)

// tap is a sandbox's slot on the host's network. name is the host's
// interface for it (the tap itself, or the veth's host end), host its address
// there, and guest the address the host sees the VM at.
type tap struct {
	name  string
	idx   int
	host  net.IP
	guest net.IP
	mac   string
	// ns is the VM's network namespace, "" for a VM from before them, whose
	// tap is name itself in the host's namespace.
	ns string
	// set is the host table's set the guest is in ("" for none); guarded by
	// the Network's mu.
	set     string
	policy  atomic.Pointer[api.NetworkPolicy]
	matcher atomic.Pointer[egressproxy.Matcher]
}

// Inside a VM's namespace everything is the same for every VM, which is what
// lets a snapshot be restored into any of them.
const (
	nsTap   = "tap0"
	nsVeth  = "veth0"
	nsTable = "sbxns"
	nsMAC   = "06:00:0a:00:00:02"
)

var (
	nsGateway = net.IPv4(10, 0, 0, 1).To4()
	nsGuest   = net.IPv4(10, 0, 0, 2).To4()
)

// gateway is what the guest is told its gateway and resolver are.
func (t *tap) gateway() net.IP {
	if t.ns != "" {
		return nsGateway
	}
	return t.host
}

func (t *tap) kernelArgs() []string {
	guest := t.guest
	if t.ns != "" {
		guest = nsGuest
	}
	return []string{
		fmt.Sprintf("ip=%s::%s:255.255.255.252::eth0:off", guest, t.gateway()),
		"sbx.dns=" + t.gateway().String(),
	}
}

// device is the tap's name and the guest's MAC, as the VMM is given them.
func (t *tap) device() (name, mac string) {
	if t.ns != "" {
		return nsTap, nsMAC
	}
	return t.name, t.mac
}

// nsPath is the namespace's file, which the jailer joins (--netns).
func (t *tap) nsPath() string { return "/run/netns/" + t.ns }

// keptTap is a tap an earlier sandboxd made for a VM this one takes back.
type keptTap struct {
	id     string
	idx    int
	ns     bool
	policy api.NetworkPolicy
}

// start installs the table and starts the proxy and the resolver. The taps of
// VMs taken back from an earlier sandboxd (keep.go) are registered first and
// their guests put in the egress set in the same transaction that installs
// the table, so a kept VM's network is never off for longer than the restart.
func (n *Network) start(kept []keptTap) (map[string]*tap, error) {
	if os.Geteuid() != 0 {
		return nil, errors.New("network: tap devices and nftables need root; run sandboxd as root or without networking")
	}
	for _, bin := range []string{"ip", "nft", "sysctl"} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, fmt.Errorf("network: %s is required", bin)
		}
	}
	if n.ProxyPort == 0 {
		n.ProxyPort = DefaultProxyPort
	}
	if n.DNSPort == 0 {
		n.DNSPort = DefaultDNSPort
	}
	if n.Open && n.OpenPort == 0 {
		n.OpenPort = DefaultOpenPort
	}
	if n.Subnet == nil {
		_, n.Subnet, _ = net.ParseCIDR("172.16.0.0/16")
	}
	if n.Logf == nil {
		n.Logf = func(string, ...any) {}
	}
	n.table = "sandboxd"
	n.used = map[int]bool{}
	n.byIP = map[string]*tap{}

	byID := map[string]*tap{}
	sets := map[string][]string{}
	for _, k := range kept {
		t := n.tapAt(k.idx, k.ns)
		pc := k.policy
		if pc.Mode == api.NetworkOpen && !n.Open {
			// Taken back by a sandboxd that no longer offers open egress: it
			// gets none rather than more than the operator now allows.
			n.Logf("network: sandbox %s had open egress, which this sandboxd does not offer (--egress-open): it has no network now", k.id)
			pc = api.NetworkPolicy{Mode: api.NetworkNone}
		}
		t.policy.Store(&pc)
		t.matcher.Store(matcherFor(pc))
		t.set = setFor(pc.Mode)
		n.used[t.idx] = true
		n.byIP[t.guest.String()] = t
		byID[k.id] = t
		if t.set != "" {
			sets[t.set] = append(sets[t.set], t.guest.String())
		}
	}
	if err := n.installTable(sets); err != nil {
		return nil, err
	}
	// The proxy answers allowlisted guests only; the resolver answers open
	// ones too, with real addresses (Open below).
	matchFor := func(a net.Addr) *egressproxy.Matcher {
		if t := n.lookup(a); t != nil && t.policy.Load().Mode == api.NetworkAllowlist {
			return t.matcher.Load()
		}
		return nil
	}
	openFor := func(a net.Addr) (*egressproxy.Matcher, bool) {
		if t := n.lookup(a); t != nil && n.Open && t.policy.Load().Mode == api.NetworkOpen {
			return t.matcher.Load(), true
		}
		return nil, false
	}
	n.proxy = egressproxy.New(nil, func(d egressproxy.Decision) {
		if !d.Allowed {
			n.Logf("%s", deniedLine(d))
		}
	})
	n.proxy.MatchFor = matchFor
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", n.ProxyPort))
	if err != nil {
		return nil, fmt.Errorf("network: proxy: %w (if another program holds tcp port %d, move the proxy with --egress-proxy-port)", err, n.ProxyPort)
	}
	n.listening = append(n.listening, l)
	go n.proxy.Serve(l)

	n.dns = &egressproxy.DNS{
		MatchFor: func(a net.Addr) *egressproxy.Matcher {
			if m, ok := openFor(a); ok {
				return m
			}
			return matchFor(a)
		},
		Open: func(a net.Addr) bool {
			_, ok := openFor(a)
			return ok
		},
		Resolve: func(name string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(context.Background(), "ip4", name)
		},
		Sinkhole: func(a net.Addr) net.IP {
			if t := n.lookup(a); t != nil {
				return t.host
			}
			return nil
		},
		// A name the resolver refuses never reaches the proxy, so without
		// this the commonest refusal — a site not on the list — leaves no
		// trace for the operator.
		Log: func(d egressproxy.Decision) {
			n.Logf("%s", deniedLine(d))
		},
	}
	pc, err := net.ListenPacket("udp", fmt.Sprintf("0.0.0.0:%d", n.DNSPort))
	if err != nil {
		return nil, fmt.Errorf("network: dns: %w (if another program holds udp port %d, move the resolver with --egress-dns-port)", err, n.DNSPort)
	}
	n.listening = append(n.listening, pc)
	go n.dns.ServeUDP(pc)
	if n.Open {
		n.fwd = egressproxy.NewForwarder(func(d egressproxy.Decision) {
			if !d.Allowed {
				n.Logf("%s", deniedLine(d))
			}
		})
		n.fwd.For = openFor
		ol, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", n.OpenPort))
		if err != nil {
			return nil, fmt.Errorf("network: open egress: %w (if another program holds tcp port %d, move it with --egress-open-port)", err, n.OpenPort)
		}
		n.listening = append(n.listening, ol)
		go n.fwd.Serve(ol)
		n.Logf("network: open egress offered: TCP to public addresses, through port %d", n.OpenPort)
	}
	n.sweepTaps()
	return byID, nil
}

// sweepTaps deletes the taps and namespaces no VM taken back owns: one an
// earlier sandboxd made for a VM since removed, or whose record was
// unreadable. Only names this backend gives (sbx<n>).
func (n *Network) sweepTaps() {
	for _, dir := range []string{"/run/netns", "/sys/class/net"} {
		des, _ := os.ReadDir(dir)
		var names []string
		for _, de := range des {
			names = append(names, de.Name())
		}
		n.mu.Lock()
		stray := strayTaps(names, n.used)
		n.mu.Unlock()
		for _, name := range stray {
			args := []string{"link", "del", name}
			what := "a network device"
			if dir == "/run/netns" {
				args, what = []string{"netns", "del", name}, "a network namespace"
			}
			if err := run("ip", args...); err == nil {
				n.Logf("network: removed %s no sandbox owns: %s", what, name)
			}
		}
	}
}

// strayTaps are the names among names that this backend gives (sbx<n>) and
// that no used slot accounts for.
func strayTaps(names []string, used map[int]bool) []string {
	var out []string
	for _, name := range names {
		rest, ok := strings.CutPrefix(name, "sbx")
		if !ok || rest == "" {
			continue
		}
		idx, err := strconv.Atoi(rest)
		if err != nil || idx < 0 || strconv.Itoa(idx) != rest {
			continue
		}
		if !used[idx] {
			out = append(out, name)
		}
	}
	return out
}

// deniedLine is the log line for a refused name. The name is the guest's —
// from its DNS query, TLS handshake or Host header — and a DNS label may hold
// any byte, so it is quoted: a newline or an escape sequence must not forge a
// line or reach the operator's terminal raw.
func deniedLine(d egressproxy.Decision) string {
	if d.Port == 0 { // the resolver: a name, no connection yet
		return fmt.Sprintf("egress denied: %q (%s)", d.Host, d.Reason)
	}
	return fmt.Sprintf("egress denied: %q port %d (%s)", d.Host, d.Port, d.Reason)
}

func (n *Network) lookup(a net.Addr) *tap {
	var ip net.IP
	switch v := a.(type) {
	case *net.TCPAddr:
		ip = v.IP
	case *net.UDPAddr:
		ip = v.IP
	default:
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.byIP[ip.To4().String()]
}

// Ruleset is the nftables table, rendered from the ports. A pure function, so
// the rules are reviewed as text and pinned by test. openPort 0 is a node
// without open egress, whose table has no open set and no rule for it.
func Ruleset(table string, proxyPort, dnsPort, openPort int) string {
	var openSet, openRedirect, openInput, openDrop string
	if openPort > 0 {
		openSet = fmt.Sprintf("\n\tset %s {\n\t\ttype ipv4_addr\n\t}", setOpen)
		openRedirect = fmt.Sprintf("\n\t\tiifname \"sbx*\" ip saddr @%[1]s udp dport 53 redirect to :%[2]d"+
			"\n\t\tiifname \"sbx*\" ip saddr @%[1]s meta l4proto tcp redirect to :%[3]d", setOpen, dnsPort, openPort)
		openInput = fmt.Sprintf("\n\t\tiifname \"sbx*\" ct status dnat tcp dport %d accept", openPort)
		openDrop = fmt.Sprintf("\n\t\ttcp dport %d counter drop", openPort)
	}
	return fmt.Sprintf(`table inet %[1]s {
	set egress_on {
		type ipv4_addr
	}%[4]s
	chain prerouting {
		type nat hook prerouting priority dstnat; policy accept;
		iifname "sbx*" ip saddr @egress_on tcp dport { 80, 443 } redirect to :%[2]d
		iifname "sbx*" ip saddr @egress_on udp dport 53 redirect to :%[3]d%[5]s
	}
	chain input {
		type filter hook input priority filter; policy accept;
		iifname "sbx*" ct state established,related accept
		iifname "sbx*" ct status dnat tcp dport %[2]d accept
		iifname "sbx*" ct status dnat udp dport %[3]d accept%[6]s
		iifname "sbx*" counter drop
		tcp dport %[2]d counter drop
		udp dport %[3]d counter drop%[7]s
	}
	chain forward {
		type filter hook forward priority filter; policy accept;
		iifname "sbx*" counter drop
		oifname "sbx*" counter drop
	}
}
`, table, proxyPort, dnsPort, openSet, openRedirect, openInput, openDrop)
}

// NSRuleset is the table in each VM's namespace, rendered from the slot's
// host address. A pure function, pinned by test. It lets the guest out only
// to the host, as the guest's own address rewritten to the slot's
// (masquerade), and its DNS to the gateway on to the host's resolver; the
// host's table then decides as it does for every sandbox. Nothing reaches
// the guest but replies, and the namespace itself answers nothing.
func NSRuleset(hostIP string) string {
	return fmt.Sprintf(`table inet %[1]s {
	chain prerouting {
		type nat hook prerouting priority dstnat; policy accept;
		iifname "%[2]s" ip daddr %[4]s udp dport 53 dnat ip to %[5]s
		iifname "%[2]s" ip daddr %[4]s tcp dport 53 dnat ip to %[5]s
	}
	chain postrouting {
		type nat hook postrouting priority srcnat; policy accept;
		oifname "%[3]s" ip saddr %[6]s masquerade
	}
	chain input {
		type filter hook input priority filter; policy drop;
		iifname "lo" accept
		ct state established,related accept
	}
	chain forward {
		type filter hook forward priority filter; policy drop;
		iifname "%[2]s" oifname "%[3]s" ip saddr %[6]s accept
		iifname "%[3]s" oifname "%[2]s" ct state established,related accept
	}
	chain output {
		type filter hook output priority filter; policy accept;
	}
}
`, nsTable, nsTap, nsVeth, nsGateway, hostIP, nsGuest)
}

// installTable replaces whatever table an earlier sandboxd left with this
// one's, and puts the guests in sets in its sets (installScript).
func (n *Network) installTable(sets map[string][]string) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(installScript(n.table, n.ProxyPort, n.DNSPort, n.OpenPort, sets))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("network: installing the nftables table: %v: %s", err, out)
	}
	return nil
}

// installScript is one nft transaction: declare the table (so the delete
// cannot fail when there is none), delete it, add it afresh, and put the
// guests in sets in its sets. nft -f applies a file atomically, so no packet
// meets a missing table or an empty set: a VM taken back keeps its egress
// through the restart, and one that was refused it stays refused.
func installScript(table string, proxyPort, dnsPort, openPort int, sets map[string][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\n", table, table)
	b.WriteString(Ruleset(table, proxyPort, dnsPort, openPort))
	for _, set := range []string{setAllowlist, setOpen} {
		if len(sets[set]) > 0 {
			fmt.Fprintf(&b, "add element inet %s %s { %s }\n", table, set, strings.Join(sets[set], ", "))
		}
	}
	return b.String()
}

// Close removes the table and stops listening. Sandboxes still running lose
// their network.
func (n *Network) Close() {
	_ = run("nft", "delete", "table", "inet", n.table)
	for _, c := range n.listening {
		_ = c.Close()
	}
	n.listening = nil
}

// attach creates a sandbox's namespace, its tap and the veth to the host, and
// applies its policy. owner is the uid the VMM runs as (the jailer's), or -1.
func (n *Network) attach(id string, p api.NetworkPolicy, owner int) (*tap, error) {
	n.mu.Lock()
	idx := -1
	max := 1 << (32 - maskBits(n.Subnet) - 2)
	for i := 0; i < max; i++ {
		if !n.used[i] {
			idx = i
			break
		}
	}
	if idx < 0 {
		n.mu.Unlock()
		return nil, errors.New("no free network slots")
	}
	n.used[idx] = true
	n.mu.Unlock()

	t := n.tapAt(idx, true)
	if err := n.makeNamespace(t, owner); err != nil {
		n.removeNamespace(t)
		n.release(t)
		return nil, err
	}
	n.mu.Lock()
	n.byIP[t.guest.String()] = t
	n.mu.Unlock()
	if err := n.update(t, p); err != nil {
		n.detach(t)
		return nil, err
	}
	return t, nil
}

// makeNamespace builds slot t's namespace: the veth pair to the host on the
// slot's /30, the tap the VMM opens (owned by its uid), the route out, and
// the namespace's table (NSRuleset).
func (n *Network) makeNamespace(t *tap, owner int) error {
	n.removeNamespace(t) // a leftover with the same name
	tapArgs := []string{"-n", t.ns, "tuntap", "add", "dev", nsTap, "mode", "tap"}
	if owner >= 0 {
		tapArgs = append(tapArgs, "user", fmt.Sprint(owner), "group", fmt.Sprint(owner))
	}
	for _, a := range [][]string{
		{"netns", "add", t.ns},
		{"link", "add", t.name, "type", "veth", "peer", "name", nsVeth, "netns", t.ns},
		{"addr", "add", t.host.String() + "/30", "dev", t.name},
		{"link", "set", t.name, "up"},
		{"-n", t.ns, "addr", "add", t.guest.String() + "/30", "dev", nsVeth},
		{"-n", t.ns, "link", "set", nsVeth, "up"},
		{"-n", t.ns, "link", "set", "lo", "up"},
		tapArgs,
		{"-n", t.ns, "addr", "add", nsGateway.String() + "/30", "dev", nsTap},
		{"-n", t.ns, "link", "set", nsTap, "up"},
		{"-n", t.ns, "route", "add", "default", "via", t.host.String()},
	} {
		if err := run("ip", a...); err != nil {
			return err
		}
	}
	if err := run("ip", "netns", "exec", t.ns, "sysctl", "-qw", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	cmd := exec.Command("ip", "netns", "exec", t.ns, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(NSRuleset(t.host.String()))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("network: the namespace's nftables table: %v: %s", err, out)
	}
	return nil
}

// removeNamespace deletes slot t's namespace, which takes its tap and the
// veth's inside end with it, and the veth's host end if it is left.
func (n *Network) removeNamespace(t *tap) {
	if t.ns != "" {
		_ = run("ip", "netns", "del", t.ns)
	}
	_ = run("ip", "link", "del", t.name)
}

// tapAt is the tap in slot idx: its name, addresses and MAC are a function
// of the slot alone, which is what lets a later sandboxd take one back. ns
// says whether the VM has a namespace of its own (every VM since they came).
func (n *Network) tapAt(idx int, ns bool) *tap {
	base := ipAdd(n.Subnet.IP.To4(), idx*4)
	t := &tap{
		name:  fmt.Sprintf("sbx%d", idx),
		idx:   idx,
		host:  ipAdd(base, 1),
		guest: ipAdd(base, 2),
		mac:   fmt.Sprintf("06:00:%02x:%02x:%02x:%02x", base[0], base[1], base[2], base[3]+2),
	}
	if ns {
		t.ns = t.name
	}
	return t
}

// update applies a policy to a running sandbox. The policy is swapped in one
// pointer store, and the proxy, the resolver and the forwarder each check
// the mode on every connection, so they hold the new policy from that
// moment; the nftables set change (moving the guest from one set to another)
// is one atomic transaction after it. Neither has a moment where the sandbox
// is less restricted than both the old and the new policy.
func (n *Network) update(t *tap, p api.NetworkPolicy) error {
	switch p.Mode {
	case api.NetworkNone, api.NetworkAllowlist:
	case api.NetworkOpen:
		if !n.Open {
			return errors.New("network mode open is not offered by this sandboxd (--egress-open)")
		}
	default:
		return fmt.Errorf("network mode %q is not supported by this backend", p.Mode)
	}
	pc := p
	t.matcher.Store(matcherFor(pc))
	t.policy.Store(&pc)
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.moveLocked(t, setFor(p.Mode))
}

// moveLocked puts the guest in set ("" for none) and out of the one it was
// in, in one nft transaction. n.mu is held.
func (n *Network) moveLocked(t *tap, set string) error {
	if t.set == set {
		return nil
	}
	script := setMove(n.table, t.guest.String(), t.set, set)
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("network: %s: %v: %s", t.name, err, strings.TrimSpace(string(out)))
	}
	t.set = set
	return nil
}

// setMove is the nft transaction moving guest from set from to set to, either
// of which may be "" (in none).
func setMove(table, guest, from, to string) string {
	var b strings.Builder
	if from != "" {
		fmt.Fprintf(&b, "delete element inet %s %s { %s }\n", table, from, guest)
	}
	if to != "" {
		fmt.Fprintf(&b, "add element inet %s %s { %s }\n", table, to, guest)
	}
	return b.String()
}

// setFor is the host table's set for a network mode: "" for none.
func setFor(mode string) string {
	switch mode {
	case api.NetworkAllowlist:
		return setAllowlist
	case api.NetworkOpen:
		return setOpen
	}
	return ""
}

// matcherFor is a policy's names: its allowlist, or under open every name
// but its deny list.
func matcherFor(p api.NetworkPolicy) *egressproxy.Matcher {
	if p.Mode == api.NetworkOpen {
		return egressproxy.NewDenyMatcher(p.Deny)
	}
	return egressproxy.NewPolicyMatcher(p.Allow, p.Deny)
}

func (n *Network) detach(t *tap) {
	none := api.NetworkPolicy{Mode: api.NetworkNone}
	t.policy.Store(&none)
	n.mu.Lock()
	if err := n.moveLocked(t, ""); err != nil {
		n.Logf("%v", err)
	}
	n.mu.Unlock()
	n.removeNamespace(t)
	n.release(t)
}

func (n *Network) release(t *tap) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.byIP, t.guest.String())
	delete(n.used, t.idx)
}

func run(bin string, args ...string) error {
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func maskBits(n *net.IPNet) int { ones, _ := n.Mask.Size(); return ones }

func ipAdd(ip net.IP, n int) net.IP {
	v := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	v += uint32(n)
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).To4()
}
