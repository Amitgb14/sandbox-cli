package rescue

import (
	"testing"
	"time"
)

// A baseline is the workspace as a run *started*, and every listing that calls
// it anything else invites somebody to restore it and conclude their work was
// lost.
//
// It read as `clean` in `sandbox-cli recover list` — the same word a finished
// run's snapshot gets — because Status() knew nothing about the outcome and the
// closed-session case swallowed it. That is how a correct restore came to look
// like data loss: a Studio run records only this, so killing an agent before it
// commits leaves exactly one session, and it holds the state from before the
// agent ran.
func TestABaselineIsNotCalledClean(t *testing.T) {
	now := time.Now()
	base := Session{Outcome: OutcomeBaseline, EndedAt: &now}
	if got := base.Status(); got != "baseline" {
		t.Errorf("Status() = %q, want \"baseline\"", got)
	}
	if !base.IsBaseline() {
		t.Error("IsBaseline() is false for a baseline session")
	}

	// The case that used to win: a baseline is *also* a closed session, so every
	// later branch would have called it clean.
	ordinary := Session{EndedAt: &now}
	if got := ordinary.Status(); got != "clean" {
		t.Errorf("an ordinary finished run: Status() = %q, want \"clean\"", got)
	}
	if ordinary.IsBaseline() {
		t.Error("IsBaseline() is true for an ordinary run")
	}

	// And a manual checkpoint keeps its own word, which is what a person asked
	// for rather than what a launch recorded.
	manual := Session{Outcome: OutcomeManual, EndedAt: &now}
	if got := manual.Status(); got != "snapshot" {
		t.Errorf("a manual capture: Status() = %q, want \"snapshot\"", got)
	}
	if manual.IsBaseline() {
		t.Error("IsBaseline() is true for a manual capture")
	}
}
