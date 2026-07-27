package cli

import (
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// pinStore replaces the two per-machine lookups for the duration of a test.
func pinStore(t *testing.T, f agentctx.Finding, sessions []agentctx.Session) {
	t.Helper()
	origResolve, origList := resolveAgentStore, listAgentSessions
	t.Cleanup(func() { resolveAgentStore, listAgentSessions = origResolve, origList })

	resolveAgentStore = func(string) (agentctx.Finding, bool) {
		return f, f.State == agentctx.StateVerified
	}
	listAgentSessions = func(agentctx.Finding, agentctx.ListOpts) ([]agentctx.Session, error) {
		return sessions, nil
	}
}

func verifiedClaude() agentctx.Finding {
	return agentctx.Finding{
		Agent:  "claude",
		State:  agentctx.StateVerified,
		Resume: []string{"--resume"},
	}
}

// runSession is a rescue session that ran between start and end.
func runSession(agent string, start, end time.Time) rescue.Session {
	return rescue.Session{
		Agent:     agent,
		Workspace: "/Users/x/project/p",
		Branch:    "fix-x",
		StartedAt: start,
		EndedAt:   &end,
	}
}

// TestFindConversationMatchesTheRunsOwnWindow is the core of issue #16: after a
// crash the files are usually already on disk, and the conversation is the half
// that actually went missing — so restore has to be able to name it.
func TestFindConversationMatchesTheRunsOwnWindow(t *testing.T) {
	start := time.Now().Add(-2 * time.Hour)
	end := start.Add(time.Hour)

	pinStore(t, verifiedClaude(), []agentctx.Session{
		// Newest first, as agentctx.List returns them.
		{ID: "bbbbbbbb-0000-0000-0000-000000000002", Modified: end.Add(-time.Minute)},
		{ID: "aaaaaaaa-0000-0000-0000-000000000001", Modified: start.Add(time.Minute)},
	})

	c, ok := findConversation(runSession("claude", start, end))
	if !ok {
		t.Fatal("no conversation found for a run whose transcripts sit inside its window")
	}
	if c.session.ID != "bbbbbbbb-0000-0000-0000-000000000002" {
		t.Errorf("picked %q, want the transcript written closest to the end of the run", c.session.ID)
	}
	if c.others != 1 {
		t.Errorf("others = %d, want 1 — overlapping sessions are reported, not hidden", c.others)
	}
	if got := c.resumeCommand(); got != "sandbox-cli claude --resume bbbbbbbb" {
		t.Errorf("resumeCommand() = %q", got)
	}
}

// A transcript from a different run in the same project must not be offered:
// resuming the wrong conversation is worse than being told there is none.
func TestFindConversationIgnoresTranscriptsOutsideTheWindow(t *testing.T) {
	start := time.Now().Add(-2 * time.Hour)
	end := start.Add(time.Hour)

	for _, tc := range []struct {
		name     string
		modified time.Time
	}{
		{"long before the run", start.Add(-24 * time.Hour)},
		{"just before the slack", start.Add(-conversationSlackBefore - time.Minute)},
		{"well after the run", end.Add(conversationSlackAfter + time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinStore(t, verifiedClaude(), []agentctx.Session{{ID: "cccccccc-0000-0000-0000-000000000003", Modified: tc.modified}})
			if _, ok := findConversation(runSession("claude", start, end)); ok {
				t.Error("offered a transcript from outside the run's window")
			}
		})
	}
}

// The cases where saying nothing is the right answer.
func TestFindConversationDeclinesToGuess(t *testing.T) {
	now := time.Now()
	inWindow := []agentctx.Session{{ID: "dddddddd-0000-0000-0000-000000000004", Modified: now}}

	t.Run("a plain run has no agent", func(t *testing.T) {
		pinStore(t, verifiedClaude(), inWindow)
		if _, ok := findConversation(runSession("", now.Add(-time.Hour), now)); ok {
			t.Error("offered a conversation for a run that had no agent")
		}
	})

	t.Run("no verified store", func(t *testing.T) {
		f := verifiedClaude()
		f.State = agentctx.StateMissing
		pinStore(t, f, inWindow)
		if _, ok := findConversation(runSession("claude", now.Add(-time.Hour), now)); ok {
			t.Error("offered a conversation from an unverified store")
		}
	})

	t.Run("the descriptor knows no resume flag", func(t *testing.T) {
		f := verifiedClaude()
		f.Resume = nil
		pinStore(t, f, inWindow)
		if _, ok := findConversation(runSession("claude", now.Add(-time.Hour), now)); ok {
			t.Error("offered a resume command for an agent with no known resume argument")
		}
	})

	t.Run("no sessions at all", func(t *testing.T) {
		pinStore(t, verifiedClaude(), nil)
		if _, ok := findConversation(runSession("claude", now.Add(-time.Hour), now)); ok {
			t.Error("offered a conversation when the store held none")
		}
	})
}

// A run that is still going has no EndedAt, and that is the state `recover` is
// most often used in. Activity() stands in for the end, so a live run's
// transcript is still found.
func TestFindConversationHandlesAnUnfinishedRun(t *testing.T) {
	start := time.Now().Add(-30 * time.Minute)
	s := rescue.Session{
		Agent:     "claude",
		Workspace: "/Users/x/project/p",
		StartedAt: start,
		// EndedAt nil: crashed, or still running.
	}
	pinStore(t, verifiedClaude(), []agentctx.Session{{ID: "eeeeeeee-0000-0000-0000-000000000005", Modified: start.Add(5 * time.Minute)}})

	if _, ok := findConversation(s); !ok {
		t.Error("a crashed run's conversation was not found — this is the case recover exists for")
	}
}
