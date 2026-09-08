package studioapi

import (
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// conversationFor finds the transcript belonging to a rescue session, so a
// restore can offer to continue it rather than only handing back files.
//
// It is the same correlation `transcriptFor` does for a container, against the
// same picker, and the two are deliberately not one function: a container knows
// its own session id from a label and can skip all of this, where a snapshot has
// only the three things its manifest records — the agent, the project, and when
// the run was alive. What they must share is the *choosing*, which is why that
// lives in agentctx.PickSessionIn and not in either caller.
//
// Only the sandbox-owned store is searched, for the reason console.go documents
// at length: the claude wrapper has two, and the other is the user's own
// ~/.claude — where the most recently modified transcript on the machine is
// whatever the developer is doing right now.
func (s *Server) conversationFor(sess rescue.Session) (agent, sessionID string) {
	if sess.Agent == "" || sess.Workspace == "" {
		return "", "" // a plain `run`: there is no conversation to resume
	}
	f, ok := agentctx.Resolve(sess.Agent, agentctx.DefaultRoots(), time.Now())
	if !ok || f.State != agentctx.StateVerified || len(f.Resume) == 0 {
		return "", ""
	}
	f = sandboxStore(f)
	if f.Dir == "" {
		return "", ""
	}
	sessions, _, err := agentctx.List(f, agentctx.ListOpts{Project: sess.Workspace})
	if err != nil || len(sessions) == 0 {
		return "", ""
	}

	// The window is the run's own: from when it started to its last recorded
	// activity, widened by the same slack a container's window uses. A session
	// that began before the run did cannot be that run's.
	from := sess.StartedAt.Add(-conversationSlack)
	until := sess.Activity().Add(conversationSlack)

	// No prompt to disambiguate with — a rescue manifest does not record one —
	// so several sessions in one window resolve to nothing rather than to the
	// newest. That is the same bargain every other correlation here makes.
	found, ok := agentctx.PickSessionIn(sessions, from, until, "")
	if !ok || found.ID == "" {
		return "", ""
	}
	return f.Agent, found.ID
}
