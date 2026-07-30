package studioapi

import (
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
	out := UsageSnapshot{Agent: "claude", Windows: []UsageWindow{}}

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
		u := UsageWindow{Kind: win.Kind, Label: win.Kind, Scope: win.Scope}
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
