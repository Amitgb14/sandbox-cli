package egressproxy

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// handshakeTimeout bounds how long one connection may take to reveal what it is
// asking for. A peer that opens a socket and then says nothing must not be able
// to hold a goroutine and a file descriptor indefinitely — the agent can open
// many, and this process is the one thing standing between it and the network.
const handshakeTimeout = 15 * time.Second

// errNoData marks a connection that closed without sending anything.
var errNoData = errors.New("connection sent no data")

// Server enforces the allowlist for connections redirected to it.
type Server struct {
	Match *Matcher
	// Resolve looks up a hostname. Injected so tests do not need DNS.
	Resolve func(host string) ([]net.IP, error)
	// Dial opens the upstream connection. Injected for the same reason.
	Dial func(network, addr string) (net.Conn, error)
	// Log receives one line per decision. Denials are the interesting half: a
	// blocked connection previously left no trace the user could find.
	Log func(Decision)
}

// New returns a Server with real networking wired in.
func New(m *Matcher, log func(Decision)) *Server {
	if log == nil {
		log = func(Decision) {}
	}
	return &Server{
		Match:   m,
		Resolve: func(h string) ([]net.IP, error) { return net.LookupIP(h) },
		Dial:    func(n, a string) (net.Conn, error) { return net.DialTimeout(n, a, 20*time.Second) },
		Log:     log,
	}
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go s.handle(c)
	}
}

// handle decides one connection and either splices it or refuses it.
//
// Nothing is forwarded before the decision. The hostname is read from bytes the
// client has already sent, so no upstream connection exists yet when the
// allowlist is consulted — a denied connection never touches the network.
func (s *Server) handle(client net.Conn) {
	defer client.Close()
	_ = client.SetReadDeadline(time.Now().Add(handshakeTimeout))

	// Sized to the ClientHello limit rather than bufio's 4 KiB default. peekRecord
	// caps its peek at br.Size(), so a smaller buffer silently truncated any hello
	// above ~4 KiB — the extensions vector then overran and the connection was
	// refused as "no hostname". Large ALPN, session-ticket and PQ-hybrid key-share
	// extensions do exceed that in the wild, and the symptom is the worst kind
	// this component can produce: an allowlisted host appearing to fail for no
	// stated reason, rather than a policy denial.
	br := bufio.NewReaderSize(client, maxClientHello+512)
	host, port, explicit, err := s.peek(br)
	if err != nil {
		// A connection that sent nothing at all is a probe or an abandoned dial —
		// the entrypoint's own readiness check is one — and logging it as a denial
		// would put noise in the place denials are meant to be legible. A
		// connection that sent data but named nothing is the interesting case:
		// that is what dialling an address directly looks like.
		if !errors.Is(err, errNoData) {
			s.Log(Decision{Reason: err.Error()})
		}
		return
	}
	// For a redirected connection the port peek() inferred from the protocol
	// shape (443 for TLS, 80 for HTTP) is a guess; the kernel knows what was
	// actually asked for. Only the port is taken — the address deliberately is
	// not, because trusting it would be the address-based matching this package
	// exists to replace.
	if !explicit {
		if _, origPort, oerr := OriginalDestination(client); oerr == nil && origPort > 0 {
			port = origPort
		}
	}
	_ = client.SetReadDeadline(time.Time{})

	if !s.Match.Allows(host) {
		s.Log(Decision{Host: host, Port: port, Reason: "not on the egress allowlist"})
		if explicit {
			// An explicit proxy client understands a status line; a redirected one
			// would receive it as garbage inside its TLS stream.
			fmt.Fprintf(client, "HTTP/1.1 403 Forbidden\r\n\r\n")
		}
		return
	}

	upstream, err := s.dialHost(host, port)
	if err != nil {
		s.Log(Decision{Host: host, Port: port, Allowed: true, Reason: "upstream: " + err.Error()})
		return
	}
	defer upstream.Close()
	s.Log(Decision{Host: host, Port: port, Allowed: true})

	if explicit {
		// The client is waiting for the tunnel to be confirmed before it sends
		// anything else; a redirected client already sent its handshake.
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			return
		}
	}
	splice(client, br, upstream)
}

// peek reads just enough to learn the hostname, leaving everything it read in
// the bufio.Reader so it can still be forwarded.
//
// Three shapes arrive here, and all three are handled because the enforcement is
// an iptables redirect rather than a request that the agent use a proxy:
//
//   - an explicit `CONNECT host:port` — the client was told about the proxy;
//   - a TLS ClientHello — the client dialled the address directly and was
//     redirected, so the name is in SNI;
//   - a plain HTTP request — the name is in the Host header.
func (s *Server) peek(br *bufio.Reader) (host string, port int, explicit bool, err error) {
	first, err := br.Peek(1)
	if err != nil {
		return "", 0, false, errNoData
	}

	if first[0] == 0x16 { // TLS handshake
		hello, err := peekRecord(br)
		if err != nil {
			return "", 0, false, err
		}
		h, err := SNIFromClientHello(hello)
		if err != nil {
			return "", 0, false, err
		}
		return h, 443, false, nil
	}

	// Text: either CONNECT (explicit proxy) or an ordinary request (Host header).
	line, err := peekHeaders(br)
	if err != nil {
		return "", 0, false, err
	}
	if h, p, ok := parseConnect(line); ok {
		return h, p, true, nil
	}
	if h, ok := hostHeader(line); ok {
		return h, 80, false, nil
	}
	return "", 0, false, ErrNoHostname
}

// peekRecord returns the bytes of the first TLS record without consuming them.
func peekRecord(br *bufio.Reader) ([]byte, error) {
	head, err := br.Peek(5)
	if err != nil {
		return nil, errors.New("short TLS record")
	}
	n := int(head[3])<<8 | int(head[4])
	if n <= 0 || n > maxClientHello {
		return nil, errNotClientHello
	}
	total := 5 + n
	if total > br.Size() {
		total = br.Size()
	}
	b, err := br.Peek(total)
	if err != nil && len(b) < 6 {
		return nil, errors.New("truncated TLS record")
	}
	return b, nil
}

// parseConnect reads a `CONNECT host:port HTTP/1.1` request line.
func parseConnect(b []byte) (string, int, bool) {
	line, _, ok := bytes.Cut(b, []byte("\r\n"))
	if !ok {
		return "", 0, false
	}
	f := strings.Fields(string(line))
	if len(f) < 2 || !strings.EqualFold(f[0], "CONNECT") {
		return "", 0, false
	}
	hostport := f[1]
	port := 443
	if h, p, err := net.SplitHostPort(hostport); err == nil {
		hostport = h
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	// The redirect only ever sends tcp/80 and tcp/443 here, so an explicit
	// CONNECT to anything else is a client reaching the proxy directly on
	// loopback and asking it to open a port the redirect would never have
	// produced. Not a widening — the address rules still permit an allowlisted
	// IP on any port — but this component's remit is the two web ports, and a
	// component should not quietly do more than it says.
	if port != 80 && port != 443 {
		return "", 0, false
	}
	h := normalizeHost(hostport)
	if h == "" || !plausibleHostname(h) {
		return "", 0, false
	}
	return h, port, true
}

// hostHeader reads the Host header of a plain HTTP request.
func hostHeader(b []byte) (string, bool) {
	for _, line := range strings.Split(string(b), "\r\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(name), "host") {
			continue
		}
		h := normalizeHost(value)
		if h == "" || !plausibleHostname(h) {
			return "", false
		}
		return h, true
	}
	return "", false
}

// dialHost resolves and connects to the permitted name.
//
// Resolution happens here, per connection, which is the other half of what makes
// this name-based: the previous firewall resolved once at container start, so a
// rotating record broke a domain the user had allowlisted. Here the answer is
// only ever as old as the connection.
func (s *Server) dialHost(host string, port int) (net.Conn, error) {
	ips, err := s.Resolve(host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("cannot resolve %s", host)
	}
	var lastErr error
	for _, ip := range ips {
		c, err := s.Dial("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// splice joins the two connections until either side closes. The buffered reader
// is drained first so the bytes already peeked at are not lost.
func splice(client net.Conn, buffered *bufio.Reader, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(upstream, buffered)
		if c, ok := upstream.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		io.Copy(client, upstream)
		if c, ok := client.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
	}()
	wg.Wait()
}

// peekHeaders returns the request head without consuming it, waiting only for as
// much as the client actually sends.
//
// bufio.Peek(n) blocks until it has n bytes, so peeking a fixed 8 KiB waited for
// bytes a ~60-byte CONNECT request was never going to send — the connection sat
// until its handshake deadline and every explicit-proxy request timed out. Peek
// only what has arrived, and ask for one more byte at a time when the head is
// not yet complete.
func peekHeaders(br *bufio.Reader) ([]byte, error) {
	for {
		n := br.Buffered()
		if n == 0 {
			n = 1 // block for the first byte only
		}
		b, err := br.Peek(n)
		if len(b) > 0 && (bytes.Contains(b, []byte("\r\n\r\n")) || bytes.Contains(b, []byte("\n\n"))) {
			return b, nil
		}
		if err != nil {
			if len(b) > 0 {
				return b, nil // peer stopped talking; decide on what it did send
			}
			return nil, errNoData
		}
		if n >= br.Size() {
			return b, nil // head larger than the buffer: decide on what we have
		}
		// Wait for at least one more byte, then re-examine.
		if _, err := br.Peek(n + 1); err != nil {
			if b, _ := br.Peek(br.Buffered()); len(b) > 0 {
				return b, nil
			}
			return nil, errNoData
		}
	}
}
