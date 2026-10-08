package server

import (
	"context"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// maxStreamBytes caps what is kept of each output stream of one process. Past
// it, output is dropped and the log says so: a process printing forever must not
// be able to grow sandboxd's memory forever, and a cut that is not reported reads
// as "this is everything".
const maxStreamBytes = 8 << 20

// outputLog is a process's output, kept so that a follower who arrives late —
// or a second follower — still sees it from the beginning.
type outputLog struct {
	mu        sync.Mutex
	events    []api.OutputEvent
	size      map[string]int
	truncated bool
	exit      *int
	changed   chan struct{} // closed and replaced on every change
}

func newOutputLog() *outputLog {
	return &outputLog{size: map[string]int{}, changed: make(chan struct{})}
}

// writer returns an io.Writer that appends to one stream.
func (l *outputLog) writer(stream string) *streamWriter { return &streamWriter{l, stream} }

type streamWriter struct {
	l      *outputLog
	stream string
}

func (w *streamWriter) Write(p []byte) (int, error) {
	l := w.l
	l.mu.Lock()
	defer l.mu.Unlock()
	room := maxStreamBytes - l.size[w.stream]
	data := p
	if len(data) > room {
		data = data[:max(room, 0)]
		l.truncated = true
	}
	if len(data) > 0 {
		l.events = append(l.events, api.OutputEvent{Stream: w.stream, Data: append([]byte(nil), data...)})
		l.size[w.stream] += len(data)
		l.notify()
	}
	// Report the whole write as accepted: the producer is a process inside a
	// sandbox, and a short write would only make it retry what is being dropped.
	return len(p), nil
}

// markTruncated records that output from before what the log holds was
// lost — a process taken back after a restart, whose sandbox kept only the
// newest of it.
func (l *outputLog) markTruncated() {
	l.mu.Lock()
	l.truncated = true
	l.mu.Unlock()
}

// finish records the exit code and wakes every follower for the last time.
func (l *outputLog) finish(code int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.exit = &code
	l.notify()
}

// notify wakes followers. Caller holds l.mu.
func (l *outputLog) notify() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// follow calls fn for every event from the first, then for each new one as it
// arrives, and finally for an event carrying the exit code. It returns when the
// process has exited and everything was delivered, when fn fails, or when ctx
// ends.
func (l *outputLog) follow(ctx context.Context, fn func(api.OutputEvent) error) error {
	next := 0
	for {
		l.mu.Lock()
		pending := append([]api.OutputEvent(nil), l.events[next:]...)
		next = len(l.events)
		exit := l.exit
		wait := l.changed
		l.mu.Unlock()

		for _, ev := range pending {
			if err := fn(ev); err != nil {
				return err
			}
		}
		if exit != nil {
			code := *exit
			return fn(api.OutputEvent{ExitCode: &code})
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// collect returns everything written so far to each stream.
func (l *outputLog) collect() (stdout, stderr []byte, truncated bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ev := range l.events {
		switch ev.Stream {
		case "stdout":
			stdout = append(stdout, ev.Data...)
		case "stderr":
			stderr = append(stderr, ev.Data...)
		}
	}
	return stdout, stderr, l.truncated
}
