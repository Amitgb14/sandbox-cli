package runtime

import (
	"bytes"
	"io"
	"strings"
	"sync"
)

// denyPrefix is the exact text internal/egressproxy/embed.go writes for a refused
// connection: "sandbox-cli: egress DENY host:port (reason)".
//
// Matched as a literal prefix rather than parsed. The line is not a protocol and
// nothing here depends on its shape beyond the host — a format change should cost
// a count, not a panic.
const denyPrefix = "sandbox-cli: egress DENY "

// maxDenyHosts bounds the distinct hosts kept for the run log. The count is
// exact; the list is a sample. One JSONL line is meant to stay a line, and a
// script looping over generated names would otherwise decide how big the user's
// audit file gets.
const maxDenyHosts = 32

// maxDenyLineBytes bounds how much of an unterminated line is buffered. Output
// arrives from the container, so a guest that writes megabytes without a newline
// is a guest choosing how much host memory this uses.
const maxDenyLineBytes = 8 << 10

// EgressDenials counts the egress denials reported on a run's stderr.
//
// **What this is, exactly:** the *container's own account* of what it was
// refused. The proxy runs inside the sandbox and writes these lines to the same
// stderr the agent writes to, so an agent can print a line that looks like one,
// and can bury real ones under noise. It is evidence, not attestation, and the
// audit field it feeds is named for that (`egress_denied_reported`) — the same
// rule EgressEnforcementRequested already follows: name it for what the host
// honestly knows.
//
// Making it authoritative means the proxy reporting over a channel the guest
// cannot write to, which is a different piece of work than counting lines.
//
// Safe for concurrent use: stdout and stderr are pumped by separate goroutines.
type EgressDenials struct {
	mu    sync.Mutex
	count int
	hosts []string
	seen  map[string]bool
}

// Observe records one already-trimmed output line, if it is a denial.
func (e *EgressDenials) Observe(line string) {
	if e == nil || !strings.HasPrefix(line, denyPrefix) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.count++
	host := denyHost(line[len(denyPrefix):])
	if host == "" || e.seen[host] {
		return
	}
	if e.seen == nil {
		e.seen = map[string]bool{}
	}
	e.seen[host] = true
	if len(e.hosts) < maxDenyHosts {
		e.hosts = append(e.hosts, host)
	}
}

// Count returns how many denial lines were seen.
func (e *EgressDenials) Count() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.count
}

// Hosts returns the distinct hosts seen, in first-seen order, capped at
// maxDenyHosts.
func (e *EgressDenials) Hosts() []string {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.hosts...)
}

// denyHost pulls the hostname out of "host:port (reason)".
//
// A connection with no name to check is logged as ":0" by the proxy, which is the
// interesting case rather than a parse failure — it is what a client dialling a
// bare address looks like. It has no host, so it contributes to the count and not
// to the list.
func denyHost(rest string) string {
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		rest = rest[:i]
	}
	i := strings.LastIndexByte(rest, ':')
	if i < 0 {
		return rest
	}
	return rest[:i]
}

// denyTap forwards everything written to it and counts denial lines on the way
// past.
//
// Output is written to dst *first*, before any scanning: this sits in front of
// the user's terminal on every run, and a counter is never a reason for an
// agent's output to arrive late.
type denyTap struct {
	dst io.Writer
	d   *EgressDenials
	buf []byte
	// over marks a line already past maxDenyLineBytes, whose remainder is
	// discarded rather than buffered until the next newline.
	over bool
}

// newDenyTap wraps dst, or returns dst unchanged when there is nothing to count
// into — so a run that is not recording denials pays nothing at all.
func newDenyTap(dst io.Writer, d *EgressDenials) io.Writer {
	if d == nil {
		return dst
	}
	return &denyTap{dst: dst, d: d}
}

func (t *denyTap) Write(p []byte) (int, error) {
	n, err := t.dst.Write(p)
	t.scan(p[:n])
	return n, err
}

func (t *denyTap) scan(p []byte) {
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			t.append(p)
			return
		}
		t.append(p[:i])
		if !t.over {
			t.d.Observe(string(bytes.TrimRight(t.buf, "\r")))
		}
		t.buf = t.buf[:0]
		t.over = false
		p = p[i+1:]
	}
}

func (t *denyTap) append(b []byte) {
	if t.over {
		return
	}
	if len(t.buf)+len(b) > maxDenyLineBytes {
		t.over = true
		t.buf = t.buf[:0]
		return
	}
	t.buf = append(t.buf, b...)
}
