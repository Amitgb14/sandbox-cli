package studioapi

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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

func TestLogsOverWebSocket(t *testing.T) {
	s, _ := newTestServer(t)
	create := doRequest(t, s.Handler(), http.MethodPost, "/runs", RunCreateRequest{
		Command: []string{"true"},
		Branch:  "feature-ws",
	})
	run := decodeBody[Run](t, create)

	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	_, resp, br := dialWS(t, addr, "/runs/"+run.ID+"/logs")
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d, want 101", resp.StatusCode)
	}
	sum := sha1.Sum([]byte(wsTestKey + wsGUID))
	if got, want := resp.Header.Get("Sec-WebSocket-Accept"), base64.StdEncoding.EncodeToString(sum[:]); got != want {
		t.Errorf("Sec-WebSocket-Accept = %q, want %q", got, want)
	}

	// fakeRuntime.Logs writes one line to each stream, so the stream is: two log
	// events, an end event, then a close frame.
	var events []LogEvent
	for {
		op, payload := readServerFrame(t, br)
		if op == wsOpClose {
			if len(payload) < 2 || binary.BigEndian.Uint16(payload) != wsCloseNormal {
				t.Errorf("close payload = %v, want code %d", payload, wsCloseNormal)
			}
			break
		}
		if op != wsOpText {
			t.Fatalf("unexpected opcode %#x", op)
		}
		var ev LogEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			t.Fatalf("frame is not a LogEvent: %v (%s)", err, payload)
		}
		events = append(events, ev)
	}

	if len(events) != 3 {
		t.Fatalf("got %d events, want 3 (stdout, stderr, end): %+v", len(events), events)
	}
	var sawStdout, sawStderr bool
	for _, ev := range events[:2] {
		if ev.Type != LogEventLog {
			t.Errorf("event type = %q, want %q", ev.Type, LogEventLog)
		}
		switch ev.Stream {
		case StreamStdout:
			sawStdout = ev.Data == "hello from stdout"
		case StreamStderr:
			sawStderr = ev.Data == "hello from stderr"
		}
	}
	if !sawStdout || !sawStderr {
		t.Errorf("missing a stream: stdout=%v stderr=%v (%+v)", sawStdout, sawStderr, events)
	}
	if events[2].Type != LogEventEnd {
		t.Errorf("last event type = %q, want %q — a client cannot otherwise tell a "+
			"finished stream from a dropped connection", events[2].Type, LogEventEnd)
	}
}

// TestWebSocketHandshakeRejections pins that a malformed upgrade gets a normal
// HTTP error rather than a half-open connection: nothing has been written at the
// point upgradeWebSocket fails, which is what makes that possible.
func TestWebSocketHandshakeRejections(t *testing.T) {
	s, _ := newTestServer(t)
	create := doRequest(t, s.Handler(), http.MethodPost, "/runs", RunCreateRequest{
		Command: []string{"true"},
		Branch:  "feature-ws-bad",
	})
	run := decodeBody[Run](t, create)

	srv := httptest.NewServer(s.Handler())
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
			fmt.Fprintf(conn, "GET /runs/%s/logs HTTP/1.1\r\nHost: %s\r\n%s\r\n", run.ID, addr, tc.headers)
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("reading response: %v", err)
			}
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

// TestWebSocketTokenViaQueryParameter covers the one place a token may travel
// outside a header, and why: the browser WebSocket API cannot set request
// headers, so a token-protected server would otherwise be unable to serve a
// browser log stream at all.
func TestWebSocketTokenViaQueryParameter(t *testing.T) {
	s, _ := newTestServer(t)
	create := doRequest(t, s.Handler(), http.MethodPost, "/runs", RunCreateRequest{
		Command: []string{"true"},
		Branch:  "feature-ws-token",
	})
	run := decodeBody[Run](t, create)

	s.Token = "secret"
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	_, resp, _ := dialWS(t, addr, "/runs/"+run.ID+"/logs")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("upgrade with no token = %d, want 401", resp.StatusCode)
	}

	_, resp2, _ := dialWS(t, addr, "/runs/"+run.ID+"/logs?token=secret")
	if resp2.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("upgrade with ?token= = %d, want 101", resp2.StatusCode)
	}

	_, resp3, _ := dialWS(t, addr, "/runs/"+run.ID+"/logs?token=wrong")
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Errorf("upgrade with a wrong ?token= = %d, want 401", resp3.StatusCode)
	}

	// The query-string route is for handshakes only. Without the method check, any
	// request could authenticate by query string just by claiming to be an upgrade
	// — and a POST that launches a container is the last place to accept a
	// credential from a URL.
	req := httptest.NewRequest(http.MethodPost, "/runs?token=secret", strings.NewReader(`{"command":["echo"]}`))
	req.Host = testHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST /runs?token= with upgrade headers = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// TestSSERemainsTheDefaultTransport pins that a plain GET still gets Server-Sent
// Events. Both transports carry the identical LogEvent payload, which is what
// lets a client pick one without changing how it reads the stream.
func TestSSERemainsTheDefaultTransport(t *testing.T) {
	s, _ := newTestServer(t)
	create := doRequest(t, s.Handler(), http.MethodPost, "/runs", RunCreateRequest{
		Command: []string{"true"},
		Branch:  "feature-sse",
	})
	run := decodeBody[Run](t, create)

	rec := doRequest(t, s.Handler(), http.MethodGet, "/runs/"+run.ID+"/logs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"event: log",
		`"stream":"stdout"`,
		`"data":"hello from stdout"`,
		`"stream":"stderr"`,
		"event: end",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE stream missing %q:\n%s", want, body)
		}
	}
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
			// A 16-bit length of 65535, well past wsMaxClientFrame. The refusal must
			// happen on the header alone — reading the payload first is the whole
			// thing the cap exists to avoid.
			client.Write([]byte{0x81, 0xFE, 0xFF, 0xFF, 0, 0, 0, 0})
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
	go ws.readLoop(func() { close(ended) })

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
