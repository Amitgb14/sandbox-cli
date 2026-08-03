package studioapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentusage"
)

// windowKinds translates agentusage's internal kind (`5h`, `7d`) into the wire
// enum clients switch on, with a label to print alongside it.
//
// Translated here rather than pushed onto every client: the internal name is
// this repository's shorthand and may change with the cache format it reads,
// while the wire enum is a published contract. A kind with no entry passes
// through as itself and labels itself — a new window the parser starts
// reporting should appear as an unknown thing, not silently borrow the name of
// a known one.
var windowKinds = map[string]struct{ kind, label string }{
	"5h": {"five_hour", "5-hour"},
	"7d": {"seven_day", "Weekly"},
}

// handleUsage reports how much of the subscription window is spent.
//
// Two rules travel with this data, both from internal/agentusage, and neither is
// negotiable. **Read only**: the file belongs to Claude Code and nothing here
// opens it for writing. **Always aged**: every snapshot carries the fetchedAt it
// came with, because these numbers refresh only when the agent talks to the
// server — an unlabelled percentage can be hours stale.
//
// A shape the parser no longer recognises yields *no windows* rather than a
// zero. That is why this can honestly answer 200 with an empty list, and why a
// client must not read that as "nothing used".
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	out := UsageSnapshot{Agent: "claude", Windows: []UsageWindow{}, CanRefresh: usageRefreshable()}

	snap, err := agentusage.Find(agentusage.ClaudePaths()...)
	if err != nil {
		// Not an error for the caller: no cache is the ordinary state before the
		// agent has run on this machine, and a 500 would make a Studio page look
		// broken for a setup that is merely new.
		writeJSON(w, http.StatusOK, out)
		return
	}

	now := time.Now()
	for _, win := range snap.Windows {
		u := UsageWindow{Kind: win.Kind, Label: win.Kind, Scope: win.Scope, Active: win.Active}
		if k, ok := windowKinds[win.Kind]; ok {
			u.Kind, u.Label = k.kind, k.label
		}

		pct := win.Percent
		u.Utilization = &pct
		if !win.ResetsAt.IsZero() {
			at := win.ResetsAt.UTC().Format(time.RFC3339)
			u.ResetsAt = &at
			if now.After(win.ResetsAt) {
				// Past its reset: the cached figure describes the window that has
				// already ended. Reporting it against the current one would be a
				// number that is wrong rather than one that is missing.
				u.Utilization = nil
			}
		}
		out.Windows = append(out.Windows, u)
	}

	if snap.Agent != "" {
		out.Agent = snap.Agent
	}
	if !snap.FetchedAt.IsZero() {
		at := snap.FetchedAt.UTC().Format(time.RFC3339)
		out.FetchedAt = &at
	}
	if snap.Path != "" {
		p := snap.Path
		out.Path = &p
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUsageRefresh is POST /v1/usage/refresh.
//
// The numbers this API serves are a cache Claude Code keeps for its own /usage,
// and it refreshes only when the agent talks to the server — so a reading can be
// hours stale with nothing here able to tell. Refresh is the one way to make it
// current: it runs a single throwaway turn, in a scratch directory so the turn
// stays out of the project's transcript history, and re-reads.
//
// Deliberately a POST and deliberately not automatic. The request is spent from
// the very window being measured, so asking for it has to be an act rather than
// a side effect of opening a screen.
// usageRefreshable answers whether a refresh is possible on this machine. A
// variable for the same reason agentusage.claudeBin is one: the answer depends
// on what is installed where the test happens to run, and a status code this
// load-bearing should be pinned rather than exercised only on machines that
// happen to lack the agent.
var usageRefreshable = agentusage.Refreshable

func (s *Server) handleUsageRefresh(w http.ResponseWriter, r *http.Request) {
	// Asked before attempting, so the impossible case gets an answer about *this
	// deployment* rather than a failed subprocess. It is the ordinary case under
	// `docker compose --profile api`: the daemon runs in a container that has no
	// claude binary, while the cache it serves is perfectly readable through the
	// mounted agent HOME. Numbers you can read and cannot refresh is a
	// configuration, not a fault.
	if !usageRefreshable() {
		writeError(w, http.StatusNotImplemented, fmt.Errorf(
			"this server cannot refresh usage: the agent that records these numbers is not on its "+
				"PATH. Reading them needs only the cache file, which is why they are shown; "+
				"refreshing them needs the agent itself. Run the API on a machine that has it — "+
				"under `docker compose --profile api` the API is a container, and the recommended "+
				"shape is the API on your host"))
		return
	}
	if err := agentusage.Refresh(r.Context()); err != nil {
		// The stale reading is still the honest one, and returning it with a 200
		// would say the refresh worked. The client keeps what it had and learns
		// why it could not be improved.
		//
		// 502 is right *here* and wrong above: this is the agent having been run
		// and failed, which is an upstream fault. "The agent is not installed" is
		// not — it is this server never having been able to, and answering both
		// with one status made a permanent condition look like a bad minute.
		writeError(w, http.StatusBadGateway, fmt.Errorf("refreshing usage: %w", err))
		return
	}
	s.handleUsage(w, r)
}
