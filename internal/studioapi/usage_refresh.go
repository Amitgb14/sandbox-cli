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
	if every <= 0 || !usageRefreshable() {
		return false
	}
	go s.usageRefreshLoop(ctx, every)
	return true
}

func (s *Server) usageRefreshLoop(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	var lastErr string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Someone may have advanced it since the last tick — the refresh button,
		// a sandbox run, or the operator's own Claude Code. `every` is the window
		// this loop is responsible for, so a reading younger than that is one it
		// does not need to buy again.
		if snap, err := usageSnapshot(); err == nil && !snap.FetchedAt.IsZero() &&
			snap.Age(time.Now()) < every {
			continue
		}

		attempt, cancel := context.WithTimeout(ctx, usageRefreshTimeout)
		err := usageRefresh(attempt)
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
