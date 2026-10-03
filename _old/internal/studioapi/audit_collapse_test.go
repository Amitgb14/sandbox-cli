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

// Runs on the same branch are different runs, however they are named.
//
// A detached container's name is deterministic, so relying on it as the pairing
// key made every run on a branch one run — and the newest ending swallowed all
// the older records. The key is minted per launch for exactly this reason.
func TestTwoRunsOnOneBranchStayTwoRecords(t *testing.T) {
	got := collapseRuns([]AuditRecord{
		{Time: "2026-08-17T11:00:00Z", RunID: "run-b", Finished: true, ExitCode: 0},
		{Time: "2026-08-17T10:00:00Z", RunID: "run-a", Finished: true, ExitCode: 1},
	})
	if len(got) != 2 {
		t.Fatalf("collapsed two separate runs into %d record(s)", len(got))
	}
}

// A page of N is N runs, not N lines.
//
// The bound used to be applied to raw lines and the pairs folded afterwards, so
// a client asking for 200 records to draw a chart got 100 on a machine that runs
// Studio — silently, since a short page and a quiet machine look the same.
func TestTheLimitCountsRunsRatherThanLines(t *testing.T) {
	var lines []AuditRecord
	for i := range 10 {
		id := "run-" + string(rune('a'+i))
		lines = append(lines,
			AuditRecord{Time: "t", RunID: id, Finished: true, ExitCode: 0},
			AuditRecord{Time: "t", RunID: id})
	}
	if got := len(capRecords(collapseRuns(lines), 10)); got != 10 {
		t.Errorf("asked for 10 runs and got %d", got)
	}
	if got := len(capRecords(collapseRuns(lines), 3)); got != 3 {
		t.Errorf("asked for 3 runs and got %d", got)
	}
}
