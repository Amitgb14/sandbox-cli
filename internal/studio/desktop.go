package studio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

// DesktopPort is where sandbox-desktop (images/desktop) serves VNC, on the
// guest's own loopback: display :0's port. A desktop the bridge reaches is
// one of the VNC display ports, DesktopPort to MaxDesktopPort (:0 to :99),
// so an agent that starts a display of its own (Xvnc :2, on 5902) can be
// watched too.
const (
	DesktopPort    = 5900
	MaxDesktopPort = 5999
	// scannedDisplays is how many displays, from :0, desktops looks for.
	scannedDisplays = 10
)

// rfbGreeting is the first thing every VNC server sends: "RFB 003.008\n".
var rfbGreeting = regexp.MustCompile(`^RFB \d{3}\.\d{3}\n$`)

// greetingTimeout bounds how long a port has to say it is a VNC server.
const greetingTimeout = 3 * time.Second

// errNotVNC is a port that answered with something other than VNC.
var errNotVNC = errors.New("does not speak VNC")

// desktopPort is the port a request asks for: DesktopPort when it names none,
// and only a VNC display port otherwise.
func desktopPort(q string) (int, error) {
	if q == "" {
		return DesktopPort, nil
	}
	p, err := strconv.Atoi(q)
	if err != nil || p < DesktopPort || p > MaxDesktopPort {
		return 0, fmt.Errorf("port %q: a VNC display port, %d to %d", q, DesktopPort, MaxDesktopPort)
	}
	return p, nil
}

// vncGreeting reads a VNC server's greeting from a fresh tunnel, which a VNC
// server sends before it is sent anything. Anything else is errNotVNC, and
// none of it is passed on. A port that says nothing in time is closed.
func vncGreeting(tun io.ReadWriteCloser) ([]byte, error) {
	b := make([]byte, 12)
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(tun, b)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return nil, errNotVNC
		}
	case <-time.After(greetingTimeout):
		_ = tun.Close()
		<-done
		return nil, errNotVNC
	}
	if !rfbGreeting.Match(b) {
		return nil, errNotVNC
	}
	return b, nil
}

// desktop bridges a browser WebSocket to a sandbox's desktop: the VNC stream
// of one of the VNC display ports, reached through the API's tunnel. Binary
// frames both ways, carrying the RFB protocol unchanged; the client that
// draws it is Studio's own, bundled in the UI.
//
// This is deliberately not a general tunnel. Only the VNC display ports are
// reached, and only a port whose first words are a VNC server's greeting is
// bridged: anything else is refused before the browser is sent a byte of it
// or sends one to it. So a page that got hold of the token could reach the
// desktops the sandbox runs and nothing else in it. And nothing the guest
// sends is served as a page: an HTTP service from inside the sandbox, shown on
// Studio's origin, would run the guest's script beside the token. Here the
// guest's bytes only ever reach a VNC decoder, as a process's output only ever
// reaches a terminal emulator.
func (s *Server) desktop(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		writeErr(w, http.StatusBadRequest, "want a WebSocket upgrade")
		return
	}
	ref := r.URL.Query().Get("sandbox")
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "sandbox: required")
		return
	}
	port, err := desktopPort(r.URL.Query().Get("port"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	// Before the upgrade, so a sandbox with no desktop running is an HTTP
	// error the page can show, not a socket that opens and drops.
	tun, err := s.clientFor(r).Tunnel(ctx, ref, port)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer tun.Close()
	greeting, err := vncGreeting(tun)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("port %d %v", port, err))
		return
	}
	ws, err := upgradeWebSocket(w, r)
	if err != nil {
		if !errors.Is(err, errUpgradeAborted) {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	defer ws.close(wsCloseNormal, "")
	if ws.writeFrame(wsOpBinary, greeting) != nil {
		return
	}
	go ws.keepalive(ctx)
	// The browser's input, to the VNC server. Text frames are not part of
	// RFB and are dropped. Closing the tunnel when the browser goes is what
	// ends the read below.
	go ws.readLoop(func(op byte, p []byte) {
		if op == wsOpBinary {
			_, _ = tun.Write(p)
		}
	}, func() {
		cancel()
		_ = tun.Close()
	})
	buf := make([]byte, 32<<10)
	for {
		n, err := tun.Read(buf)
		if n > 0 {
			if ws.writeFrame(wsOpBinary, buf[:n]) != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// Desktops is GET /api/desktops: the VNC display ports that answer as a VNC
// server in the sandbox, lowest first.
type Desktops struct {
	Ports []int `json:"ports"`
}

// desktops looks for the sandbox's desktops: displays :0 to :9, each tried at
// once through its own tunnel, kept when it greets as a VNC server, and
// closed straight after. The Desktop tab shows what it finds, which may be a
// desktop the image or an agent started rather than one Studio did.
func (s *Server) desktops(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("sandbox")
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "sandbox: required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), greetingTimeout+5*time.Second)
	defer cancel()
	c := s.clientFor(r)
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := Desktops{Ports: []int{}}
	for port := DesktopPort; port < DesktopPort+scannedDisplays; port++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tun, err := c.Tunnel(ctx, ref, port)
			if err != nil {
				return
			}
			defer tun.Close()
			if _, err := vncGreeting(tun); err == nil {
				mu.Lock()
				out.Ports = append(out.Ports, port)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Ints(out.Ports)
	writeJSON(w, http.StatusOK, out)
}
