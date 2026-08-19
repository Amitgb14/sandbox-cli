package studioapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/handoff"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// The session id used by the fixture below, in the shape claude writes.
const handoffSessionID = "3f2a1b4c-0000-4000-8000-0000000000ff"

const handoffTranscript = `{"type":"user","cwd":"/workspace","timestamp":"2026-08-17T10:00:01.000Z","sessionId":"3f2a1b4c-0000-4000-8000-0000000000ff","message":{"role":"user","content":"add pagination to /orders"}}
{"type":"assistant","timestamp":"2026-08-17T10:00:09.000Z","sessionId":"3f2a1b4c-0000-4000-8000-0000000000ff","message":{"role":"assistant","content":[{"type":"text","text":"Added a cursor and a limit."}]}}
`

// writeSandboxSession plants one claude conversation in the **sandbox-owned**
// store — the agent HOME containers get, under the temp XDG_CONFIG_HOME
// newTestServer sets, so this never touches the developer's own ~/.claude.
func writeSandboxSession(t *testing.T) {
	t.Helper()
	dir := config.AgentStateDir("claude")
	if dir == "" {
		t.Skip("no agent state dir resolvable in this environment")
	}
	bucket := filepath.Join(dir, ".claude", "projects", "-workspace")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatalf("creating store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bucket, handoffSessionID+".jsonl"), []byte(handoffTranscript), 0o644); err != nil {
		t.Fatalf("writing transcript: %v", err)
	}
}

// A handoff is not a resume, and the request must not be able to blur them.
//
// Each refusal here is a way of asking for a briefing that could only fail
// later, inside a container, where the reason would be invisible: no agent to
// read it, no task to do, or half a reference to a conversation.
func TestHandoffRefusals(t *testing.T) {
	s, _ := newTestServer(t)

	cases := []struct {
		name string
		req  RunCreateRequest
		want string
	}{
		{
			name: "with resume",
			req: RunCreateRequest{
				Agent: "claude", Console: true, Prompt: "carry on",
				Resume:      handoffSessionID,
				HandoffFrom: &HandoffRef{Agent: "claude", SessionID: handoffSessionID},
			},
			want: "cannot be combined",
		},
		{
			name: "with a plain command",
			req: RunCreateRequest{
				Command:     []string{"echo", "hi"},
				Prompt:      "carry on",
				HandoffFrom: &HandoffRef{Agent: "claude", SessionID: handoffSessionID},
			},
			want: "needs an agent",
		},
		{
			name: "with no prompt",
			req: RunCreateRequest{
				Agent:       "codex",
				HandoffFrom: &HandoffRef{Agent: "claude", SessionID: handoffSessionID},
			},
			want: "needs a prompt",
		},
		{
			name: "with half a reference",
			req: RunCreateRequest{
				Agent: "codex", Prompt: "carry on",
				HandoffFrom: &HandoffRef{Agent: "claude"},
			},
			want: "agent and a session id",
		},
		{
			name: "naming a conversation that does not exist",
			req: RunCreateRequest{
				Agent: "codex", Prompt: "carry on",
				HandoffFrom: &HandoffRef{Agent: "claude", SessionID: "no-such-session"},
			},
			want: "no conversation",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", tc.req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("refusal did not explain itself: got %q, want it to mention %q", rec.Body.String(), tc.want)
			}
		})
	}
}

// The whole of a handoff, end to end: the conversation is read, the briefing is
// mounted read-only, the prompt says what it is, and the run records whose
// conversation it came from.
func TestHandoffMountsABriefingAndRecordsIt(t *testing.T) {
	s, fr := newTestServer(t)
	writeSandboxSession(t)

	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent:       "codex",
		Prompt:      "finish the pagination work",
		Branch:      "handoff-x",
		HandoffFrom: &HandoffRef{Agent: "claude", SessionID: handoffSessionID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(fr.started) != 1 {
		t.Fatalf("started %d containers, want 1", len(fr.started))
	}
	spec := fr.started[0]

	var mount string
	for _, m := range spec.Mounts {
		if m.Target == handoff.GuestDir {
			mount = m.Source
			if !m.RO {
				t.Error("the briefing is mounted writable; an agent that can rewrite it can rewrite its own history")
			}
		}
	}
	if mount == "" {
		t.Fatalf("no briefing mounted at %s: %+v", handoff.GuestDir, spec.Mounts)
	}
	for _, name := range []string{"HANDOFF.md", "transcript.jsonl", "files.md"} {
		if _, err := os.Stat(filepath.Join(mount, name)); err != nil {
			t.Errorf("briefing is missing %s: %v", name, err)
		}
	}

	// The target is told it is reading a briefing. This is the sentence that
	// keeps the whole mechanism honest — an agent told it is *resuming* answers
	// as though a conversation it never had were its own.
	argv := strings.Join(spec.Command, " ")
	if !strings.Contains(argv, handoff.GuestDir) {
		t.Errorf("the prompt does not point at the briefing: %s", argv)
	}
	if !strings.Contains(argv, "finish the pagination work") {
		t.Errorf("the original task did not survive the briefing preamble: %s", argv)
	}

	// Recorded on the container, because a fact not stamped is one no later
	// command can recover — and this one answers "why is codex doing claude's
	// work" with a person's decision rather than an outage.
	if got := spec.Labels[sandbox.LabelHandoffFrom]; got != "claude" {
		t.Errorf("%s = %q, want claude", sandbox.LabelHandoffFrom, got)
	}
	if got := spec.Labels[sandbox.LabelHandoffSession]; got != handoffSessionID {
		t.Errorf("%s = %q, want the source session id", sandbox.LabelHandoffSession, got)
	}
	// Not routing. The two look identical in a listing and answer different
	// questions, which is exactly why they are separate labels.
	if got := spec.Labels[sandbox.LabelRoutedFrom]; got != "" {
		t.Errorf("%s = %q on a handoff; nothing fell over here, somebody chose", sandbox.LabelRoutedFrom, got)
	}
}

// A conversation of an agent that cannot reopen one by id is still readable, and
// must not be advertised as resumable: the launch refuses it, and an offer that
// can only 400 is worse than no offer.
func TestSessionResumableRequiresAResumeArgv(t *testing.T) {
	if canResume("gemini") {
		t.Skip("gemini has gained a resume argv; this test's premise is gone")
	}
	got := toSessionSummary("gemini", sessionFixture(), storeSandbox)
	if got.Resumable {
		t.Error("a gemini session in the sandbox store reported resumable, but buildRunOptions refuses it for having no verified resume flag")
	}
	if claude := toSessionSummary("claude", sessionFixture(), storeSandbox); !claude.Resumable {
		t.Error("a claude session in the sandbox store must stay resumable")
	}
	if host := toSessionSummary("claude", sessionFixture(), storeHost); host.Resumable {
		t.Error("a host session reported resumable; resuming it would mount the host's history into a container that was not asked to have it")
	}
}

// sessionFixture is one listed conversation, with only the fields Resumable
// depends on.
func sessionFixture() agentctx.Session {
	return agentctx.Session{ID: handoffSessionID, Turns: 2}
}

// A handoff run that fails over must not carry its request's handoff into the
// retry.
//
// The request is what the supervisor rebuilds from, so a `handoffFrom` left set
// makes buildRunOptions resolve the source session again, write a second export
// and prepend a second preamble — on top of the briefing the failover itself
// just wrote. Two mounts land on /sandbox/context, docker refuses a duplicate
// mount point, and the retry never starts: the failover breaks in exactly the
// case where the run it is rescuing began as a handoff.
//
// Asserted through buildRunOptions rather than by starting a supervisor, because
// what has to hold is a property of the options: one briefing mount, one
// preamble, however many times a request is rebuilt.
func TestARebuiltHandoffRequestMountsOneBriefing(t *testing.T) {
	s, _ := newTestServer(t)
	writeSandboxSession(t)

	req := RunCreateRequest{
		Agent:       "codex",
		Prompt:      "finish the pagination work",
		Branch:      "handoff-failover",
		HandoffFrom: &HandoffRef{Agent: "claude", SessionID: handoffSessionID},
	}

	first, err := s.buildRunOptions(context.Background(), req)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if got := countBriefingMounts(first.ExtraMounts); got != 1 {
		t.Fatalf("first build produced %d briefing mounts, want 1", got)
	}

	// What the supervisor does: the same request, re-targeted. It clears
	// HandoffFrom, so the rebuild carries no briefing of its own and the one the
	// failover wrote is the only one.
	retry := req
	retry.Agent = "claude"
	retry.HandoffFrom = nil
	second, err := s.buildRunOptions(context.Background(), retry)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := countBriefingMounts(second.ExtraMounts); got != 0 {
		t.Errorf("a rebuilt request wrote %d briefings of its own; the failover's is the one that counts", got)
	}
	if strings.Count(strings.Join(second.Command, " "), "A previous agent") > 1 {
		t.Error("the briefing preamble was prepended twice")
	}
}

func countBriefingMounts(mounts []string) int {
	n := 0
	for _, m := range mounts {
		if strings.Contains(m, handoff.GuestDir) {
			n++
		}
	}
	return n
}

// A conversation sandbox-cli cannot read is refused rather than exported empty.
//
// handoff.Write is happy to produce a briefing from a transcript it could not
// parse — correct for the supervisor, where the file ledger is the useful part —
// but here somebody picked *this conversation*, and an empty transcript.jsonl
// under a prompt announcing "0 prompts of that conversation" is a claim that it
// crossed when it did not.
func TestHandoffRefusesASourceWithNoVerifiedReader(t *testing.T) {
	s, _ := newTestServer(t)
	dir := config.AgentStateDir("gemini")
	if dir == "" {
		t.Skip("no agent state dir resolvable in this environment")
	}
	// A gemini session: listed, with real dates, and `partial` because no reader
	// exists for the format.
	bucket := filepath.Join(dir, ".gemini", "tmp", "abc123")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatalf("creating store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bucket, "logs.json"), []byte("[]"), 0o644); err != nil {
		t.Fatalf("writing log: %v", err)
	}

	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent:       "codex",
		Prompt:      "carry on",
		HandoffFrom: &HandoffRef{Agent: "gemini", SessionID: "abc123"},
	})
	if rec.Code == http.StatusCreated {
		t.Fatal("a handoff from an unreadable conversation was launched; the briefing would carry nothing")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
