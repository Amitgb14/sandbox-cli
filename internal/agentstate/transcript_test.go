package agentstate

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// The transcript comes out of the guest: only .jsonl files in the agent's
// bucket are read, and the conversation that ended last is the one handed on.
func TestReadTranscriptFromTheSandbox(t *testing.T) {
	srv := httptest.NewServer((&server.Server{Backend: fake.New(), Policy: spec.DefaultPolicy()}).Handler())
	defer srv.Close()
	c, err := api.NewClient(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
	if err != nil {
		t.Fatal(err)
	}
	dir := workspace.GuestHome + "/.claude/projects/-workspace"
	line := func(prompt, at string) []byte {
		return []byte(`{"type":"user","timestamp":"` + at + `","message":{"role":"user","content":"` + prompt + `"}}` + "\n")
	}
	c.WriteFile(ctx, sb.ID, dir+"/old.jsonl", line("earlier session", "2026-10-02T09:00:00Z"))
	c.WriteFile(ctx, sb.ID, dir+"/new.jsonl", line("this session", "2026-10-02T10:00:00Z"))
	c.WriteFile(ctx, sb.ID, dir+"/notes.txt", line("not a transcript", "2026-10-02T11:00:00Z"))

	msgs := ReadTranscript(ctx, c, sb.ID, "claude")
	if len(msgs) != 1 || msgs[0].Text != "this session" {
		t.Fatalf("got %+v", msgs)
	}
	if ReadTranscript(ctx, c, sb.ID, "gemini") != nil {
		t.Error("an agent whose format is unverified had its transcript read")
	}

	// codex keeps sessions sharded by date, and its reader drops the context
	// it injects: what crosses is the typed prompt and the answer.
	rollout := func(prompt, answer, at string) []byte {
		return []byte(`{"timestamp":"` + at + `","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>x</environment_context>"}]}}` + "\n" +
			`{"timestamp":"` + at + `","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + prompt + `"}]}}` + "\n" +
			`{"timestamp":"` + at + `","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + answer + `"}]}}` + "\n")
	}
	codex := workspace.GuestHome + "/.codex/sessions/2026/10/02"
	c.WriteFile(ctx, sb.ID, codex+"/rollout-2026-10-02T09-00-00-a.jsonl", rollout("earlier", "ok", "2026-10-02T09:00:00Z"))
	c.WriteFile(ctx, sb.ID, codex+"/rollout-2026-10-02T10-00-00-b.jsonl", rollout("fix the tests", "they pass", "2026-10-02T10:00:00Z"))
	c.WriteFile(ctx, sb.ID, codex+"/notes.jsonl", rollout("not a session", "x", "2026-10-02T11:00:00Z"))
	msgs = ReadTranscript(ctx, c, sb.ID, "codex")
	if len(msgs) != 2 || msgs[0].Text != "fix the tests" || msgs[1].Text != "they pass" {
		t.Fatalf("codex: got %+v", msgs)
	}
}
