// Package audit is the server's event log: what each sandbox was asked to do,
// and what happened to it.
//
// beta.15 wrote one line per run on the client, after the run, because the
// container was the client's. The rewrite moves it to sandboxd, where every
// client's requests arrive — the CLI, Studio, an SDK, someone with curl — so
// the record no longer depends on which of them started the sandbox, and an
// operator of a self-hosted machine has it for every user. Clients say why a
// sandbox exists through labels, which the log carries; routing's attempt ids
// and Studio's mark arrive that way, without the server knowing what
// an agent is.
//
// What it deliberately does not record: any environment *value*. Names, yes —
// which credentials a run was handed is exactly what you want to look up later —
// but never what they were worth. The credential broker exists to keep secret
// values off the argv and out of config files; writing them to a log would hand
// that back. File contents are never recorded either: a file event carries the
// path and the size.
//
// The guest command *is* recorded verbatim, and that is the known soft edge in
// the rule above: an argv is the other classic place a token ends up
// (`-- curl -H "Authorization: Bearer …"`). It is kept because a log that
// cannot say what ran answers nothing, and because redaction here would have to
// guess — a heuristic that scans argv for secrets misses the ones it does not
// recognise while implying it caught them all, which is worse than a documented
// limitation. Treat the log as sensitive; it is written 0600 for this reason.
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Log appends events to a JSONL file, one per line.
//
// Best-effort: an unwritable log must never fail a request, because the
// request is what the client asked for and the record is a courtesy. That is
// a choice a server operator may want reversed for compliance, and it is
// recorded in docs/self-hosting.md as such.
type Log struct {
	Path string
	mu   sync.Mutex
}

// NewLog returns a log at path, or nil — which records nothing — when path is
// empty.
func NewLog(path string) *Log {
	if path == "" {
		return nil
	}
	return &Log{Path: path}
}

// maxLogBytes is the size at which the log is rotated. An event is a few
// hundred bytes, so this holds a long history while bounding a file nothing
// else ever prunes.
const maxLogBytes = 8 << 20

// Record appends one event. A nil Log records nothing.
func (l *Log) Record(e api.Event) {
	if l == nil || l.Path == "" {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return
	}
	l.rotateIfLarge()
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// Read returns the events for one sandbox, oldest first, across every
// generation — at most max of the newest, with truncated set when older ones
// were left out. A line that does not parse is skipped: the log is appended to
// while it is read, and its last line may be half-written.
func (l *Log) Read(sandbox string, max int) (events []api.Event, truncated bool, err error) {
	if l == nil {
		return nil, false, nil
	}
	gens := Generations(l.Path)
	// Oldest generation first, so the result is in order.
	for i := len(gens) - 1; i >= 0; i-- {
		f, err := os.Open(gens[i])
		if err != nil {
			continue
		}
		// ReadBytes rather than a Scanner: one oversized line (a long argv)
		// would stop a Scanner and silently drop the rest of the file.
		br := bufio.NewReader(f)
		for {
			line, rerr := br.ReadBytes('\n')
			var e api.Event
			if len(line) > 0 && json.Unmarshal(line, &e) == nil && e.Sandbox == sandbox {
				events = append(events, e)
			}
			if rerr != nil {
				break
			}
		}
		f.Close()
	}
	if max > 0 && len(events) > max {
		events, truncated = events[len(events)-max:], true
	}
	return events, truncated, nil
}

// MaxGenerations is how many rotated logs are kept beside the current one.
//
// It used to be one, and that quietly deleted history. At the density this
// writes — a few hundred bytes per run — 8 MiB is roughly twelve thousand runs,
// so a single previous generation meant the twelve-thousandth-oldest run
// vanished with nothing recording that it had ever existed. A team running
// fifty sandboxes a day reached that in about eighteen months; one running five
// hundred, in six weeks.
//
// Five generations is ~40 MiB and ~60,000 runs. That is a bounded cost in a
// directory nothing else prunes, and the ceiling is the point: an append-only
// log with no ceiling is a slow leak in someone's home directory, and a log
// that drops the oldest without saying so is worse than one that is capped.
const MaxGenerations = 5

// rotateIfLarge moves the log aside once it passes maxLogBytes, shifting the
// previous generations along and dropping the oldest.
//
// Oldest first, so no rename can overwrite a generation that has not been moved
// yet: .4 becomes .5 before .3 becomes .4. Doing it the other way round loses
// every generation but one, which is the bug this is fixing.
//
// Best-effort like everything else here: if a rename fails the run still
// proceeds and the log simply keeps growing, which is the lesser of the two
// failures.
func (l *Log) rotateIfLarge() {
	fi, err := os.Stat(l.Path)
	if err != nil || fi.Size() < maxLogBytes {
		return
	}
	// The last generation is removed rather than shifted: something has to be
	// the end, and this is the one place it is decided.
	_ = os.Remove(generationPath(l.Path, MaxGenerations))
	for i := MaxGenerations - 1; i >= 1; i-- {
		_ = os.Rename(generationPath(l.Path, i), generationPath(l.Path, i+1))
	}
	_ = os.Rename(l.Path, generationPath(l.Path, 1))
}

// generationPath names one rotated log. Generation 0 is the live file.
func generationPath(base string, n int) string {
	if n <= 0 {
		return base
	}
	return base + "." + strconv.Itoa(n)
}

// Generations lists the log files that exist, newest first, starting with the
// live one.
//
// Exported because a reader has to know that the history is several files: a
// caller that opens only sessions.jsonl sees the most recent generation and
// reports the rest as though it never happened, which is exactly the failure
// this pairs with.
func Generations(path string) []string {
	var out []string
	for n := 0; n <= MaxGenerations; n++ {
		p := generationPath(path, n)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
