package egressproxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// testServer wires a Server with fake DNS and a fake upstream, so the tests
// exercise the decision path without touching the network.
func testServer(t *testing.T, allow []string) (*Server, net.Listener, *[]Decision, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var log []Decision
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := New(NewMatcher(allow), func(d Decision) {
		mu.Lock()
		log = append(log, d)
		mu.Unlock()
	})
	s.Resolve = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("127.0.0.1")}, nil }
	s.Dial = func(network, addr string) (net.Conn, error) {
		c1, c2 := net.Pipe()
		go func() { io.Copy(io.Discard, c2); c2.Close() }()
		go func() { io.WriteString(c2, "UPSTREAM-REACHED") }()
		return c1, nil
	}
	go s.Serve(l)
	t.Cleanup(func() { l.Close() })
	return s, l, &log, &mu
}

// TestDeniedConnectionNeverReachesUpstream is the property that matters most: a
// refused name must not cause an upstream connection at all. Checking only that
// the client got nothing back would pass even if the proxy had dialled out and
// then dropped the response — which for an exfiltration channel is the whole
// attack.
func TestDeniedConnectionNeverReachesUpstream(t *testing.T) {
	var dialed []string
	var mu sync.Mutex
	s, l, _, _ := testServer(t, []string{"github.com"})
	s.Dial = func(network, addr string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, addr)
		mu.Unlock()
		return nil, fmt.Errorf("should not dial")
	}

	// gist.github.com is the confirmed exfiltration channel the address-based
	// firewall let through because it shares github.com's IP.
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go tls.Client(c, &tls.Config{ServerName: "gist.github.com", InsecureSkipVerify: true}).Handshake()
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(dialed) != 0 {
		t.Errorf("a denied host caused an upstream dial to %v", dialed)
	}
}

// TestAllowedHostIsTunnelled checks the other direction: an allowlisted name
// still works, including that the bytes already peeked at are forwarded rather
// than swallowed.
func TestAllowedHostIsTunnelled(t *testing.T) {
	_, l, log, mu := testServer(t, []string{"github.com"})

	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go tls.Client(c, &tls.Config{ServerName: "github.com", InsecureSkipVerify: true}).Handshake()

	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, _ := c.Read(buf)
	if !strings.Contains(string(buf[:n]), "UPSTREAM-REACHED") {
		t.Errorf("allowed host was not tunnelled; read %q", buf[:n])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*log) == 0 || !(*log)[0].Allowed {
		t.Errorf("expected an allow decision, got %+v", *log)
	}
}

// TestExplicitConnectIsAlsoEnforced covers the second shape a connection arrives
// in. HTTPS_PROXY is set for compatibility, but it is not the boundary — an
// agent that uses it must be checked by the same rule as one that is redirected.
func TestExplicitConnectIsAlsoEnforced(t *testing.T) {
	_, l, _, _ := testServer(t, []string{"github.com"})

	ask := func(target string) string {
		c, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		line, _ := bufio.NewReader(c).ReadString('\n')
		return line
	}
	if got := ask("github.com:443"); !strings.Contains(got, "200") {
		t.Errorf("allowed CONNECT = %q, want 200", got)
	}
	if got := ask("gist.github.com:443"); !strings.Contains(got, "403") {
		t.Errorf("denied CONNECT = %q, want 403", got)
	}
}

// TestConnectionWithNoNameIsRefused pins that "dial the address directly" is not
// a way around a name-based allowlist. A handshake with no SNI carries no name,
// and the proxy must refuse rather than fall back to the destination address.
func TestConnectionWithNoNameIsRefused(t *testing.T) {
	var dialed int
	var mu sync.Mutex
	s, l, _, _ := testServer(t, []string{"github.com"})
	s.Dial = func(string, string) (net.Conn, error) {
		mu.Lock()
		dialed++
		mu.Unlock()
		return nil, fmt.Errorf("should not dial")
	}

	c, _ := net.Dial("tcp", l.Addr().String())
	defer c.Close()
	go tls.Client(c, &tls.Config{InsecureSkipVerify: true}).Handshake() // no ServerName
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if dialed != 0 {
		t.Error("a connection with no hostname was dialled out anyway")
	}
}

// TestSilentConnectionDoesNotHoldAGoroutine covers the resource side. The agent
// can open many sockets; one that says nothing must time out rather than pin a
// goroutine and a descriptor for the life of the run.
func TestSilentConnectionDoesNotHoldAGoroutine(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	_, l, log, mu := testServer(t, []string{"github.com"})
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// The deadline is 15s; assert the connection is not simply forgotten by
	// checking the handler eventually records a decision for it.
	deadline := time.Now().Add(handshakeTimeout + 5*time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(*log)
		mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Error("a silent connection was never timed out")
}
