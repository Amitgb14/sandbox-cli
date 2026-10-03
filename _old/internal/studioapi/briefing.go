package studioapi

import (
	"os"

	"github.com/Amitgb14/sandbox-cli/internal/handoff"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// A briefing is what crosses when one agent takes over work another was doing.
//
// Two callers ask for one and they differ in exactly one thing — which
// conversation. The supervisor *correlates*: a container failed, and the
// transcript belonging to it is found by store, window and prompt, so a wrong
// guess would brief the next agent with a stranger's conversation. A request
// *selects*: somebody read a conversation and named it. Selection may therefore
// reach where correlation must not — the user's own ~/.claude is a legitimate
// answer to "carry this one on" and never a legitimate answer to "what was this
// container doing".
//
// Everything after that fork is identical, and it lives here so it stays that
// way: write the export, mount it read-only, and prepend the sentence that says
// it *is* a briefing. That last part is the load-bearing one — handoff.Prompt
// tells the target a previous agent stopped before finishing, because an agent
// told it is resuming answers as though the history were its own.

// writeBriefing exports one conversation for another agent to read.
//
// Best-effort by construction, and nil is a normal answer: the transcript may be
// unreadable, absent, or in a format sandbox-cli has no verified reader for. A
// run that starts without a briefing is a run that starts with only its prompt,
// which is what would have happened anyway — so a failure here must never fail
// the launch.
func writeBriefing(from, sessionPath, workspace, base string) *handoff.Export {
	dir, err := os.MkdirTemp("", "sandbox-handoff-*")
	if err != nil {
		return nil
	}
	ex, err := handoff.Write(dir, from, sessionPath, workspace, base)
	if err != nil {
		os.RemoveAll(dir)
		return nil
	}
	return ex
}

// briefingMount is where an export is bind-mounted, read-only.
//
// Read-only is not a default here: the briefing is evidence about a run that has
// finished, and an agent that could rewrite it could rewrite its own history.
func briefingMount(ex *handoff.Export) string {
	return ex.Dir + ":" + handoff.GuestDir + ":ro"
}

// applyBriefing attaches an export to the options a run is about to be built
// from — the mount, and the record of whose conversation it was.
//
// The prompt is *not* rewritten here, because the two callers rewrite it at
// different moments: the supervisor before it builds options (the argv is
// derived from the prompt), the handler while it still holds the request. Doing
// it in one place would mean one of them passing a prompt back out of a function
// that had already used it.
func applyBriefing(opts *sandbox.Options, ex *handoff.Export, fromAgent, sessionID string) {
	if ex == nil {
		return
	}
	opts.ExtraMounts = append(opts.ExtraMounts, briefingMount(ex))
	opts.HandoffFrom = fromAgent
	opts.HandoffSession = sessionID
}
