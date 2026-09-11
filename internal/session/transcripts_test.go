package session

import (
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
)

// The refusals, which are the load-bearing half of the lookup.
//
// Reading the *wrong* conversation would report one agent's state on another's pane —
// the misattribution `agentctx.ConversationFor` was rewritten to prevent, where a
// Studio run's correlation could return the developer's own Claude Code session. So
// every case where the pairing is not known returns nothing, and `detect` answers
// `unknown`, which is an answer.
func TestReadTranscriptsDeclinesRatherThanGuessing(t *testing.T) {
	// Point the stores somewhere empty, so a developer's real transcripts cannot
	// make this pass or fail.
	t.Setenv("HOME", shortTmp(t))
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))

	cases := []struct {
		name string
		pane protocol.Pane
	}{
		{"no agent: a plain command has no conversation",
			protocol.Pane{ID: "p_1", Kind: protocol.PaneCommand}},
		{"an agent with no verified store on this machine",
			protocol.Pane{ID: "p_2", Agent: "claude", CreatedAt: time.Now()}},
		{"no creation time, so no window to correlate in",
			protocol.Pane{ID: "p_3", Agent: "claude"}},
		{"a recorded conversation id that resolves to nothing",
			protocol.Pane{ID: "p_4", Agent: "claude", ConversationID: "no-such-session", CreatedAt: time.Now()}},
	}
	for _, tc := range cases {
		if got := ReadTranscripts(tc.pane, "/repo"); got != nil {
			t.Errorf("%s: returned %d messages, want none", tc.name, len(got))
		}
	}
}

// A pane with no agent must not cost a store probe. It is the commonest pane there
// is — every `run --` — and the daemon refreshes on every request.
func TestReadTranscriptsSkipsAPaneWithNoAgent(t *testing.T) {
	t.Setenv("HOME", shortTmp(t))
	// No XDG_CONFIG_HOME at all: if this probed the registry it would touch the
	// developer's own. Returning early is what makes that safe.
	if got := ReadTranscripts(protocol.Pane{ID: "p_1"}, "/repo"); got != nil {
		t.Errorf("a pane with no agent read %d messages", len(got))
	}
}
