package server

import (
	"context"
	"encoding/binary"
	"net/http"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
)

// attach upgrades the connection (Upgrade: api.StreamProtocol) to a two-way
// stream of guestproto frames on a running process: stdin, end-of-input,
// signals and resizes in; output and, last, the exit code out. It is the
// way a terminal session is held open over plain HTTP, the unix socket and TLS
// alike. Output is replayed from the start, so attaching late, or again, shows
// everything. Disconnecting detaches; it does not stop the process: that is
// what signal and terminate are for.
func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), api.StreamProtocol) ||
		!strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "attach needs Upgrade: "+api.StreamProtocol)
		return
	}
	_, pr, ok := s.process(w, r)
	if !ok {
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "this connection cannot be upgraded")
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + api.StreamProtocol + "\r\n\r\n")
	if brw.Flush() != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// In: frames from the client to the process. A closed connection ends the
	// output side too, which is the detach.
	go func() {
		defer cancel()
		for {
			typ, payload, err := guestproto.ReadFrame(brw.Reader)
			if err != nil {
				return
			}
			switch typ {
			case guestproto.FrameStdin:
				_, _ = pr.proc.Stdin().Write(payload)
			case guestproto.FrameStdinEOF:
				_ = pr.proc.Stdin().Close()
			case guestproto.FrameSignal:
				for _, sig := range api.Signals {
					if string(payload) == sig {
						_ = pr.proc.Signal(sig)
					}
				}
			case guestproto.FrameResize:
				if rz, ok := pr.proc.(backend.Resizer); ok && len(payload) == 4 {
					_ = rz.Resize(binary.BigEndian.Uint16(payload[0:2]), binary.BigEndian.Uint16(payload[2:4]))
				}
			}
		}
	}()

	// Out: the process's output, from the start, then the exit code.
	_ = pr.log.follow(ctx, func(ev api.OutputEvent) error {
		if ev.ExitCode != nil {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(int32(*ev.ExitCode)))
			return guestproto.WriteFrame(conn, guestproto.FrameExit, b[:])
		}
		typ := guestproto.FrameStdout
		if ev.Stream == "stderr" {
			typ = guestproto.FrameStderr
		}
		for data := ev.Data; len(data) > 0; {
			n := min(len(data), guestproto.MaxFrame)
			if err := guestproto.WriteFrame(conn, typ, data[:n]); err != nil {
				return err
			}
			data = data[n:]
		}
		return nil
	})
}
