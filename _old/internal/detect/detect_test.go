package detect

import (
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
)

// at is the moment the fixtures are written around.
var at = time.Date(2026, 9, 11, 10, 0, 9, 0, time.UTC)

// What the container proves comes first, because an exit code is a fact and a
// transcript is an inference. A finished container's state does not depend on what
// its conversation looked like — including a conversation that would otherwise read
// as `working`.
func TestTheContainerSettlesItWhenItCan(t *testing.T) {
	busy := []agentctx.Message{{Role: "user", Text: "go", At: at}}
	cases := []struct {
		state string
		code  int
		want  protocol.PaneState
	}{
		{"created", 0, protocol.StateStarting},
		{"exited", 0, protocol.StateDone},
		{"exited", 1, protocol.StateFailed},
		{"dead", 137, protocol.StateFailed},
		// An unreadable state is not a licence to infer anything — the rule `clean`
		// already states. Not knowing what the container is doing means not knowing
		// what the agent is, however busy the transcript looks.
		{"", 0, protocol.StateUnknown},
		{"something-new", 0, protocol.StateUnknown},
	}
	for _, tc := range cases {
		got := From(Evidence{
			ContainerState: tc.state, ExitCode: tc.code,
			Transcript: busy, Now: at, OpenStdin: true,
		})
		if got != tc.want {
			t.Errorf("container %q/%d: %q, want %q", tc.state, tc.code, got, tc.want)
		}
	}
}

// The three live states, and the fact that separates the last two: whether anybody
// can answer.
//
// A console pane has a keyboard, so an agent that has gone quiet there is waiting
// for a human. A headless pane has none, so "waiting for a human" is not a state it
// can be in — quiet there is quiet. Without this distinction every finished headless
// run would report as needing attention.
func TestAQuietAgentIsBlockedOnlyWhereSomebodyCanAnswer(t *testing.T) {
	spoke := []agentctx.Message{{Role: "assistant", Text: "done", At: at}}
	stale := at.Add(Quiet + time.Second)

	cases := []struct {
		name      string
		msgs      []agentctx.Message
		now       time.Time
		openStdin bool
		want      protocol.PaneState
	}{
		{"agent spoke just now", spoke, at, true, protocol.StateWorking},
		{"agent spoke just now, headless", spoke, at, false, protocol.StateWorking},
		{"quiet with a console", spoke, stale, true, protocol.StateBlocked},
		{"quiet with no console", spoke, stale, false, protocol.StateIdle},
		// A prompt with nothing after it: the agent owes an answer, and has owed it
		// for two hours. "Thinking" and "hung" are the same evidence, so this does
		// not guess between them.
		{"prompt unanswered, fresh",
			[]agentctx.Message{{Role: "user", Text: "go", At: at}}, at, true, protocol.StateWorking},
		{"prompt unanswered, two hours",
			[]agentctx.Message{{Role: "user", Text: "go", At: at}}, at.Add(2 * time.Hour), true, protocol.StateWorking},
	}
	for _, tc := range cases {
		got := From(Evidence{
			ContainerState: "running", Transcript: tc.msgs,
			Now: tc.now, OpenStdin: tc.openStdin,
		})
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Unknown is the default whenever the evidence runs out, and never a placeholder
// for "probably fine". Each of these is a real shape: a container up before its
// agent's first turn, an agent with no verified store, a format read without
// timestamps.
func TestUnknownWhereTheEvidenceRunsOut(t *testing.T) {
	cases := []struct {
		name string
		e    Evidence
	}{
		{"no transcript at all", Evidence{ContainerState: "running", Now: at}},
		{"transcript of nothing", Evidence{
			ContainerState: "running", Now: at,
			Transcript: []agentctx.Message{{Role: "", Text: "?"}},
		}},
		{"agent spoke, no timestamp to age it", Evidence{
			ContainerState: "running", Now: at, OpenStdin: true,
			Transcript: []agentctx.Message{{Role: "assistant", Text: "done"}},
		}},
	}
	for _, tc := range cases {
		if got := From(tc.e); got != protocol.StateUnknown {
			t.Errorf("%s: %q, want unknown", tc.name, got)
		}
	}

	// The one thing a turn with no timestamp can still settle: who spoke last. A
	// prompt with nothing after it means the agent owes an answer, whenever it was.
	got := From(Evidence{
		ContainerState: "running", Now: at,
		Transcript: []agentctx.Message{{Role: "user", Text: "go"}},
	})
	if got != protocol.StateWorking {
		t.Errorf("unanswered prompt with no timestamp: %q, want working", got)
	}
}

// A container whose clock is ahead of the host's must not make an agent look idle.
//
// The two clocks are genuinely different — one is the host's, the other whatever
// the container wrote — and sandbox-cli forwards TZ precisely because they disagree.
// A negative age is treated as fresh rather than as evidence.
func TestAFutureTurnIsNotEvidence(t *testing.T) {
	future := []agentctx.Message{{Role: "assistant", Text: "done", At: at.Add(time.Hour)}}
	if got := From(Evidence{
		ContainerState: "running", Transcript: future, Now: at, OpenStdin: true,
	}); got != protocol.StateWorking {
		t.Errorf("a turn timestamped in the future: %q, want working", got)
	}
}

// Read through the real agentctx readers, on both formats the phase names, so the
// fixtures are transcripts rather than hand-built Message values. A decision layer
// tested only against its own structs is a decision layer that has never seen a
// transcript.
func TestFromRealTranscripts(t *testing.T) {
	cases := []struct {
		file      string
		format    string
		now       time.Time
		openStdin bool
		want      protocol.PaneState
		why       string
	}{
		{"claude-assistant-last.jsonl", agentctx.FormatClaudeJSONL, at, true,
			protocol.StateWorking, "the agent spoke four seconds ago"},
		{"claude-assistant-last.jsonl", agentctx.FormatClaudeJSONL, at.Add(5 * time.Minute), true,
			protocol.StateBlocked, "quiet for five minutes with a console to answer on"},
		{"claude-assistant-last.jsonl", agentctx.FormatClaudeJSONL, at.Add(5 * time.Minute), false,
			protocol.StateIdle, "quiet for five minutes with nothing able to type"},
		{"claude-user-last.jsonl", agentctx.FormatClaudeJSONL, at.Add(time.Hour), true,
			protocol.StateWorking, "a prompt with nothing after it, however long ago"},
		// The last line is half-written, which is the normal state of a transcript
		// being appended to. The reader drops it; the decision must come from the
		// last *complete* turn rather than from nothing.
		{"claude-truncated.jsonl", agentctx.FormatClaudeJSONL, at, true,
			protocol.StateWorking, "the last complete turn is four seconds old"},
		{"codex-assistant-last.jsonl", agentctx.FormatCodexRollout, at, true,
			protocol.StateWorking, "codex: the agent spoke a second ago"},
		{"codex-assistant-last.jsonl", agentctx.FormatCodexRollout, at.Add(5 * time.Minute), true,
			protocol.StateBlocked, "codex: quiet with a console"},
	}

	for _, tc := range cases {
		msgs, err := agentctx.TranscriptOf(tc.format, "testdata/"+tc.file, 0)
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if len(msgs) == 0 {
			t.Fatalf("%s read as an empty conversation; the fixture or the reader is wrong", tc.file)
		}
		got := From(Evidence{
			ContainerState: "running", Transcript: msgs,
			Now: tc.now, OpenStdin: tc.openStdin,
		})
		if got != tc.want {
			t.Errorf("%s (stdin=%v, +%v): %q, want %q — %s",
				tc.file, tc.openStdin, tc.now.Sub(at), got, tc.want, tc.why)
		}
	}
}

// The impostors both readers are built to drop, checked here because this package's
// whole answer turns on *who spoke last*.
//
// In claude's transcripts a tool result arrives as a user message; in codex's there
// are `developer` turns it ships with and an injected `<environment_context>` block.
// Counted as prompts, every one of them would make the agent look like it owed an
// answer — permanently `working`, which is the state that hides the one the user
// cares about.
func TestToolResultsAndInjectedContextAreNotPrompts(t *testing.T) {
	msgs, err := agentctx.TranscriptOf(agentctx.FormatClaudeJSONL, "testdata/claude-assistant-last.jsonl", 0)
	if err != nil {
		t.Fatal(err)
	}
	last, ok := lastTurn(msgs)
	if !ok {
		t.Fatal("no turns read")
	}
	if last.Role != "assistant" {
		t.Errorf("last turn is %q — the tool result in that fixture was counted as a prompt", last.Role)
	}

	msgs, err = agentctx.TranscriptOf(agentctx.FormatCodexRollout, "testdata/codex-assistant-last.jsonl", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.Role == "developer" {
			t.Error("a developer turn reached the decision; nobody typed it")
		}
		if m.Role == "user" && len(m.Text) > 0 && m.Text[0] == '<' {
			t.Errorf("an injected context block was read as a prompt: %q", m.Text)
		}
	}
}

// Every state has a sentence, because a listing shows `blocked` and somebody will
// ask why. The split is the one internal/fleet's branchRefusal already makes:
// deciding and explaining are different jobs.
func TestEveryStateCanExplainItself(t *testing.T) {
	for _, s := range []protocol.PaneState{
		protocol.StateUnknown, protocol.StateStarting, protocol.StateWorking,
		protocol.StateBlocked, protocol.StateIdle, protocol.StateDone,
		protocol.StateFailed, protocol.StateStopped,
	} {
		if Describe(s) == "" {
			t.Errorf("state %q has no explanation", s)
		}
	}
	// And an invented one still answers, rather than returning empty: a state this
	// package has not heard of is exactly when somebody needs a sentence.
	if Describe(protocol.PaneState("invented")) == "" {
		t.Error("an unknown state explained itself with silence")
	}
}
