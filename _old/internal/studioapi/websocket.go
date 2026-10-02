package studioapi

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A minimal server-side WebSocket, written against RFC 6455 rather than pulled
// in as a dependency: this repository's stated convention is standard library
// plus cobra and yaml.v3 (see CLAUDE.md), and what the log stream actually needs
// is a small, well-specified subset — the handshake, unmasked server text
// frames, and enough of the client direction to answer a ping and notice a close.
//
// What it deliberately does not implement, because nothing here uses it:
// permessage-deflate, subprotocol negotiation, and reassembly of fragmented
// client messages (control frames, which are never fragmented, are the only
// client frames this server acts on). Anything beyond that belongs in a real
// library, and that would be the moment to argue for the dependency.

// wsGUID is the fixed salt RFC 6455 §4.2.2 specifies for the accept token. Its
// only purpose is proving to the client that it reached a server that speaks
// WebSocket rather than one that echoed a header back.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Frame opcodes (RFC 6455 §5.2).
const (
	wsOpText  byte = 0x1
	wsOpClose byte = 0x8
	wsOpPing  byte = 0x9
	wsOpPong  byte = 0xA
)

// Close codes (RFC 6455 §7.4.1).
const (
	wsCloseNormal   uint16 = 1000
	wsCloseInternal uint16 = 1011
)

const (
	// wsMaxClientFrame caps an inbound payload. This server reads no application
	// data from the client at all — the stream is one-directional — so the cap only
	// needs to accommodate a close reason, and a client claiming a gigabyte
	// payload should cost nothing to refuse.
	wsMaxClientFrame = 4096

	wsWriteTimeout = 10 * time.Second
	// wsPingInterval keeps an idle stream alive through anything that reaps quiet
	// connections; wsIdleTimeout is how long a peer may say nothing at all before
	// its half of the connection is presumed gone. A browser answers ping with pong
	// automatically, so a live client always refreshes the deadline.
	wsPingInterval = 30 * time.Second
	wsIdleTimeout  = 3 * wsPingInterval
)

// isWebSocketUpgrade reports whether a request is asking to upgrade. Both
// headers are checked the way RFC 7230 defines them — Connection is a
// comma-separated token list and may arrive as repeated headers — rather than by
// string equality, which misses the "keep-alive, Upgrade" some clients send.
func isWebSocketUpgrade(r *http.Request) bool {
	return headerHasToken(r.Header.Values("Connection"), "upgrade") &&
		headerHasToken(r.Header.Values("Upgrade"), "websocket")
}

func headerHasToken(values []string, token string) bool {
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// wsConn is one upgraded connection. Every write goes through writeFrame, which
// serializes on mu: the log stream has two concurrent producers (docker writes a
// container's stdout and stderr from separate goroutines) plus the keepalive
// ticker and the reader's pong replies, and interleaved frames on the wire are
// not a recoverable error but a protocol violation.
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader

	mu     sync.Mutex
	closed bool
}

// errUpgradeAborted marks the one class of upgrade failure that happens *after*
// the hijack: the connection is already gone, so a caller must not try to answer
// with an HTTP status it can no longer send.
var errUpgradeAborted = errors.New("websocket upgrade aborted")

// upgradeWebSocket performs the handshake and hands back the hijacked
// connection. Every validation happens before the hijack, so an error that is
// not errUpgradeAborted leaves the response untouched and the caller can still
// answer with a normal HTTP status.
func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if v := r.Header.Get("Sec-WebSocket-Version"); v != "13" {
		return nil, fmt.Errorf("unsupported Sec-WebSocket-Version %q: this server speaks 13", v)
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, fmt.Errorf("missing Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("this connection cannot be upgraded to a WebSocket")
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return nil, fmt.Errorf("hijacking the connection: %w", err)
	}

	sum := sha1.Sum([]byte(key + wsGUID))
	handshake := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	if _, err := buf.WriteString(handshake); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: writing the handshake: %v", errUpgradeAborted, err)
	}
	if err := buf.Flush(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: flushing the handshake: %v", errUpgradeAborted, err)
	}
	_ = conn.SetWriteDeadline(time.Time{})
	// buf.Reader, not a fresh one: a client that sent data immediately after the
	// handshake already has those bytes buffered here, and reading past them would
	// desynchronize the frame stream.
	return &wsConn{conn: conn, br: buf.Reader}, nil
}

// writeJSON sends one value as a text frame.
func (c *wsConn) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.writeFrame(wsOpText, b)
}

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	// One buffer, one Write: a header and payload written separately can be seen
	// as two segments, and a client reading with a short timeout is entitled to
	// treat that badly.
	frame := make([]byte, 0, len(payload)+10)
	frame = append(frame, 0x80|opcode) // FIN set: this server never fragments
	switch n := len(payload); {
	case n < 126:
		frame = append(frame, byte(n))
	case n <= 0xFFFF:
		frame = append(frame, 126, 0, 0)
		binary.BigEndian.PutUint16(frame[2:], uint16(n))
	default:
		frame = append(frame, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(frame[2:], uint64(n))
	}
	// No masking key: RFC 6455 §5.1 forbids a server masking its frames.
	frame = append(frame, payload...)
	if err := c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
		return err
	}
	_, err := c.conn.Write(frame)
	return err
}

// close sends a close frame and shuts the connection down. Safe to call twice —
// the second call is a no-op — because the read loop and the handler both reach
// for it on the paths where either could finish first.
func (c *wsConn) close(code uint16, reason string) {
	payload := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(payload, code)
	payload = append(payload, reason...)
	_ = c.writeFrame(wsOpClose, payload)

	c.mu.Lock()
	already := c.closed
	c.closed = true
	c.mu.Unlock()
	if !already {
		c.conn.Close()
	}
}

// readLoop consumes client frames until the connection ends, answering pings and
// stopping at a close frame or any read error. It calls onEnd exactly once.
//
// This is what makes client disconnection actionable: a hijacked connection is no
// longer watched by net/http, so nothing else would notice a closed browser tab,
// and `docker logs --follow` would keep running with nobody reading it.
func (c *wsConn) readLoop(onEnd func()) {
	defer onEnd()
	for {
		if err := c.conn.SetReadDeadline(time.Now().Add(wsIdleTimeout)); err != nil {
			return
		}
		op, payload, err := c.readFrame()
		if err != nil {
			return
		}
		switch op {
		case wsOpClose:
			return
		case wsOpPing:
			if err := c.writeFrame(wsOpPong, payload); err != nil {
				return
			}
		}
		// Anything else — a text frame, a pong — is discarded: this stream carries
		// no client-to-server application data, and receiving it is still proof the
		// peer is alive, which is all the deadline above needs.
	}
}

func (c *wsConn) readFrame() (opcode byte, payload []byte, err error) {
	var head [2]byte
	if _, err := io.ReadFull(c.br, head[:]); err != nil {
		return 0, nil, err
	}
	opcode = head[0] & 0x0F
	masked := head[1]&0x80 != 0
	length := uint64(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if !masked {
		// RFC 6455 §5.1: a client frame that is not masked must fail the connection.
		// Enforced rather than tolerated, because an unmasked client is either
		// broken or not a browser, and guessing which is not this code's job.
		return 0, nil, fmt.Errorf("client frame is not masked")
	}
	if length > wsMaxClientFrame {
		return 0, nil, fmt.Errorf("client frame of %d bytes exceeds the %d-byte limit", length, wsMaxClientFrame)
	}
	var mask [4]byte
	if _, err := io.ReadFull(c.br, mask[:]); err != nil {
		return 0, nil, err
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return opcode, payload, nil
}

// keepalive pings until ctx ends. A failed ping means the peer is gone; the read
// loop's deadline reaches the same conclusion, and whichever gets there first
// ends the stream.
func (c *wsConn) keepalive(ctx context.Context) {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.writeFrame(wsOpPing, nil); err != nil {
				return
			}
		}
	}
}
