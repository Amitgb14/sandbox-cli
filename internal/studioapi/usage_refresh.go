package studioapi

import (
	"context"
	"log"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentusage"
)

// Periodic usage refresh.
//
// The reading Studio shows only moves when Claude Code talks to the server, so
// on a machine where nobody has used the agent for a fortnight the panel is
// honest and useless: "18 days old" is the true answer to a question nobody
// wanted to ask. `POST /v1/usage/refresh` fixes one reading on demand; this
// keeps it fixed.
//
// What it costs is the reason this is a flag and not a default of the package:
// **each refresh spends one request from the very subscription window being
// measured.** At the ten-minute default that is ~144 a day, taken from the
// budget the number describes. Whoever runs the daemon is the one who can weigh
// that, so it is stated at startup rather than buried here.
//
// Three restraints keep it from being worse than the staleness it fixes:
//
//   - It never starts where the agent is not on PATH. That is the ordinary
//     containerised deployment, where the cache is readable through the mounted
//     agent HOME and the binary is absent — a timer there would only fail every
//     ten minutes, on a schedule, forever.
//   - It skips a tick whose reading is already fresh. An interactive refresh, a
//     sandbox run, or the user's own Claude Code all advance the same file, and
//     spending a request to re-learn what was just learned is pure waste.
//   - It complains once. A broken setup that logged every tick would produce a
//     hundred and forty-four identical lines a day, which is how a log stops
//     being read.

// usageSnapshot and usageRefresh are variables for the same reason
// usageRefreshable is: the loop's decisions depend on a file and a binary that
// belong to another program, and the decisions are what deserve a test.
//
// `handleUsageRefresh` goes through the same `usageRefresh`, so the endpoint and
// the timer cannot drift into refreshing by different means — and a test that
// stubs it is not quietly leaving one of the two callers driving the real agent.
var (
	usageSnapshot = func() (agentusage.Snapshot, error) {
		return agentusage.Find(agentusage.ClaudePaths()...)
	}
	usageRefresh = agentusage.Refresh
)

// usageRefreshTimeout bounds one attempt. `claude -p` normally answers in
// seconds; a hung one must not still be running when the next tick arrives.
const usageRefreshTimeout = 2 * time.Minute

// StartUsageRefresh runs a refresh every `every` until ctx is done, and reports
// whether it started. A non-positive interval is off.
//
// Deliberately not an error: "this deployment cannot refresh" is a
// configuration, the same one handleUsageRefresh answers 501 for, and a daemon
// that refused to boot over it would be refusing to serve numbers it can read
// perfectly well.
func (s *Server) StartUsageRefresh(ctx context.Context, every time.Duration) bool {
	return s.startUsageRefresh(ctx, every) != nil
}

// startUsageRefresh is the same thing with a handle on the goroutine: the
// returned channel closes when the loop has stopped, and is nil when it never
// started.
//
// The handle exists for the tests, and the capture below exists because of what
// the tests found. Reading the package-level seams on every tick is a data race
// against anything that restores them — `go test -race` says so — and it is also
// wrong on its own terms: a loop should not be able to change which functions it
// is calling half way through. Both are fixed by taking them once, here, before
// the goroutine starts.
func (s *Server) startUsageRefresh(ctx context.Context, every time.Duration) <-chan struct{} {
	if every <= 0 || !usageRefreshable() {
		return nil
	}
	snapshot, refresh := usageSnapshot, usageRefresh
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.usageRefreshLoop(ctx, every, snapshot, refresh)
	}()
	return done
}

func (s *Server) usageRefreshLoop(
	ctx context.Context,
	every time.Duration,
	snapshot func() (agentusage.Snapshot, error),
	refresh func(context.Context) error,
) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	// The skip threshold is deliberately *below* the interval, and the gap is the
	// whole reason it works.
	//
	// A refresh is not instant: the tick fires at T, `claude -p` answers a few
	// seconds later, and Claude Code stamps the cache at roughly T+3s. Comparing
	// the next tick against `every` then measures the loop's own last refresh as
	// 9m57s old, calls it fresh, and skips — so it fires every *other* tick and
	// the reading reaches twenty minutes at a setting that says ten. Ninety
	// percent is wide enough to swallow any plausible refresh, and far narrower
	// than the case the check exists for: a reading somebody else advanced since
	// the last tick.
	freshEnough := every - every/10

	// Checked before the first tick, because the state this feature exists for is
	// a reading that is already old — a daemon started next to a nineteen-day-old
	// figure should not serve it for another ten minutes first. It costs nothing
	// when the reading is current: that is what the skip below decides.
	first := true

	var lastErr string
	for {
		if !first {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		first = false
		if ctx.Err() != nil {
			return // cancelled before the first check ever ran
		}

		// Someone may have advanced it since the last tick — the refresh button,
		// a sandbox run, or the operator's own Claude Code. A reading that recent
		// is one this loop does not need to buy again.
		if snap, err := snapshot(); err == nil && !snap.FetchedAt.IsZero() &&
			snap.Age(time.Now()) < freshEnough {
			continue
		}

		attempt, cancel := context.WithTimeout(ctx, usageRefreshTimeout)
		err := refresh(attempt)
		cancel()

		if err == nil {
			lastErr = ""
			continue
		}
		if ctx.Err() != nil {
			return // shutting down; the failure is the shutdown
		}
		if msg := err.Error(); msg != lastErr {
			lastErr = msg
			log.Printf("sandbox-studio-api: periodic usage refresh failed (saying so once until it "+
				"changes): %v", err)
		}
	}
}
