package egressproxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// upstreamGreeting is what the fake upstream says the moment it is dialled, so a
// test can tell "the proxy connected me" from "the proxy swallowed it".
const upstreamGreeting = "UPSTREAM-REACHED"

// testServer wires a Server with fake DNS and a fake upstream, so the tests
// exercise the decision path without touching the network.
//
// Nothing here may race a wall clock. The tests in this file used to launch
// `go tls.Client(…).Handshake()` and then either sleep or read with a short
// deadline, which made them depend on a goroutine being scheduled promptly — and
// a loaded CI runner does not promise that. It cost a release: this package
// failed the 0.0.1beta.10 release build after passing sixty local runs. Two of
// the tests were worse than flaky, since a refusal test that has not yet sent
// its handshake passes for the wrong reason. Send bytes synchronously, then wait
// for something the server actually did.
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
		// Drain what the client sends, so the proxy's client→upstream copy never
		// blocks. It deliberately does **not** close c2 afterwards: net.Pipe is
		// unbuffered, so a close here raced the greeting below and could discard
		// it, leaving the client waiting on a deadline for bytes nobody would
		// send again. Cleanup owns the close.
		go func() { _, _ = io.Copy(io.Discard, c2) }()
		go func() { _, _ = io.WriteString(c2, upstreamGreeting) }()
		t.Cleanup(func() { c2.Close(); c1.Close() })
		return c1, nil
	}
	go s.Serve(l)
	t.Cleanup(func() { l.Close() })
	return s, l, &log, &mu
}

// speak sends a real ClientHello for serverName and returns once it is on the
// wire. Synchronous on purpose — see testServer.
func speak(t *testing.T, c net.Conn, serverName string) {
	t.Helper()
	if _, err := c.Write(captureClientHello(t, serverName)); err != nil {
		t.Fatalf("writing ClientHello: %v", err)
	}
}

// awaitDecision waits for the server to judge a connection and returns what it
// decided.
//
// This is the signal a refusal test has to wait on, and waiting on the
// *connection* instead is not good enough — which a mutation showed: comment out
// the handshake and a test that waits only for the socket to close still passes,
// because the server's own silence timeout closes it eventually. Requiring a
// logged decision cannot pass that way, since a connection that says nothing is
// deliberately never logged (see TestSilentConnectionIsTimedOut).
//
// The ceiling only bounds a hang; the decision is recorded as soon as the server
// reaches it, because the handshake was written synchronously.
func awaitDecision(t *testing.T, mu *sync.Mutex, log *[]Decision) Decision {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(*log)
		var d Decision
		if n > 0 {
			d = (*log)[0]
		}
		mu.Unlock()
		if n > 0 {
			return d
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the server never recorded a decision — it never read the handshake, " +
		"so any assertion about what it did next would pass for the wrong reason")
	return Decision{}
}

// readUntil accumulates until want appears, so a reply split across TCP segments
// is not read as a missing one.
func readUntil(t *testing.T, c net.Conn, want string) string {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	var got []byte
	buf := make([]byte, 64)
	for !strings.Contains(string(got), want) {
		n, err := c.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	return string(got)
}

// TestDeniedConnectionNeverReachesUpstream is the property that matters most: a
// refused name must not cause an upstream connection at all. Checking only that
// the client got nothing back would pass even if the proxy had dialled out and
// then dropped the response — which for an exfiltration channel is the whole
// attack.
func TestDeniedConnectionNeverReachesUpstream(t *testing.T) {
	var dialed []string
	var mu sync.Mutex
	s, l, log, logMu := testServer(t, []string{"github.com"})
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
	speak(t, c, "gist.github.com")
	if d := awaitDecision(t, logMu, log); d.Allowed {
		t.Errorf("gist.github.com was allowed: %+v", d)
	}

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
	speak(t, c, "github.com")

	if got := readUntil(t, c, upstreamGreeting); !strings.Contains(got, upstreamGreeting) {
		t.Errorf("allowed host was not tunnelled; read %q", got)
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
		// Synchronous already — the request is written before the read — so this
		// ceiling only bounds a hang, it is not a bet on how fast the server is.
		c.SetReadDeadline(time.Now().Add(30 * time.Second))
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
	s, l, log, logMu := testServer(t, []string{"github.com"})
	s.Dial = func(string, string) (net.Conn, error) {
		mu.Lock()
		dialed++
		mu.Unlock()
		return nil, fmt.Errorf("should not dial")
	}

	c, _ := net.Dial("tcp", l.Addr().String())
	defer c.Close()
	speak(t, c, "") // no ServerName
	if d := awaitDecision(t, logMu, log); d.Allowed {
		t.Errorf("a handshake with no name was allowed: %+v", d)
	}

	mu.Lock()
	defer mu.Unlock()
	if dialed != 0 {
		t.Error("a connection with no hostname was dialled out anyway")
	}
}

// TestSilentConnectionIsTimedOut covers the resource side. The agent can open
// many sockets; one that says nothing must be closed rather than pin a goroutine
// and a descriptor for the life of the run.
//
// The assertion is that the server closes the connection, not that it logs one:
// a connection that sends nothing is deliberately NOT logged, because the
// entrypoint's own readiness probe is one and denials are meant to be legible.
// Asserting on the log would be asserting on the thing that was removed.
func TestSilentConnectionIsTimedOut(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	_, l, _, _ := testServer(t, []string{"github.com"})
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Say nothing. The server should hang up once its handshake deadline passes.
	c.SetReadDeadline(time.Now().Add(handshakeTimeout + 10*time.Second))
	n, err := c.Read(make([]byte, 1))
	if err == nil {
		t.Fatalf("server sent %d bytes to a connection that said nothing", n)
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Error("the server never closed a silent connection; it would hold a goroutine and an fd")
	}
}
