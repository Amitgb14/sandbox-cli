package studioapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// handleRunLogs is GET /runs/{id}/logs. It streams Server-Sent Events rather
// than a WebSocket: this repository's convention is standard library plus
// cobra and yaml.v3 only (see CLAUDE.md), and Go's net/http has no server-side
// WebSocket support, so a WebSocket here would mean either hand-rolling the
// framing/handshake or adding a dependency for what is, in this direction, an
// ordinary unidirectional stream — exactly what SSE is for. `docker logs -f`
// itself is unidirectional; there is nothing a client would send back on this
// connection. follow=1 streams new output until the run ends or the client
// disconnects, matching `sandbox-cli logs --follow`; without it, everything
// buffered is sent once and the stream closes.
func (s *Server) handleRunLogs(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	// Without follow the caller wants the log as a document, not a stream, and
	// gets JSON. SSE stays for follow=1, where the connection is the point.
	//
	// Both shapes exist because they answer different questions: "show me what
	// this run wrote" is a fetch, and framing it as an event stream forces every
	// client to implement a parser to read a finished container's output.
	if !r.URL.Query().Has("follow") {
		lines, err := s.logLines(r.Context(), c.ID)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, lines)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported by this connection"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// One mutex shared by both streams: docker writes stdout and stderr from two
	// concurrent goroutines internally (cmd.Run with non-file Writers), and an
	// http.ResponseWriter is not safe for concurrent use.
	var mu sync.Mutex
	stdout := &sseLineWriter{mu: &mu, w: w, flusher: flusher, stream: "stdout"}
	stderr := &sseLineWriter{mu: &mu, w: w, flusher: flusher, stream: "stderr"}

	ctx := r.Context()
	follow := r.URL.Query().Has("follow")
	logsErr := s.RT.Logs(ctx, c.ID, follow, stdout, stderr)
	stdout.flushPartial()
	stderr.flushPartial()

	// ctx.Err() != nil means the client went away or the request ended — not a
	// failure worth reporting, the same bargain runtime.DockerCLI.Logs itself
	// makes for Ctrl-C.
	if logsErr != nil && ctx.Err() == nil {
		mu.Lock()
		writeSSE(w, "error", ErrorResponse{Error: logsErr.Error()})
		flusher.Flush()
		mu.Unlock()
	}
}

// sseLineWriter is an io.Writer that buffers partial lines and emits one SSE
// `log` event per complete line, tagged with which stream it came from.
type sseLineWriter struct {
	mu      *sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
	stream  string
	buf     []byte
}

func (sw *sseLineWriter) Write(p []byte) (int, error) {
	sw.buf = append(sw.buf, p...)
	for {
		i := bytes.IndexByte(sw.buf, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimSuffix(sw.buf[:i], []byte("\r")))
		sw.buf = sw.buf[i+1:]
		sw.mu.Lock()
		writeSSE(sw.w, "log", LogEvent{Stream: sw.stream, Data: line})
		sw.flusher.Flush()
		sw.mu.Unlock()
	}
	return len(p), nil
}

// flushPartial emits whatever is left in the buffer once the underlying stream
// has ended, so a container that exits mid-line without a trailing newline does
// not lose its last line of output.
func (sw *sseLineWriter) flushPartial() {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if len(sw.buf) == 0 {
		return
	}
	writeSSE(sw.w, "log", LogEvent{Stream: sw.stream, Data: string(sw.buf)})
	sw.flusher.Flush()
	sw.buf = nil
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

// logLines reads a container's whole log as structured lines.
//
// Through runtime.Controller.Logs rather than by running the engine directly,
// even though the engine could add --timestamps and this cannot. The Controller
// is what the tests fake, and a handler that reached past it would be shipping a
// path no test covers — the interface is also the seam that lets a second
// backend exist at all.
//
// So TS is empty here, and that is reported rather than filled in: docker's
// plain log carries no per-line time, and stamping every line with "when the
// server read it" would be a value that looks like evidence and is not one.
// Timestamps are available on the follow=1 stream, where each line is seen as it
// happens.
//
// The two streams are kept apart because which one a line came from is how a
// reader separates the agent's own output from the egress proxy's DENY lines
// interleaved with it.
func (s *Server) logLines(ctx context.Context, id string) ([]LogLine, error) {
	var outBuf, errBuf bytes.Buffer
	if err := s.RT.Logs(ctx, id, false, &outBuf, &errBuf); err != nil {
		return nil, fmt.Errorf("reading logs: %w", err)
	}

	lines := make([]LogLine, 0, 64)
	for _, part := range []struct {
		buf    *bytes.Buffer
		stream string
	}{{&outBuf, "stdout"}, {&errBuf, "stderr"}} {
		for _, raw := range strings.Split(part.buf.String(), "\n") {
			if raw == "" {
				continue
			}
			lines = append(lines, LogLine{Stream: part.stream, Text: raw})
		}
	}
	for i := range lines {
		lines[i].Seq = i
	}
	return lines, nil
}
