package studioapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// handleRunLogs is GET /runs/{id}/logs, over a WebSocket when the client asks to
// upgrade and Server-Sent Events otherwise.
//
// Both carry the identical JSON payload — a LogEvent per frame or per `data:`
// line — so a frontend picks a transport without changing how it reads one. The
// WebSocket is what a browser wants (EventSource cannot carry an Authorization
// header, and a page that already holds a socket for everything else should not
// need a second mechanism); SSE is what curl and any HTTP client want, and needs
// no handshake to demonstrate.
//
// follow=1 streams new output until the run ends or the client disconnects,
// matching `sandbox-cli logs --follow`; without it, everything buffered is sent
// once and the stream ends.
func (s *Server) handleRunLogs(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if isWebSocketUpgrade(r) {
		s.streamLogsWS(w, r, c)
		return
	}
	s.streamLogsSSE(w, r, c)
}

// streamLogsWS runs the stream over a WebSocket.
//
// Note what has already happened by the time this is reached: a browser sends
// Origin on a WebSocket handshake and the middleware has checked it, which
// matters more here than on any other route — a WebSocket upgrade is exempt from
// CORS entirely, so reflecting origins would have protected nothing. See
// guard.go.
func (s *Server) streamLogsWS(w http.ResponseWriter, r *http.Request, c runtime.ContainerInfo) {
	ws, err := upgradeWebSocket(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// A hijacked connection is no longer watched by net/http, so a closed tab
	// arrives as a read error in readLoop and nowhere else. Cancelling this context
	// is what stops `docker logs --follow` when that happens.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go ws.readLoop(cancel)
	go ws.keepalive(ctx)

	// emit is called from docker's two output goroutines, so the failure flag needs
	// the same care the frame writer already takes.
	var failed atomic.Bool
	emit := func(ev LogEvent) {
		if err := ws.writeJSON(ev); err != nil {
			// The peer is gone or stalled past the write deadline. Stop the log
			// stream rather than block on every subsequent line.
			failed.Store(true)
			cancel()
		}
	}
	logsErr := s.streamLogs(ctx, c.ID, r.URL.Query().Has("follow"), emit)

	switch {
	case failed.Load():
		ws.close(wsCloseInternal, "write failed")
	case logsErr != nil && ctx.Err() == nil:
		_ = ws.writeJSON(LogEvent{Type: LogEventError, Error: logsErr.Error()})
		ws.close(wsCloseInternal, "log stream failed")
	default:
		_ = ws.writeJSON(LogEvent{Type: LogEventEnd})
		ws.close(wsCloseNormal, "")
	}
}

// streamLogsSSE runs the stream as Server-Sent Events.
func (s *Server) streamLogsSSE(w http.ResponseWriter, r *http.Request, c runtime.ContainerInfo) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported by this connection"))
		return
	}
	writeSSEHeaders(w, flusher)

	// One mutex for the whole response: docker writes stdout and stderr from two
	// concurrent goroutines, and an http.ResponseWriter is not safe for concurrent
	// use. (The WebSocket path needs no equivalent — wsConn serializes writes.)
	var mu sync.Mutex
	emit := func(ev LogEvent) {
		mu.Lock()
		defer mu.Unlock()
		writeSSE(w, string(ev.Type), ev)
		flusher.Flush()
	}
	logsErr := s.streamLogs(r.Context(), c.ID, r.URL.Query().Has("follow"), emit)

	// ctx.Err() != nil means the client went away or the request ended — not a
	// failure worth reporting, the same bargain runtime.DockerCLI.Logs itself
	// makes for Ctrl-C.
	if logsErr != nil && r.Context().Err() == nil {
		emit(LogEvent{Type: LogEventError, Error: logsErr.Error()})
		return
	}
	emit(LogEvent{Type: LogEventEnd})
}

// streamLogs is the transport-independent half: it splits the container's two
// output streams into lines and hands each to emit. Shared by both transports on
// purpose — a second copy of the line-splitting is exactly the kind of duplicate
// that drifts (see CLAUDE.md on the two copies of Claude's bootstrap script),
// and the buffering of a partial final line is the part that would drift first.
func (s *Server) streamLogs(ctx context.Context, id string, follow bool, emit func(LogEvent)) error {
	stdout := &lineWriter{stream: StreamStdout, emit: emit}
	stderr := &lineWriter{stream: StreamStderr, emit: emit}
	err := s.RT.Logs(ctx, id, follow, stdout, stderr)
	stdout.flushPartial()
	stderr.flushPartial()
	return err
}

// lineWriter is an io.Writer that buffers partial writes and calls emit once per
// complete line. One per stream, so buf needs no lock; emit is shared between the
// two and must be safe for concurrent use.
type lineWriter struct {
	stream string
	emit   func(LogEvent)
	buf    []byte
}

func (lw *lineWriter) Write(p []byte) (int, error) {
	lw.buf = append(lw.buf, p...)
	for {
		i := bytes.IndexByte(lw.buf, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimSuffix(lw.buf[:i], []byte("\r")))
		lw.buf = lw.buf[i+1:]
		lw.emit(LogEvent{Type: LogEventLog, Stream: lw.stream, Data: line})
	}
	return len(p), nil
}

// flushPartial emits whatever is left once the underlying stream has ended, so a
// container that exits mid-line without a trailing newline does not lose its last
// line of output.
func (lw *lineWriter) flushPartial() {
	if len(lw.buf) == 0 {
		return
	}
	lw.emit(LogEvent{Type: LogEventLog, Stream: lw.stream, Data: string(lw.buf)})
	lw.buf = nil
}

func writeSSEHeaders(w http.ResponseWriter, flusher http.Flusher) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Nginx and friends buffer a proxied response by default, which turns a live
	// stream into one delivery at the end.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
}

// writeSSE writes one Server-Sent Event. Caller holds whatever lock guards w.
func writeSSE(w http.ResponseWriter, event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	// SSE frames a `data:` field per line, so a payload can never itself contain
	// a bare newline; JSON-encoding v already guarantees that.
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
}
