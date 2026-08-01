package studioapi

import (
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
)

// The bug this pins, observed rather than imagined: a console run that had been
// alive for three minutes showed a conversation from two days earlier. The
// developer's own Claude Code session is by definition the most recently
// *modified* transcript on the machine, so a window applied to mtime matched it
// every time.
func TestPickSessionIgnoresAnOldSessionThatIsStillBeingWritten(t *testing.T) {
	now := time.Now()
	runStart := now.Add(-3 * time.Minute)

	sessions := []agentctx.Session{
		// Newest by mtime — the host's own live session, started two days ago and
		// appended to seconds ago. List returns it first.
		{Path: "/host/live.jsonl", Started: now.Add(-48 * time.Hour), Modified: now},
		// The run's own transcript: started with the container.
		{Path: "/sandbox/run.jsonl", Started: runStart.Add(2 * time.Second), Modified: now.Add(-time.Minute)},
	}

	got, ok := pickSession(sessions, runStart.Add(-2*time.Minute), now.Add(2*time.Minute))
	if !ok {
		t.Fatal("expected the run's own transcript to be found")
	}
	if got != "/sandbox/run.jsonl" {
		t.Errorf("picked %q, want the session that started with the run", got)
	}
}

// Nothing in the window means nothing reported. The reply box hangs off this
// answer, so a near-miss must not be promoted to a match.
func TestPickSessionReportsNothingRatherThanTheClosest(t *testing.T) {
	now := time.Now()
	sessions := []agentctx.Session{
		{Path: "/a.jsonl", Started: now.Add(-72 * time.Hour), Modified: now},
		{Path: "/b.jsonl", Started: now.Add(-71 * time.Hour), Modified: now},
	}
	if got, ok := pickSession(sessions, now.Add(-5*time.Minute), now); ok {
		t.Errorf("picked %q, want no match", got)
	}
}

// A session whose first line could not be read has no start time. It cannot
// pass a filter it has no value for.
func TestPickSessionSkipsASessionWithNoStartTime(t *testing.T) {
	now := time.Now()
	sessions := []agentctx.Session{{Path: "/unknown.jsonl", Modified: now}}
	if _, ok := pickSession(sessions, now.Add(-5*time.Minute), now); ok {
		t.Error("a session with no recorded start must not match")
	}
}

// Only the sandbox-owned store can hold a container's transcript: that
// directory is the container's whole HOME. The claude wrapper really does have
// two verified stores, and the other one is the user's own history.
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
