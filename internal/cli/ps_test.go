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
// supervision command destroys the thing it supervises. An unreadable state is
// live for the same reason.
//
// `created` is reapable, and the difference is one of kind: a container that
// never started ran no agent and has no in-flight write to protect. It is the
// husk a failed detached start leaves behind, and without this the only way to
// remove one was --force, which warns about killing an agent that may still be
// working — a far scarier thing than what the user is actually doing.
func TestCleanOnlyReapsSessionsThatAreOver(t *testing.T) {
	over := map[string]bool{"exited": true, "dead": true, "created": true}
	for _, state := range []string{"running", "restarting", "paused", "removing", "created", "exited", "dead", ""} {
		if got := sessionFinished(runtime.ContainerInfo{State: state}); got != over[state] {
			t.Errorf("sessionFinished(%q) = %v, want %v", state, got, over[state])
		}
	}
}
