package agentstate

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

var at = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)

func running(tty bool) *api.Process { return &api.Process{PID: 1, State: api.ProcessRunning, Tty: tty} }

func exited(code int) *api.Process {
	return &api.Process{PID: 1, State: api.ProcessExited, ExitCode: &code}
}

// What the sandbox and the process prove settles it before any transcript is
// read: an exit code is a fact, a conversation an inference.
func TestTheSandboxAndProcessSettleItWhenTheyCan(t *testing.T) {
	busy := []agentctx.Message{{Role: "user", Text: "go", At: at}}
	for _, tc := range []struct {
		sandbox string
		p       *api.Process
		want    State
	}{
		{api.StateRunning, exited(0), Done},
		{api.StateRunning, exited(1), Failed},
		{api.StateSuspended, running(true), Suspended},
		{api.StateTerminated, running(true), Stopped},
		// Not knowing what the sandbox is doing means not knowing what the agent
		// is, however busy the transcript looks.
		{"", running(true), Unknown},
		{api.StatePending, running(true), Unknown},
		{api.StateRunning, nil, Unknown},
	} {
		if got := From(Evidence{SandboxState: tc.sandbox, Process: tc.p, Transcript: busy, Now: at}); got != tc.want {
			t.Errorf("%s/%+v: %q, want %q", tc.sandbox, tc.p, got, tc.want)
		}
	}
}

// The three live states, and the fact that separates the last two: whether
// anybody can answer. Without it every finished headless run would report as
// needing attention.
func TestAQuietAgentIsBlockedOnlyWhereSomebodyCanAnswer(t *testing.T) {
	spoke := []agentctx.Message{{Role: "assistant", Text: "done", At: at}}
	stale := at.Add(Quiet + time.Second)
	prompt := []agentctx.Message{{Role: "user", Text: "go", At: at}}
	for _, tc := range []struct {
		name string
		msgs []agentctx.Message
		now  time.Time
		tty  bool
		want State
	}{
		{"spoke just now", spoke, at, true, Working},
		{"spoke just now, headless", spoke, at, false, Working},
		{"quiet with a terminal", spoke, stale, true, Blocked},
		{"quiet with no terminal", spoke, stale, false, Idle},
		// "Thinking" and "hung" are the same evidence; this does not guess.
		{"prompt unanswered, fresh", prompt, at, true, Working},
		{"prompt unanswered, two hours", prompt, at.Add(2 * time.Hour), true, Working},
		// A guest clock ahead of the host's must not make an agent look idle.
		{"a turn from the future", []agentctx.Message{{Role: "assistant", Text: "x", At: at.Add(time.Hour)}}, at, true, Working},
	} {
		if got := From(Evidence{SandboxState: api.StateRunning, Process: running(tc.tty), Transcript: tc.msgs, Now: tc.now}); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Unknown whenever the evidence runs out, never a placeholder for "probably
// fine": an agent before its first turn, a format nothing reads, a turn with
// no time to age.
func TestUnknownWhereTheEvidenceRunsOut(t *testing.T) {
	for name, msgs := range map[string][]agentctx.Message{
		"no transcript":       nil,
		"a turn with no role": {{Text: "?"}},
		"spoke, no timestamp": {{Role: "assistant", Text: "done"}},
	} {
		if got := From(Evidence{SandboxState: api.StateRunning, Process: running(true), Transcript: msgs, Now: at}); got != Unknown {
			t.Errorf("%s: %q, want unknown", name, got)
		}
	}
}

// Every state has words for why it was reported.
func TestEveryStateCanExplainItself(t *testing.T) {
	fallback := Describe("no-such-state")
	for _, s := range []State{Working, Blocked, Idle, Done, Failed, Suspended, Stopped} {
		if d := Describe(s); d == "" || d == fallback {
			t.Errorf("%s has no explanation of its own", s)
		}
	}
}

// Look, over the API: the agent is the sandbox's first process, its transcript
// is read from the guest, and a sandbox no agent run started is not read as
// one.
func TestLookReadsTheAgentFromTheSandbox(t *testing.T) {
	srv := httptest.NewServer((&server.Server{Backend: fake.New(api.CapEgressAllowlist), Policy: spec.DefaultPolicyFor(fake.New(api.CapEgressAllowlist).Capabilities())}).Handler())
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	ctx := context.Background()

	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Labels: map[string]string{AgentLabel: "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"sleep", "30"}, Tty: true}); err != nil {
		t.Fatal(err)
	}
	// A helper started later — a checkpoint, a bring-back — is not the agent.
	c.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"true"}})
	turn := `{"type":"assistant","timestamp":"` + at.Format(time.RFC3339) + `","message":{"role":"assistant","content":[{"type":"text","text":"Shall I go on?"}]}}` + "\n"
	c.WriteFile(ctx, sb.ID, agenthome.GuestHome+"/.claude/projects/-sandbox-home/s.jsonl", []byte(turn))
	sb, _ = c.Sandbox(ctx, sb.ID)

	if r := Look(ctx, c, sb, at.Add(time.Minute)); r.State != Blocked || r.PID != 1 || r.Agent != "claude" {
		t.Errorf("quiet agent with a terminal: %+v", r)
	}
	if r := Look(ctx, c, sb, at.Add(time.Second)); r.State != Working {
		t.Errorf("agent that spoke a second ago: %+v", r)
	}

	plain, _ := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	c.StartProcess(ctx, plain.ID, api.RunRequest{Argv: []string{"sleep", "30"}})
	plain, _ = c.Sandbox(ctx, plain.ID)
	if r := Look(ctx, c, plain, at); r.State != Unknown || !strings.Contains(Describe(r.State), "not enough") {
		t.Errorf("a sandbox no agent run started: %+v", r)
	}
	c.TerminateSandbox(ctx, sb.ID)
	sb, _ = c.Sandbox(ctx, sb.ID)
	if r := Look(ctx, c, sb, at); r.State != Stopped {
		t.Errorf("a terminated sandbox: %+v", r)
	}
}
