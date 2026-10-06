package studio

import (
	"context"
	"errors"
	"net/http"
)

// DesktopPort is where sandbox-desktop (images/desktop) serves VNC, on the
// guest's own loopback.
const DesktopPort = 5900

// desktop bridges a browser WebSocket to a sandbox's desktop: the VNC stream
// of DesktopPort, reached through the API's tunnel. Binary frames both ways,
// carrying the RFB protocol unchanged; the client that draws it is Studio's
// own, bundled in the UI.
//
// This is deliberately not a general tunnel. The port is fixed, so a page that
// got hold of the token could reach one service the user asked a desktop to
// run and nothing else in the sandbox. And nothing the guest sends is served
// as a page: an HTTP service from inside the sandbox, shown on Studio's
// origin, would run the guest's script beside the token. Here the guest's
// bytes only ever reach a VNC decoder, as a process's output only ever
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
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	// Before the upgrade, so a sandbox with no desktop running is an HTTP
	// error the page can show, not a socket that opens and drops.
	tun, err := s.clientFor(r).Tunnel(ctx, ref, DesktopPort)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer tun.Close()
	ws, err := upgradeWebSocket(w, r)
	if err != nil {
		if !errors.Is(err, errUpgradeAborted) {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	defer ws.close(wsCloseNormal, "")
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
