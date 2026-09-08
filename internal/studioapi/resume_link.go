package studioapi

import (
	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// conversationFor names the transcript belonging to a restored run, so a restore
// can offer to continue it rather than only handing back files.
//
// The correlation itself is agentctx.ConversationFor, shared with
// `sandbox-cli recover restore` — the two must not name different conversations
// for the same snapshot, and the first version of this file managed exactly that
// by searching the sandbox-owned store with a host-path project filter. Those
// two choices are each right somewhere else and together match nothing: a
// sandbox's transcripts live in the host bucket for the project, while the
// sandbox-owned store buckets by the container's own cwd, which is always
// /workspace. Measured on a real machine: 14 sessions the shared way, 0 that
// way — and the failure is silent, because an empty answer is also what this
// returns whenever it declines to guess.
//
// What is left here is the window, which is the one thing only a rescue session
// knows: from when the run started to its last recorded activity.
func (s *Server) conversationFor(sess rescue.Session) (agent, sessionID string) {
	from := sess.StartedAt.Add(-conversationSlack)
	until := sess.Activity().Add(conversationSlack)

	f, found, ok := agentctx.ConversationFor(sess.Agent, sess.Workspace, from, until)
	if !ok || found.ID == "" {
		return "", ""
	}
	return f.Agent, found.ID
}
