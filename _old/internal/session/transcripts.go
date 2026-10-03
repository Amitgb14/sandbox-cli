package session

import (
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
)

// transcriptTail is how many turns are read to decide a pane's state.
//
// Small, because the decision needs the *last* turn and nothing else: who spoke,
// and when. Reading more would cost I/O per pane per refresh for facts nothing
// uses. Four rather than one so a tail whose final turns were all tool calls —
// which `agentctx` drops — still has something left.
const transcriptTail = 4

// ReadTranscripts is the real conversation lookup: the Transcripts a daemon uses.
//
// Two ways to find a pane's conversation, and the difference between them is
// certainty rather than convenience.
//
// A pane that *recorded* one is the easy case and the only exact one. `sandbox.session`
// is stamped when a run resumed a conversation by id, and that is the single
// situation where the pairing is known rather than inferred — which is why the label
// exists at all: a resumed session began before its container, and every correlation
// heuristic assumes the opposite.
//
// Everything else is correlated by agent, project and time window, through
// `agentctx.ConversationFor` — which searches both the host project bucket (where a
// CLI run's transcript lands, because the claude wrapper mounts it) and the
// container's own `/workspace` bucket (where a Studio run's lands, because it mounts
// nothing). It declines rather than guessing: no agent, no verified store, nothing in
// the window, or two candidates in one window all return nothing. That is not a gap
// to be filled later — reading the wrong conversation would report one agent's state
// on another's pane, which is worse than reporting `unknown`.
func ReadTranscripts(pane protocol.Pane, project string) []agentctx.Message {
	if pane.Agent == "" {
		// A plain command has no conversation. Nothing to look for.
		return nil
	}

	// The recorded id first: exact, and it skips the window entirely. Only a
	// *verified* store is read — `agentctx`'s rule, and the reason is that an
	// unverified one is a candidate path rather than a fact about this machine.
	if pane.ConversationID != "" {
		if f, ok := agentctx.Resolve(pane.Agent, agentctx.DefaultRoots(), time.Now()); ok && f.State == agentctx.StateVerified {
			// Both buckets, for the reason ConversationFor documents: a CLI run's
			// transcript lands under the host project path and a Studio run's under
			// the container's own `/workspace`, so searching one finds the other
			// front end's conversations and not this run's.
			for _, proj := range []string{project, agentctx.ContainerWorkspace} {
				sessions, _, err := agentctx.List(f, agentctx.ListOpts{Project: proj})
				if err != nil {
					continue
				}
				if found, _ := agentctx.Find(sessions, pane.ConversationID); found.Path != "" {
					if msgs, err := agentctx.TranscriptOf(f.Format, found.Path, transcriptTail); err == nil {
						return msgs
					}
				}
			}
		}
	}

	// Otherwise correlate. The window opens slightly before the container was
	// created, because the agent writes its first turn after the container starts
	// and the two clocks are not the same one — `sandbox-cli` forwards TZ precisely
	// because they disagree. It has no end: a conversation that began in the window
	// and is still being appended to is the one wanted, and an `until` in the past
	// would exclude exactly that.
	if pane.CreatedAt.IsZero() {
		return nil
	}
	found, sess, ok := agentctx.ConversationFor(pane.Agent, project,
		pane.CreatedAt.Add(-2*time.Minute), time.Now().Add(time.Minute))
	if !ok || sess.Path == "" {
		return nil
	}
	msgs, err := agentctx.TranscriptOf(found.Format, sess.Path, transcriptTail)
	if err != nil {
		return nil
	}
	return msgs
}
