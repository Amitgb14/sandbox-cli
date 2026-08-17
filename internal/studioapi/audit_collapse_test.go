package studioapi

import "testing"

// A detached run is two lines and one run.
//
// It cannot know its own exit code at launch, so the log gets a line when the
// container starts and another when it stops. Counting both would double every
// Studio run in every total on every screen; showing both would put a perpetual
// "still running" next to its own conclusion.
func TestARunsTwoLinesCollapseIntoOne(t *testing.T) {
	// Newest first, as the listing arrives: the ending, then the launch.
	got := collapseRuns([]AuditRecord{
		{Time: "2026-08-17T10:05:00Z", RunID: "sandbox-x", Finished: true, ExitCode: 1},
		{Time: "2026-08-17T10:00:00Z", RunID: "sandbox-x", ExitCode: 0},
	})
	if len(got) != 1 {
		t.Fatalf("collapsed to %d records, want 1", len(got))
	}
	if !got[0].Finished || got[0].ExitCode != 1 {
		t.Errorf("kept the placeholder over the result: %+v", got[0])
	}

	// And the other order, since the log is append-only and a rebuild reads it
	// oldest-first.
	got = collapseRuns([]AuditRecord{
		{Time: "2026-08-17T10:00:00Z", RunID: "sandbox-x", ExitCode: 0},
		{Time: "2026-08-17T10:05:00Z", RunID: "sandbox-x", Finished: true, ExitCode: 1},
	})
	if len(got) != 1 || !got[0].Finished || got[0].ExitCode != 1 {
		t.Errorf("order changed the answer: %+v", got)
	}
}

// A launch with no partner is a run still going, or one whose ending nobody saw.
// Both are more honestly left unfinished than guessed at in either direction.
func TestAnUnpartneredLaunchStaysUnfinished(t *testing.T) {
	got := collapseRuns([]AuditRecord{{Time: "t", RunID: "sandbox-y", ExitCode: 0}})
	if len(got) != 1 {
		t.Fatalf("dropped a run with no ending: %v", got)
	}
	if got[0].Finished {
		t.Error("an unpartnered launch claims to have finished")
	}
}

// A foreground run has no RunID at all — one line, written by the process that
// waited for it — and must not be grouped with anything.
func TestForegroundRunsAreNeverCollapsed(t *testing.T) {
	got := collapseRuns([]AuditRecord{
		{Time: "a", Finished: true, ExitCode: 0},
		{Time: "b", Finished: true, ExitCode: 1},
	})
	if len(got) != 2 {
		t.Errorf("collapsed %d foreground runs into %d", 2, len(got))
	}
}
