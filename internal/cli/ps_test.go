package cli

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

func TestDashRendersEmptyColumns(t *testing.T) {
	if dash("") != "-" || dash("   ") != "-" {
		t.Error("an empty label should render as a dash, not a gap")
	}
	if dash("claude") != "claude" {
		t.Error("a real value must pass through")
	}
}

// TestCleanOnlyReapsSessionsThatAreOver.
//
// "Finished" is deliberately narrower than "not running": a paused or restarting
// container is somebody's live run caught in an odd moment, and reaping it
// because it was not literally running at the instant we looked is how a
// supervision command destroys the thing it supervises.
func TestCleanOnlyReapsSessionsThatAreOver(t *testing.T) {
	over := map[string]bool{"exited": true, "dead": true}
	for _, state := range []string{"running", "restarting", "paused", "removing", "created", "exited", "dead", ""} {
		if got := sessionFinished(runtime.ContainerInfo{State: state}); got != over[state] {
			t.Errorf("sessionFinished(%q) = %v, want %v", state, got, over[state])
		}
	}
}
