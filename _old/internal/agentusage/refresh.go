package agentusage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// claudeBin names the agent that owns the cache this package reads. A variable
// so tests can point it at a stand-in.
var claudeBin = "claude"

// refreshPrompt is the throwaway turn. The reply is discarded — the refreshed
// cache is the entire point — so it is phrased to cost as little as anything can.
const refreshPrompt = "ok"

// Refresh gives Claude Code a reason to talk to the server, so the cache it
// keeps for its own /usage is current by the time Read next opens it.
//
// This does not break the package's read-only rule. Nothing here opens
// ~/.claude.json for writing; Claude Code writes it, as it does after any
// request, and we only supply the request. Nor is it the live query the design
// rules out (docs/proposals/usage-stats.md) — that meant replaying the agent's
// stored credentials against an endpoint nobody documented. This drives the
// agent's own supported CLI and then reads exactly the file it read before.
//
// Two things make it best-effort, and both are why nothing calls it unasked:
//
//   - It costs one request against the very subscription being measured.
//   - Claude Code decides when to refetch. It holds a reading it considers
//     current rather than asking again for every request, so a refresh can
//     legitimately leave the stamp where it was. What this buys is the
//     difference between minutes and the hours an idle machine accumulates,
//     not a reading stamped now. Callers print the age either way, so a refresh
//     that changed nothing stays visible rather than implied.
//
// Refreshable reports whether a refresh could even be attempted here — that is,
// whether the agent that owns this cache is on PATH.
//
// Asked *before* the offer rather than discovered by making it. These numbers
// are readable on a machine that has never had Claude Code installed, because
// the cache travels in the sandbox-owned agent HOME, and the daemon itself may
// be running in a container that has no claude binary at all. In both cases the
// figures are real and the refresh is impossible, so a UI that offers one
// anyway is promising something it cannot do.
func Refreshable() bool {
	_, err := exec.LookPath(claudeBin)
	return err == nil
}

func Refresh(ctx context.Context) error {
	if _, err := exec.LookPath(claudeBin); err != nil {
		return fmt.Errorf("refresh usage: %s is not on PATH — these numbers can only be "+
			"refreshed by the agent that records them", claudeBin)
	}
	cmd := exec.CommandContext(ctx, claudeBin, "-p", refreshPrompt)
	cmd.Dir = refreshDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("refresh usage: %s gave no answer in time (%v)", claudeBin, ctx.Err())
		}
		return fmt.Errorf("refresh usage: %s -p: %w%s", claudeBin, err, detail(out))
	}
	return nil
}

// refreshDir keeps the throwaway turn out of the project's own history. Claude
// Code files transcripts by working directory, so running this where the user
// works would put an "ok" session among the conversations `context list` offers
// to resume. A fixed path rather than a fresh mkdtemp each time, so this can
// only ever account for one such directory. An empty return means run where the
// caller stands — a refresh is worth more than the tidiness.
func refreshDir() string {
	d := filepath.Join(os.TempDir(), "sandbox-cli-usage")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return ""
	}
	return d
}

// detail appends the agent's own complaint to ours, trimmed to a line. Whatever
// went wrong — signed out, no network, a plan with no window — the agent said it
// better than a wrapped exit status can.
func detail(out []byte) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 200
	if len(s) > max {
		s = s[:max] + "…"
	}
	return ": " + s
}

// StaleAfter is how old a reading has to be before asking for a new one is worth
// a request. Claude Code will not refetch inside its own interval anyway, so a
// refresh below this buys nothing and still spends.
const StaleAfter = time.Minute

// NeedsRefresh reports whether a snapshot is old enough to be worth refreshing.
// A snapshot with no stamp at all counts: an age we cannot vouch for is not the
// same as a recent one.
func (s Snapshot) NeedsRefresh(now time.Time) bool {
	if s.FetchedAt.IsZero() {
		return true
	}
	return s.Age(now) >= StaleAfter
}

// RolledOver reports whether this window has passed its reset since the reading
// was taken. The percentage beside it is then the *previous* window's final
// figure — true when it was written, and about the wrong period by the time it
// is read. Windows that report no reset time cannot be placed either side of one
// and so are never called rolled over.
func (w Window) RolledOver(now time.Time) bool {
	return !w.ResetsAt.IsZero() && !w.ResetsAt.After(now)
}
