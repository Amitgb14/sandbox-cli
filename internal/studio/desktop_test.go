package studio

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// vncNode stands in for sandboxd's tunnel endpoint with a VNC server behind
// it: after the upgrade it sends an RFB banner and echoes what it is sent. It
// records the port each tunnel asked for, and closes when the bridge does.
type vncNode struct {
	mu     sync.Mutex
	ports  []string
	closed chan struct{}
}

func (n *vncNode) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sandboxd-token" {
			http.Error(w, "token", http.StatusUnauthorized)
			return
		}
		if strings.Contains(r.URL.Path, "nodesk") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":{"code":"unavailable","message":"connection refused"}}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/tunnel") || r.Header.Get("Upgrade") != api.TunnelProtocol {
			http.Error(w, "not a tunnel", http.StatusBadRequest)
			return
		}
		n.mu.Lock()
		n.ports = append(n.ports, r.URL.Query().Get("port"))
		n.mu.Unlock()
		conn, brw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + api.TunnelProtocol + "\r\n\r\nRFB 003.008\n")
		_ = brw.Flush()
		_, _ = io.Copy(conn, brw.Reader) // echo, until the bridge closes the tunnel
		close(n.closed)
	})
}

func desktopUnderTest(t *testing.T) (*vncNode, string) {
	t.Helper()
	node := &vncNode{closed: make(chan struct{})}
	sd := httptest.NewServer(node.handler(t))
	t.Cleanup(sd.Close)
	c, err := api.NewClient(sd.URL, "sandboxd-token")
	if err != nil {
		t.Fatal(err)
	}
	st := httptest.NewServer((&Server{Client: c, Context: "t", Token: testToken}).Handler())
	t.Cleanup(st.Close)
	return node, strings.TrimPrefix(st.URL, "http://")
}

// sendMaskedFrame writes one masked client frame, as a browser does.
func sendMaskedFrame(t *testing.T, w io.Writer, opcode byte, payload []byte) {
	t.Helper()
	mask := []byte{1, 2, 3, 4}
	b := append([]byte(nil), payload...)
	for i := range b {
		b[i] ^= mask[i%4]
	}
	if len(b) >= 126 {
		t.Fatal("test frames are short")
	}
	if _, err := w.Write(append(append([]byte{0x80 | opcode, byte(0x80 | len(b))}, mask...), b...)); err != nil {
		t.Fatal(err)
	}
}

// The desktop bridge carries the VNC stream both ways as binary frames, to
// the one port a desktop serves on, whatever the page asks for; it needs the
// token like every /api call; and the browser going away closes the tunnel.
func TestDesktopBridge(t *testing.T) {
	node, addr := desktopUnderTest(t)

	if _, resp, _ := dialWS(t, addr, "/api/ws/desktop?sandbox=sbx_1"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without the token: %d", resp.StatusCode)
	}

	conn, resp, br := dialWS(t, addr, "/api/ws/desktop?sandbox=sbx_1&port=22&token="+testToken)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake: %d", resp.StatusCode)
	}
	if op, payload := readServerFrame(t, br); op != wsOpBinary || string(payload) != "RFB 003.008\n" {
		t.Fatalf("first frame: op %d %q; want the server's RFB banner as binary", op, payload)
	}
	// A text frame is not RFB and goes nowhere; the binary one after it is
	// what the VNC server sees, and echoes.
	sendMaskedFrame(t, conn, wsOpText, []byte("not vnc"))
	sendMaskedFrame(t, conn, wsOpBinary, []byte("RFB 003.008\n"))
	if op, payload := readServerFrame(t, br); op != wsOpBinary || string(payload) != "RFB 003.008\n" {
		t.Fatalf("echo: op %d %q", op, payload)
	}

	node.mu.Lock()
	ports := append([]string(nil), node.ports...)
	node.mu.Unlock()
	if len(ports) != 1 || ports[0] != "5900" {
		t.Fatalf("tunnel ports %v; want only 5900, never the page's 22", ports)
	}

	conn.Close()
	select {
	case <-node.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel stayed open after the browser left")
	}
}

// A sandbox whose desktop is not running is an HTTP error before any
// upgrade, so the page can say so instead of seeing a socket that drops.
func TestDesktopBridgeRefusalIsAnHTTPError(t *testing.T) {
	_, addr := desktopUnderTest(t)
	_, resp, _ := dialWS(t, addr, "/api/ws/desktop?sandbox=nodesk&token="+testToken)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d; want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if !strings.Contains(string(body), "connection refused") {
		t.Fatalf("body %q", body)
	}
	if _, resp, _ := dialWS(t, addr, "/api/ws/desktop?token="+testToken); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no sandbox: %d", resp.StatusCode)
	}
}

// A page on another origin cannot open the desktop even with the token: a
// browser always sends Origin on a WebSocket, and the guard refuses a foreign
// one before any tunnel is asked for.
func TestDesktopBridgeRefusesAnotherOrigin(t *testing.T) {
	node, addr := desktopUnderTest(t)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "GET /api/ws/desktop?sandbox=sbx_1&token="+testToken+" HTTP/1.1\r\n"+
		"Host: "+addr+"\r\nOrigin: http://evil.example\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: "+wsTestKey+"\r\nSec-WebSocket-Version: 13\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d; want 403", resp.StatusCode)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if len(node.ports) != 0 {
		t.Fatalf("a tunnel was opened for a foreign origin: %v", node.ports)
	}
}
