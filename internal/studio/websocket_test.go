package studio

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// These tests speak the protocol rather than mock it: the handshake and the
// frame layout are the parts a hand-rolled WebSocket can get subtly wrong, and a
// fake client that shares the implementation's assumptions would agree with every
// one of those mistakes. So the client below builds its own request, checks the
// accept token independently, and parses frames byte by byte.

const wsTestKey = "dGhlIHNhbXBsZSBub25jZQ==" // RFC 6455 §1.3's example key

// dialWS performs a client handshake and returns the raw connection plus the
// handshake response.
func dialWS(t *testing.T, addr, path string) (net.Conn, *http.Response, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dialing %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: keep-alive, Upgrade\r\n" + // a token list, as real clients send
		"Sec-WebSocket-Key: " + wsTestKey + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("writing handshake: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("reading handshake response: %v", err)
	}
	return conn, resp, br
}

// readServerFrame parses one unmasked server frame.
func readServerFrame(t *testing.T, br *bufio.Reader) (opcode byte, payload []byte) {
	t.Helper()
	var head [2]byte
	if _, err := io.ReadFull(br, head[:]); err != nil {
		t.Fatalf("reading frame header: %v", err)
	}
	if head[0]&0x80 == 0 {
		t.Fatalf("server sent a fragmented frame (FIN clear): %08b", head[0])
	}
	if head[1]&0x80 != 0 {
		t.Fatal("server masked its frame, which RFC 6455 §5.1 forbids")
	}
	opcode = head[0] & 0x0F
	length := uint64(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			t.Fatal(err)
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			t.Fatal(err)
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(br, payload); err != nil {
		t.Fatalf("reading %d-byte payload: %v", length, err)
	}
	return opcode, payload
}

// --- frame-level unit tests ---------------------------------------------------

func newWSPair(t *testing.T) (*wsConn, net.Conn) {
	t.Helper()
	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { serverSide.Close(); clientSide.Close() })
	return &wsConn{conn: serverSide, br: bufio.NewReader(serverSide)}, clientSide
}

// TestWriteFrameLengthEncodings walks the three payload-length forms RFC 6455
// defines. The boundaries are the whole point: a 125-byte payload and a 126-byte
// one are framed differently, and getting that wrong produces a stream that works
// until a log line gets long.
func TestWriteFrameLengthEncodings(t *testing.T) {
	for _, size := range []int{0, 1, 125, 126, 65535, 65536} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ws, client := newWSPair(t)
			payload := strings.Repeat("x", size)
			done := make(chan error, 1)
			go func() { done <- ws.writeFrame(wsOpText, []byte(payload)) }()

			br := bufio.NewReader(client)
			op, got := readServerFrame(t, br)
			if err := <-done; err != nil {
				t.Fatalf("writeFrame: %v", err)
			}
			if op != wsOpText {
				t.Errorf("opcode = %#x, want %#x", op, wsOpText)
			}
			if string(got) != payload {
				t.Errorf("payload round-trip lost data: got %d bytes, want %d", len(got), size)
			}
		})
	}
}

// TestReadFrameRequiresMasking pins RFC 6455 §5.1's requirement. Enforced rather
// than tolerated: an unmasked client is either broken or not a browser, and
// guessing which is not this code's job.
func TestReadFrameRequiresMasking(t *testing.T) {
	ws, client := newWSPair(t)
	go func() {
		// FIN + text, length 1, no mask bit, one byte of payload.
		client.Write([]byte{0x81, 0x01, 'a'})
	}()
	if _, _, err := ws.readFrame(); err == nil {
		t.Error("an unmasked client frame was accepted")
	}
}

func TestReadFrameUnmasksAndCapsSize(t *testing.T) {
	t.Run("unmasks", func(t *testing.T) {
		ws, client := newWSPair(t)
		mask := []byte{0xDE, 0xAD, 0xBE, 0xEF}
		body := []byte("ping me")
		masked := make([]byte, len(body))
		for i := range body {
			masked[i] = body[i] ^ mask[i%4]
		}
		go func() {
			frame := append([]byte{0x89, byte(0x80 | len(body))}, mask...) // 0x89: ping
			client.Write(append(frame, masked...))
		}()
		op, payload, err := ws.readFrame()
		if err != nil {
			t.Fatalf("readFrame: %v", err)
		}
		if op != wsOpPing {
			t.Errorf("opcode = %#x, want ping", op)
		}
		if string(payload) != string(body) {
			t.Errorf("payload = %q, want %q", payload, body)
		}
	})

	t.Run("caps size", func(t *testing.T) {
		ws, client := newWSPair(t)
		go func() {
			// A 64-bit length of 1 MiB, past wsMaxClientFrame. The refusal must
			// happen on the header alone — reading the payload first is the whole
			// thing the cap exists to avoid.
			client.Write([]byte{0x81, 0xFF, 0, 0, 0, 0, 0, 0x10, 0, 0, 0, 0, 0, 0})
		}()
		if _, _, err := ws.readFrame(); err == nil {
			t.Error("an oversize client frame was accepted")
		}
	})
}

// TestReadLoopEndsOnClose pins the mechanism that stops `docker logs --follow`
// when a browser tab closes: a hijacked connection is no longer watched by
// net/http, so the read loop noticing is the only signal there is.
func TestReadLoopEndsOnClose(t *testing.T) {
	ws, client := newWSPair(t)
	ended := make(chan struct{})
	go ws.readLoop(nil, func() { close(ended) })

	// A masked, empty close frame.
	if _, err := client.Write([]byte{0x88, 0x80, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("readLoop did not end after a close frame")
	}
}

// The handshake refuses what it cannot speak before hijacking, so the caller
// can still answer with an HTTP status.
func TestWebSocketHandshakeRejections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgradeWebSocket(w, r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ws.close(wsCloseNormal, "")
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	for _, tc := range []struct {
		name    string
		headers string
	}{
		{"wrong version", "Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: " + wsTestKey + "\r\nSec-WebSocket-Version: 8\r\n"},
		{"no key", "Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\n%s\r\n", addr, tc.headers)
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("reading response: %v", err)
			}
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
	// And a good handshake carries the accept token RFC 6455 computes from the key.
	_, resp, _ := dialWS(t, addr, "/")
	sum := sha1.Sum([]byte(wsTestKey + wsGUID))
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		t.Errorf("handshake: %d %q", resp.StatusCode, resp.Header.Get("Sec-WebSocket-Accept"))
	}
}

// Client data frames reach onMessage, unmasked and in order: a terminal's
// keystrokes are the point of the read side now.
func TestReadLoopDeliversDataFrames(t *testing.T) {
	ws, client := newWSPair(t)
	got := make(chan string, 2)
	go ws.readLoop(func(op byte, p []byte) { got <- fmt.Sprintf("%d:%s", op, p) }, func() {})
	send := func(op byte, body string) {
		mask := []byte{1, 2, 3, 4}
		m := []byte(body)
		for i := range m {
			m[i] ^= mask[i%4]
		}
		client.Write(append(append([]byte{0x80 | op, byte(0x80 | len(body))}, mask...), m...))
	}
	send(wsOpText, "ls\r")
	send(wsOpBinary, "\x03")
	for _, want := range []string{"1:ls\r", "2:\x03"} {
		select {
		case g := <-got:
			if g != want {
				t.Errorf("got %q, want %q", g, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a data frame was not delivered")
		}
	}
}
