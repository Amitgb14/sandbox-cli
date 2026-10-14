package egressproxy

import (
	"bufio"
	"net"
	"strconv"
	"time"
)

// Forwarder serves sandboxes with open egress. Every TCP connection such a
// sandbox makes is redirected to it, whatever its port, and it connects to
// where the connection was going, from the host. Nothing is routed: the host
// forwards no packet for a guest, so open egress needs no ip_forward and
// meets no other firewall's forward chain, exactly as the allowlist does not.
//
// Open is the internet, not the operator's networks. A destination that is
// not a public address — private, loopback, link-local (a cloud's metadata
// service), the shared CGNAT range a tailnet lives in, multicast, reserved —
// or that is one of the host's own addresses is refused (Unreachable), so a
// sandbox with open egress still cannot reach the host's services or its LAN.
//
// The deny list is by name, as everywhere: a denied name does not resolve in
// the sandbox (DNS), and a connection to port 80 or 443 that names a denied
// host (TLS SNI, HTTP Host) is refused here, wherever it was addressed. A
// connection that names nothing goes through — open is everything but what
// was denied — so deny is a filter on names, not a guarantee about addresses.
type Forwarder struct {
	// For returns the sandbox's matcher (all names but its deny list) for
	// the connecting address; ok false refuses the connection — it is from
	// no sandbox with open egress.
	For func(remote net.Addr) (m *Matcher, ok bool)
	// Dest is where a redirected connection was going (OriginalDestination).
	Dest func(c net.Conn) (net.IP, int, error)
	// Unreachable says why an address may not be connected to, "" when it
	// may.
	Unreachable func(ip net.IP) string
	Dial        func(network, addr string) (net.Conn, error)
	Log         func(Decision)
}

// NewForwarder returns a Forwarder with real networking wired in: the
// original destination from the kernel, and the host's own addresses read
// on every connection, so an address added after startup is refused too.
func NewForwarder(log func(Decision)) *Forwarder {
	if log == nil {
		log = func(Decision) {}
	}
	return &Forwarder{
		Dest: OriginalDestination,
		Unreachable: func(ip net.IP) string {
			local, err := localAddrs()
			if err != nil {
				return "open egress: cannot read the host's addresses"
			}
			return Unreachable(ip, local)
		},
		Dial: func(n, a string) (net.Conn, error) { return net.DialTimeout(n, a, 20*time.Second) },
		Log:  log,
	}
}

// Serve accepts connections until the listener is closed.
func (f *Forwarder) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go f.handle(c)
	}
}

func (f *Forwarder) handle(client net.Conn) {
	defer client.Close()
	m, ok := f.For(client.RemoteAddr())
	if !ok || m == nil {
		return
	}
	ip, port, err := f.Dest(client)
	if err != nil || ip == nil || port <= 0 {
		f.Log(Decision{Reason: "open egress: no original destination"})
		return
	}
	addr := ip.String()
	if why := f.Unreachable(ip); why != "" {
		f.Log(Decision{Host: addr, Port: port, Reason: why})
		return
	}
	br := bufio.NewReaderSize(client, maxClientHello+512)
	// The deny list is checked where a name is carried. Only when there is a
	// list: peeking waits for the client to speak first, which a protocol
	// whose server speaks first never does.
	if m.deny != nil && (port == 80 || port == 443) {
		_ = client.SetReadDeadline(time.Now().Add(handshakeTimeout))
		host, _, _, perr := (&Server{}).peek(br)
		_ = client.SetReadDeadline(time.Time{})
		if perr == nil && !m.Allows(host) {
			f.Log(Decision{Host: host, Port: port, Reason: "on the deny list"})
			return
		}
	}
	upstream, err := f.Dial("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
	if err != nil {
		f.Log(Decision{Host: addr, Port: port, Allowed: true, Reason: "upstream: " + err.Error()})
		return
	}
	defer upstream.Close()
	splice(client, br, upstream)
}

// notPublic are the IPv4 ranges open egress never reaches: nothing in them is
// "the internet", and much of what is in them is the operator's.
var notPublic = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8",       // this network
		"10.0.0.0/8",      // private
		"100.64.0.0/10",   // shared address space: CGNAT, and tailnets
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local, where cloud metadata services answer
		"172.16.0.0/12",   // private, and sandboxd's own slots by default
		"192.0.0.0/24",    // protocol assignments
		"192.0.2.0/24",    // documentation
		"192.88.99.0/24",  // 6to4 relay
		"192.168.0.0/16",  // private
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved, and broadcast
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// Unreachable says why open egress may not connect to ip, or "" when it may:
// ip must be a public IPv4 address and none of the host's own (local).
func Unreachable(ip net.IP, local []net.IP) string {
	v4 := ip.To4()
	if v4 == nil {
		return "open egress: not an IPv4 address"
	}
	for _, n := range notPublic {
		if n.Contains(v4) {
			return "open egress: not a public address"
		}
	}
	for _, l := range local {
		if l.Equal(v4) {
			return "open egress: one of the host's own addresses"
		}
	}
	return ""
}

// localAddrs are the host's addresses, read afresh for every connection
// rather than once, so one added since startup is refused too. An error
// refuses the connection (NewForwarder): a host's public address is what
// the public check alone would miss.
func localAddrs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			out = append(out, n.IP)
		}
	}
	return out, nil
}
