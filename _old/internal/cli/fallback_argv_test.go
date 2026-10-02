package cli

import (
	"slices"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// A failover must not change who is in charge of the run.
//
// `sandbox-cli claude "refactor auth"` is an interactive session seeded with a
// task: somebody is at the terminal, and the agent asks before it acts. The
// first version re-expressed every fallback through Autonomous, which appends
// --dangerously-skip-permissions — so a provider outage silently upgraded a
// watched session into an unattended one that approves itself. docs/GUIDE.md is
// explicit that the wrappers never add that flag on the user's behalf; a
// fallback is not an exception to it.
func TestAnAttendedRunFallsOverToAnAttendedRun(t *testing.T) {
	claude, ok := agents.Lookup("claude")
	if !ok {
		t.Fatal("claude is not in the descriptor table")
	}

	argv, err := fallbackArgv(claude, "refactor auth", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range claude.SkipPermissionArgs {
		if slices.Contains(argv, flag) {
			t.Errorf("an attended run's fallback carries %s: %v", flag, argv)
		}
	}
	// Still seeded, where the descriptor says how — the task travels, only the
	// approvals do not.
	if !slices.Contains(argv, "refactor auth") {
		t.Errorf("the prompt was dropped: %v", argv)
	}

	// And the unattended case is unchanged: nobody can answer a prompt there, so
	// an agent that stops to ask does not fail, it hangs.
	argv, err = fallbackArgv(claude, "refactor auth", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range claude.SkipPermissionArgs {
		if !slices.Contains(argv, flag) {
			t.Errorf("an unattended fallback is missing %s: %v", flag, argv)
		}
	}
}

// What counts as unattended is the user having said so, never an inference.
func TestAsksForAutonomyReadsTheUsersOwnFlag(t *testing.T) {
	if !asksForAutonomy("claude", []string{"--dangerously-skip-permissions", "do the thing"}) {
		t.Error("the flag the user typed was not recognised")
	}
	if asksForAutonomy("claude", []string{"do the thing"}) {
		t.Error("an ordinary interactive run was read as unattended")
	}
	// codex has no such flag — its non-interactive mode is a subcommand — so
	// nothing in an argv can turn approvals off for it, and claiming otherwise
	// would be an assumption about an agent that never made the promise.
	if asksForAutonomy("codex", []string{"--dangerously-skip-permissions"}) {
		t.Error("codex reported as opted out of approvals it does not have a flag for")
	}
	if asksForAutonomy("not-an-agent", []string{"--dangerously-skip-permissions"}) {
		t.Error("an unknown agent reported an opinion")
	}
}
