package egressproxy

import (
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestUnreachable(t *testing.T) {
	local := []net.IP{net.ParseIP("203.0.114.7")}
	for addr, want := range map[string]string{
		"140.82.112.3":    "",
		"1.1.1.1":         "",
		"10.1.2.3":        "not a public address",
		"192.168.1.10":    "not a public address",
		"172.16.0.5":      "not a public address", // sandboxd's own slots
		"172.31.255.255":  "not a public address",
		"127.0.0.1":       "not a public address",
		"169.254.169.254": "not a public address", // a cloud's metadata service
		"100.104.115.38":  "not a public address", // a tailnet
		"0.0.0.0":         "not a public address",
		"224.0.0.1":       "not a public address",
		"255.255.255.255": "not a public address",
		"203.0.114.7":     "one of the host's own addresses",
		"2606:4700::1111": "not an IPv4 address",
	} {
		got := Unreachable(net.ParseIP(addr), local)
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%s: %q, want %q", addr, got, want)
		}
	}
}

// openForwarder is a Forwarder whose connections were all redirected from
// dest, for a sandbox whose deny list is deny; open false is a connection
// from no open sandbox. Dials are counted and answered with the greeting.
func openForwarder(t *testing.T, dest string, deny []string, open bool) (net.Listener, *atomic.Int32, *[]Decision, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var log []Decision
	var dials atomic.Int32
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(dest)
	f := NewForwarder(func(d Decision) {
		mu.Lock()
		log = append(log, d)
		mu.Unlock()
	})
	f.For = func(net.Addr) (*Matcher, bool) { return NewDenyMatcher(deny), open }
	f.Dest = func(net.Conn) (net.IP, int, error) {
		p, _ := net.LookupPort("tcp", port)
		return net.ParseIP(host), p, nil
	}
	f.Unreachable = func(ip net.IP) string { return Unreachable(ip, nil) }
	f.Dial = func(network, addr string) (net.Conn, error) {
		if addr != dest {
			t.Errorf("dialled %s, want %s", addr, dest)
		}
		dials.Add(1)
		c1, c2 := net.Pipe()
		go func() { _, _ = io.Copy(io.Discard, c2) }()
		go func() { _, _ = io.WriteString(c2, upstreamGreeting) }()
		t.Cleanup(func() { c2.Close(); c1.Close() })
		return c1, nil
	}
	go f.Serve(l)
	t.Cleanup(func() { l.Close() })
	return l, &dials, &log, &mu
}

func TestOpenEgressConnectsToWhereTheConnectionWasGoing(t *testing.T) {
	// Any port, and nothing need be said first: ssh's server speaks first.
	l, dials, _, _ := openForwarder(t, "140.82.112.3:22", nil, true)
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := readUntil(t, c, upstreamGreeting); !strings.Contains(got, upstreamGreeting) {
		t.Errorf("not connected; read %q", got)
	}
	if dials.Load() != 1 {
		t.Errorf("%d dials", dials.Load())
	}
}

func TestOpenEgressNeverReachesThePrivateNetwork(t *testing.T) {
	for _, dest := range []string{"169.254.169.254:80", "192.168.1.1:443", "100.104.115.38:2222", "127.0.0.1:7444"} {
		l, dials, log, mu := openForwarder(t, dest, nil, true)
		c, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		d := awaitDecision(t, mu, log)
		c.Close()
		if d.Allowed || !strings.Contains(d.Reason, "not a public address") || dials.Load() != 0 {
			t.Errorf("%s: %+v, %d dials", dest, d, dials.Load())
		}
	}
}

func TestOpenEgressRefusesADeniedName(t *testing.T) {
	l, dials, log, mu := openForwarder(t, "140.82.112.3:443", []string{"*.example.com"}, true)
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	speak(t, c, "api.example.com")
	d := awaitDecision(t, mu, log)
	if d.Allowed || d.Host != "api.example.com" || dials.Load() != 0 {
		t.Errorf("a denied name went through: %+v, %d dials", d, dials.Load())
	}

	// One not on the list goes through, its handshake with it.
	l, dials, _, _ = openForwarder(t, "140.82.112.3:443", []string{"*.example.com"}, true)
	c2, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	speak(t, c2, "github.com")
	if got := readUntil(t, c2, upstreamGreeting); !strings.Contains(got, upstreamGreeting) || dials.Load() != 1 {
		t.Errorf("an allowed name was not connected; read %q", got)
	}
}

func TestOpenEgressAnswersOnlyOpenSandboxes(t *testing.T) {
	l, dials, _, _ := openForwarder(t, "140.82.112.3:22", nil, false)
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := readUntil(t, c, upstreamGreeting); got != "" || dials.Load() != 0 {
		t.Errorf("a sandbox without open egress was connected: %q, %d dials", got, dials.Load())
	}
}
