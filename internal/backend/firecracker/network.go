//go:build linux

package firecracker

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
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
type Network struct {
	// ProxyPort and DNSPort are where redirected traffic arrives on the host;
	// zero means DefaultProxyPort and DefaultDNSPort.
	ProxyPort, DNSPort int
	// Subnet is the pool tap /30s are carved from; default 172.16.0.0/16.
	Subnet *net.IPNet
	Logf   func(format string, a ...any)

	table string
	mu    sync.Mutex
	used  map[int]bool
	byIP  map[string]*tap // guest IP -> tap
	proxy *egressproxy.Server
	dns   *egressproxy.DNS
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
)

type tap struct {
	name    string
	idx     int
	host    net.IP
	guest   net.IP
	mac     string
	policy  atomic.Pointer[api.NetworkPolicy]
	matcher atomic.Pointer[egressproxy.Matcher]
}

func (t *tap) kernelArgs() []string {
	return []string{
		fmt.Sprintf("ip=%s::%s:255.255.255.252::eth0:off", t.guest, t.host),
		"sbx.dns=" + t.host.String(),
	}
}

func (n *Network) start() error {
	if os.Geteuid() != 0 {
		return errors.New("network: tap devices and nftables need root; run sandboxd as root or without networking")
	}
	for _, bin := range []string{"ip", "nft"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("network: %s is required", bin)
		}
	}
	if n.ProxyPort == 0 {
		n.ProxyPort = DefaultProxyPort
	}
	if n.DNSPort == 0 {
		n.DNSPort = DefaultDNSPort
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

	if err := n.installTable(); err != nil {
		return err
	}
	matchFor := func(a net.Addr) *egressproxy.Matcher {
		if t := n.lookup(a); t != nil && t.policy.Load().Mode == api.NetworkAllowlist {
			return t.matcher.Load()
		}
		return nil
	}
	n.proxy = egressproxy.New(nil, func(d egressproxy.Decision) {
		if !d.Allowed {
			n.Logf("%s", deniedLine(d))
		}
	})
	n.proxy.MatchFor = matchFor
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", n.ProxyPort))
	if err != nil {
		return fmt.Errorf("network: proxy: %w (if another program holds tcp port %d, move the proxy with --egress-proxy-port)", err, n.ProxyPort)
	}
	go n.proxy.Serve(l)

	n.dns = &egressproxy.DNS{
		MatchFor: matchFor,
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
		return fmt.Errorf("network: dns: %w (if another program holds udp port %d, move the resolver with --egress-dns-port)", err, n.DNSPort)
	}
	go n.dns.ServeUDP(pc)
	return nil
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
// the rules are reviewed as text and pinned by test.
func Ruleset(table string, proxyPort, dnsPort int) string {
	return fmt.Sprintf(`table inet %[1]s {
	set egress_on {
		type ipv4_addr
	}
	chain prerouting {
		type nat hook prerouting priority dstnat; policy accept;
		iifname "sbx*" ip saddr @egress_on tcp dport { 80, 443 } redirect to :%[2]d
		iifname "sbx*" ip saddr @egress_on udp dport 53 redirect to :%[3]d
	}
	chain input {
		type filter hook input priority filter; policy accept;
		iifname "sbx*" ct state established,related accept
		iifname "sbx*" ct status dnat tcp dport %[2]d accept
		iifname "sbx*" ct status dnat udp dport %[3]d accept
		iifname "sbx*" counter drop
		tcp dport %[2]d counter drop
		udp dport %[3]d counter drop
	}
	chain forward {
		type filter hook forward priority filter; policy accept;
		iifname "sbx*" counter drop
		oifname "sbx*" counter drop
	}
}
`, table, proxyPort, dnsPort)
}

func (n *Network) installTable() error {
	_ = run("nft", "delete", "table", "inet", n.table) // a leftover from an earlier run
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(Ruleset(n.table, n.ProxyPort, n.DNSPort))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("network: installing the nftables table: %v: %s", err, out)
	}
	return nil
}

// Close removes the table. Sandboxes still running lose their network.
func (n *Network) Close() {
	_ = run("nft", "delete", "table", "inet", n.table)
}

// attach creates a sandbox's tap and applies its policy. owner is the uid the
// VMM runs as (the jailer's), or -1.
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

	base := ipAdd(n.Subnet.IP.To4(), idx*4)
	t := &tap{
		name:  fmt.Sprintf("sbx%d", idx),
		idx:   idx,
		host:  ipAdd(base, 1),
		guest: ipAdd(base, 2),
		mac:   fmt.Sprintf("06:00:%02x:%02x:%02x:%02x", base[0], base[1], base[2], base[3]+2),
	}
	args := []string{"tuntap", "add", "dev", t.name, "mode", "tap"}
	if owner >= 0 {
		args = append(args, "user", fmt.Sprint(owner), "group", fmt.Sprint(owner))
	}
	_ = run("ip", "link", "del", t.name) // a leftover with the same name
	for _, a := range [][]string{
		args,
		{"addr", "add", t.host.String() + "/30", "dev", t.name},
		{"link", "set", t.name, "up"},
	} {
		if err := run("ip", a...); err != nil {
			n.release(t)
			return nil, err
		}
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

// update applies a policy to a running sandbox. The allowlist is swapped in one
// pointer store; the nftables set change is one atomic transaction. Neither has
// a moment where the sandbox is less restricted than both the old and the new
// policy — when tightening, the set change comes first.
func (n *Network) update(t *tap, p api.NetworkPolicy) error {
	switch p.Mode {
	case api.NetworkNone, api.NetworkAllowlist:
	default:
		return fmt.Errorf("network mode %q is not supported by this backend", p.Mode)
	}
	pc := p
	m := egressproxy.NewPolicyMatcher(p.Allow, p.Deny)
	if p.Mode == api.NetworkNone {
		if err := run("nft", "delete", "element", "inet", n.table, "egress_on", "{", t.guest.String(), "}"); err != nil && !strings.Contains(err.Error(), "No such file") {
			n.Logf("network: %s: %v", t.name, err)
		}
		t.policy.Store(&pc)
		t.matcher.Store(m)
		return nil
	}
	t.matcher.Store(m)
	t.policy.Store(&pc)
	return run("nft", "add", "element", "inet", n.table, "egress_on", "{", t.guest.String(), "}")
}

func (n *Network) detach(t *tap) {
	_ = run("nft", "delete", "element", "inet", n.table, "egress_on", "{", t.guest.String(), "}")
	_ = run("ip", "link", "del", t.name)
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
