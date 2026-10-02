package studioapi

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
)

// The bug this pins, observed rather than imagined: a console run that had been
// alive for three minutes showed a conversation from two days earlier. The
// developer's own Claude Code session is by definition the most recently
// *modified* transcript on the machine, so a window applied to mtime matched it
// every time.

func TestSandboxStorePicksTheMountedHome(t *testing.T) {
	f := agentctx.Finding{
		Agent: "claude",
		Root:  agentctx.RootHome,
		Dir:   "/home/me/.claude/projects",
		Locations: []agentctx.Location{
			{Root: agentctx.RootHome, Dir: "/home/me/.claude/projects"},
			{Root: agentctx.RootAgent, Dir: "/home/me/.config/sandbox/agents/claude/.claude/projects"},
		},
	}
	got := sandboxStore(f)
	if got.Dir != "/home/me/.config/sandbox/agents/claude/.claude/projects" {
		t.Errorf("Dir = %q, want the sandbox-owned store", got.Dir)
	}
	if got.Root != agentctx.RootAgent {
		t.Errorf("Root = %q, want %q", got.Root, agentctx.RootAgent)
	}
}

// No sandbox store is a real state — --no-persist-auth gives the agent a HOME
// that went away with the container — and it reports nothing rather than
// falling back to the user's own history.

func TestSandboxStoreRefusesToFallBackToTheHostHistory(t *testing.T) {
	f := agentctx.Finding{
		Agent:     "claude",
		Root:      agentctx.RootHome,
		Dir:       "/home/me/.claude/projects",
		Locations: []agentctx.Location{{Root: agentctx.RootHome, Dir: "/home/me/.claude/projects"}},
	}
	if got := sandboxStore(f); got.Dir != "" {
		t.Errorf("Dir = %q, want empty so the caller reports nothing", got.Dir)
	}
}

// Two agents running at once pool into the same `-workspace` directory, so the
// clock cannot separate them. This is the case that shipped broken: a
// reviewer's conversation came back as a concurrent test run's, because the
// test started inside the reviewer's window and had the newer mtime.
