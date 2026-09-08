package studioapi

import (
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// The case the first version of this file could not fail on.
//
// Every other test here asserts *silence*, and silence is also what a
// correlation that can never match produces — so a suite made only of refusals
// passes forever while the feature does nothing. It did: the first version
// searched the sandbox-owned store with a host-path project filter, which is
// two individually-correct choices that together match nothing (14 sessions the
// shared way, 0 that way, measured). This is the assertion that would have
// caught it.
func TestARestoreNamesTheConversationItFound(t *testing.T) {
	s, _ := newTestServer(t)
	start := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	end := start.Add(20 * time.Minute)

	restore := agentctx.PinLookups(t,
		agentctx.Finding{
			Agent:  "claude",
			State:  agentctx.StateVerified,
			Resume: []string{"--resume"},
		},
		[]agentctx.Session{
			{ID: "the-one", Path: "/p/the-one", Started: start.Add(2 * time.Minute)},
		},
	)
	defer restore()

	agent, id := s.conversationFor(rescue.Session{
		ID:        "snap",
		Agent:     "claude",
		Workspace: "/repo",
		StartedAt: start,
		EndedAt:   &end,
	})
	if agent != "claude" || id != "the-one" {
		t.Fatalf("conversationFor = %q/%q, want claude/the-one", agent, id)
	}
}

// A restore hands back files and starts nothing, which reads as nothing having
// happened. Naming the conversation is what makes the second half one click
// instead of a hunt — so the cases where it stays quiet are the ones worth
// pinning, because every one of them is a decision rather than a gap.
func TestConversationForStaysQuietWhenItCannotBeSure(t *testing.T) {
	s, _ := newTestServer(t)
	now := time.Now()

	for _, tc := range []struct {
		name string
		sess rescue.Session
	}{
		{
			// A plain `run` has no conversation at all, and offering to resume one
			// would be inventing it.
			name: "no agent recorded",
			sess: rescue.Session{ID: "x", Workspace: "/w", StartedAt: now},
		},
		{
			// Without a project there is nothing to scope the search to, and the
			// whole store is every conversation on the machine.
			name: "no workspace recorded",
			sess: rescue.Session{ID: "x", Agent: "claude", StartedAt: now},
		},
		{
			// An agent nothing knows about resolves to no store, which is not the
			// same as a store that is empty.
			name: "unknown agent",
			sess: rescue.Session{ID: "x", Agent: "nosuchagent", Workspace: "/w", StartedAt: now},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent, id := s.conversationFor(tc.sess)
			if agent != "" || id != "" {
				t.Errorf("offered %q/%q for a session it cannot identify", agent, id)
			}
		})
	}
}

// The window is the run's own, and it is applied to when a session *began*.
// PickSessionIn is where that rule lives; this pins that conversationFor hands
// it a window derived from the manifest rather than something wider.
func TestTheWindowComesFromTheRun(t *testing.T) {
	start := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	sess := rescue.Session{StartedAt: start, EndedAt: &end}

	from := sess.StartedAt.Add(-conversationSlack)
	until := sess.Activity().Add(conversationSlack)

	inside := []agentctx.Session{{ID: "during", Path: "/p/during", Started: start.Add(time.Minute)}}
	if got, ok := agentctx.PickSessionIn(inside, from, until, ""); !ok || got.ID != "during" {
		t.Errorf("a session inside the run's window was not picked: %+v (%v)", got, ok)
	}

	// A session that began before the run did cannot be that run's, however
	// recently it was written to — the rule console.go documents from a real
	// misattribution.
	before := []agentctx.Session{{ID: "earlier", Path: "/p/earlier", Started: start.Add(-2 * time.Hour)}}
	if got, ok := agentctx.PickSessionIn(before, from, until, ""); ok {
		t.Errorf("a session that began before the run was picked: %+v", got)
	}

	// Two in one window and no prompt to separate them: nothing, rather than the
	// newest. Resuming the wrong conversation is worse than offering none.
	two := []agentctx.Session{
		{ID: "a", Path: "/p/a", Started: start.Add(time.Minute)},
		{ID: "b", Path: "/p/b", Started: start.Add(2 * time.Minute)},
	}
	if got, ok := agentctx.PickSessionIn(two, from, until, ""); ok {
		t.Errorf("guessed between two sessions in one window: %+v", got)
	}
}

// PickSession is now a projection of PickSessionIn, and the two must not drift —
// that is the whole reason the choosing was moved rather than copied.
func TestPickSessionIsTheSameChoice(t *testing.T) {
	now := time.Now()
	sessions := []agentctx.Session{
		{ID: "only", Path: "/p/only", Started: now},
	}
	from, until := now.Add(-time.Minute), now.Add(time.Minute)

	path, okPath := agentctx.PickSession(sessions, from, until, "")
	sess, okSess := agentctx.PickSessionIn(sessions, from, until, "")
	if okPath != okSess || path != sess.Path {
		t.Fatalf("PickSession = %q/%v, PickSessionIn = %q/%v", path, okPath, sess.Path, okSess)
	}
}
