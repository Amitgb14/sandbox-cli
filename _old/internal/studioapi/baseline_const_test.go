package studioapi

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// The daemon and internal/cli must mean the same thing by "baseline".
//
// This literal is written by the daemon and read by both front ends, and the
// comment beside it in runs.go says it "has to match in three places and is one
// typo away from offering a run's starting state as its work". It matched in
// two: internal/cli did not know the value existed, so `recover list` called a
// baseline `clean` and `recover restore` handed one back without a word. Pinning
// it to the shared constant is what makes the third place impossible to forget.
func TestBaselineOutcomeIsTheSharedConstant(t *testing.T) {
	if baselineOutcome != rescue.OutcomeBaseline {
		t.Errorf("daemon writes %q, rescue reads %q — the two front ends will disagree",
			baselineOutcome, rescue.OutcomeBaseline)
	}
	// And that the value is what is already on disk in every manifest a daemon
	// run has written, so the fix does not orphan existing sessions.
	if rescue.OutcomeBaseline != "baseline" {
		t.Errorf("OutcomeBaseline = %q; manifests already recorded \"baseline\"", rescue.OutcomeBaseline)
	}
}
