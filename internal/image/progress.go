package image

import (
	"context"
	"io"
	"sync/atomic"
)

// Progress is how far a pull and build have got, for a caller that shows it:
// sandboxd's image install, read by a client while it runs.
type Progress struct {
	Phase string // "pulling", then "building"
	// Done and Total are bytes of the image's blobs: those already cached
	// count as done from the start. Total is 0 until the manifest is read.
	Done, Total int64
}

type progressKey struct{}

// progressState carries the callback and the counts it reports, shared by
// every blob of one pull.
type progressState struct {
	fn          func(Progress)
	done, total atomic.Int64
}

// WithProgress returns a context whose pull and build report to fn.
func WithProgress(ctx context.Context, fn func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, &progressState{fn: fn})
}

func progressOf(ctx context.Context) *progressState {
	ps, _ := ctx.Value(progressKey{}).(*progressState)
	return ps
}

func (ps *progressState) report(phase string) {
	if ps != nil {
		ps.fn(Progress{Phase: phase, Done: ps.done.Load(), Total: ps.total.Load()})
	}
}

// countingWriter adds what passes through to the pull's done count.
type countingWriter struct{ ps *progressState }

func (c countingWriter) Write(p []byte) (int, error) {
	c.ps.done.Add(int64(len(p)))
	c.ps.report("pulling")
	return len(p), nil
}

// progressWriter is w, counted when there is progress to report.
func progressWriter(ctx context.Context, w io.Writer) io.Writer {
	if ps := progressOf(ctx); ps != nil {
		return io.MultiWriter(w, countingWriter{ps})
	}
	return w
}
