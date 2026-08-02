package runtime

import (
	"bytes"
	"io"
	"strings"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/egressproxy"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// denyPrefix is what the proxy writes for a refused connection:
// "sandbox-cli: egress DENY host:port (reason)".
//
// Taken from the package that owns the format rather than spelled out again here.
// It used to be a literal, which made this the third hand-written copy of a line
// assembled in two other places — and a change to either of those would have sent
// this counter silently to zero with every test still passing. egressproxy binds
// its constants to the source that actually ships (see format.go), so a break
// there is now a failing test rather than an audit field that quietly reads 0.
//
// Matched as a prefix rather than parsed. The line is not a protocol and nothing
// here depends on its shape beyond the host — a format change should cost a
// count, not a panic.
const denyPrefix = egressproxy.DenyLinePrefix

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
	// Stop before touching `seen` once the list is full. It exists only to keep
	// duplicates out of `hosts`, so past the cap there is nothing left to dedupe
	// against — and a map that kept growing would be the one unbounded thing here,
	// which is exactly what the caps above promise it is not. The count still
	// rises; only the sample is bounded.
	//
	// The order has a consequence worth stating: the sample is first-seen, not
	// representative, and the guest chooses what arrives first. A container that
	// forges maxDenyHosts invented names before doing anything real fills the list
	// and every genuine refusal afterwards is counted but never named. There is no
	// bounded sample without this property — any cap can be filled by whoever
	// speaks first — so it is documented on the audit field rather than defended
	// against here. The count remains exact, which is the number to trust.
	if len(e.hosts) >= maxDenyHosts {
		return
	}
	host := denyHost(line[len(denyPrefix):])
	if host == "" || e.seen[host] {
		return
	}
	if e.seen == nil {
		e.seen = map[string]bool{}
	}
	e.seen[host] = true
	e.hosts = append(e.hosts, host)
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

// maxHostBytes is the longest a recorded host may be. 253 is the longest a DNS
// name can legitimately be, so nothing real is lost — and without it a "host" is
// whatever the guest put before the last colon of a line up to maxDenyLineBytes
// long, which is to say 8KB of its choosing, thirty-two times over. A ~256KB
// record would rotate real history out of an audit log whose size ceiling was
// written for lines "a few hundred bytes" long, and a guest that can evict the
// audit trail is a worse outcome than one denial going uncounted.
const maxHostBytes = 253

// denyHost pulls the hostname out of "host:port (reason)".
//
// A connection with no name to check is logged as ":0" by the proxy, which is the
// interesting case rather than a parse failure — it is what a client dialling a
// bare address looks like. It has no host, so it contributes to the count and not
// to the list.
//
// The result is truncated and then run through termsafe.Clean, because this
// string is guest-controlled text that ends up in a JSONL record and, from there,
// in whatever reads it. That is the same reasoning the session table already
// applies to branch names, one step further down the trust ladder: a branch name
// is at least written by someone with commit access, and this is written by the
// sandboxed process.
//
// Truncating by bytes can split a multi-byte rune. That is deliberate rather
// than overlooked: termsafe.Clean runs strings.Map over the result, which turns
// the orphaned fragment into U+FFFD, so the recorded name stays valid UTF-8 and
// the JSON record stays well-formed. Counting runes to avoid a cosmetic
// replacement character in a truncated hostile hostname is not worth the code.
func denyHost(rest string) string {
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.LastIndexByte(rest, ':'); i >= 0 {
		rest = rest[:i]
	}
	if len(rest) > maxHostBytes {
		rest = rest[:maxHostBytes]
	}
	return termsafe.Clean(rest)
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
// into.
//
// **Wrapping is not free, and it is worth naming what it costs.** `cmd.Stderr =
// os.Stderr` hands docker the inherited descriptor; anything that is not an
// *os.File makes os/exec create a pipe and a copying goroutine instead. Three
// consequences on a wrapped run: docker's own diagnostics no longer see a
// terminal on fd 2; stderr is ordered against a stdout that still goes direct;
// and docker's progress output for an image pull loses its terminal formatting
// and arrives as plain lines.
//
// Both consequences are bounded by *when* this is wrapped at all: only for a run
// with an allowlist and no pty (sandbox.canObserveDenials). An interactive
// session — the case where a terminal on fd 2 would have meant something, and
// where interleaving is most visible — is never wrapped. What is left is a
// non-interactive run whose stderr was not a terminal to begin with.
//
// **Only stderr is ever wrapped, and only for a run with no pty.** That is a
// limit rather than an oversight, and it was measured:
//
// Which host stream the proxy's lines arrive on depends on `-t`. Without a pty
// docker demultiplexes and stderr is stderr. *With* one there is a single
// hijacked stream carrying both, which the client copies to its own **stdout** —
// so wrapping stderr sees nothing at all on an interactive run.
//
// The obvious repair is to wrap stdout too. It works, and it costs more than it
// buys: an `io.Writer` that is not an *os.File makes os/exec hand docker a pipe,
// docker then cannot see a terminal on its own stdout, and the container's
// terminal size collapses. Measured through a real pty, guest `stty size`:
//
//	stderr tapped only          44 173   <- the client's real size
//	stdout tapped as well        0   0   <- every agent TUI renders blind
//
// Breaking the size for every interactive agent to record a count is the wrong
// trade, and it is the same shape as the tmux experiment CLAUDE.md warns against
// repeating. So an interactive run is left **unobserved** and says so, rather
// than being observed at the cost of the thing the user is looking at. Making it
// observable needs a channel that is not the guest's stdio — which is the first
// required feature of roadmap task 4, and is where this should be fixed.
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
