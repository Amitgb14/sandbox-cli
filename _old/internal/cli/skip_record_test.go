package cli

import (
	"strings"
	"testing"
)

// A skip is a switch, and has to be written down as one.
//
// routedFrom/routeReason travel to the container labels and the audit line;
// stderr is not where anybody looks a week later. The first version assigned
// them only in the post-run failover branch, so the *preflight* case — the one
// the whole feature exists for — recorded a plain run of whichever agent
// answered, and Studio's Routing screen counted the episode as never having
// switched at all.
func TestASkippedProviderIsRecordedNotJustPrinted(t *testing.T) {
	var from, reason string
	var skipped []string

	skipped = append(skipped, "claude (provider answered 503)")
	from, reason = noteSkip(from, "claude", skipped)
	if from != "claude" {
		t.Errorf("routedFrom = %q, want the agent that was asked for", from)
	}

	// A second skip does not rewrite who was asked for — that is still claude —
	// but it does join the reasons, because the agent that finally runs is
	// explained by the sequence rather than by the last link of it.
	skipped = append(skipped, "codex (timed out)")
	from, reason = noteSkip(from, "codex", skipped)
	if from != "claude" {
		t.Errorf("routedFrom = %q after a second skip, want it to stay claude", from)
	}
	for _, want := range []string{"claude", "503", "codex", "timed out"} {
		if !strings.Contains(reason, want) {
			t.Errorf("routeReason = %q, want it to mention %q", reason, want)
		}
	}
}
