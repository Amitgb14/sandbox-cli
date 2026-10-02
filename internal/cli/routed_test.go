package cli

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

type attempt struct {
	code    int
	changed *bool
}

// routedEnv points the user config at a temp dir that turns the probes off, so
// the test never reaches a provider, and scripts each attempt's outcome.
func routedEnv(t *testing.T, script ...attempt) *[]runSpec {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	os.MkdirAll(filepath.Join(cfg, "sandbox"), 0o700)
	os.WriteFile(filepath.Join(cfg, "sandbox", "config.yaml"),
		[]byte("providers:\n  claude: \"\"\n  codex: \"\"\n  gemini: \"\"\n"), 0o600)
	var seen []runSpec
	old := execute
	t.Cleanup(func() { execute = old })
	execute = func(_ context.Context, _ *runFlags, rs runSpec) (int, error) {
		seen = append(seen, rs)
		a := script[len(seen)-1]
		if rs.result != nil {
			rs.result.changed = a.changed
		}
		return a.code, nil
	}
	return &seen
}

func ptr(b bool) *bool { return &b }

func routed(t *testing.T, fallbacks ...string) (int, error) {
	t.Helper()
	d, _ := agents.LookupInteractive("claude")
	rf := &runFlags{project: t.TempDir(), fallback: fallbacks}
	return routedRun(context.Background(), rf, d, []string{"-p", "fix the bug"})
}

// A run that failed having changed nothing is an outage, and the next agent
// gets the task in its own spelling with a briefing in front of it.
func TestRoutedRunFallsThroughOnlyWhenNothingChanged(t *testing.T) {
	seen := routedEnv(t, attempt{1, ptr(false)}, attempt{0, nil})
	code, err := routed(t, "codex")
	if err != nil || code != 0 || len(*seen) != 2 {
		t.Fatalf("code %d err %v attempts %d", code, err, len(*seen))
	}
	second := (*seen)[1]
	if second.agent.Name != "codex" || second.before == nil {
		t.Fatalf("fallback: agent %v, briefing hook set %v", second.agent.Name, second.before != nil)
	}
	last := second.argv[len(second.argv)-1]
	if !strings.Contains(last, "briefing, not a resumed conversation") || !strings.HasSuffix(last, "fix the bug") {
		t.Errorf("fallback prompt:\n%s", last)
	}
	// One episode, one id; the fallback says where it came from and why.
	first, fb := (*seen)[0].labels, second.labels
	if first["route.id"] == "" || first["route.id"] != fb["route.id"] || first["route.attempt"] != "1" || fb["route.attempt"] != "2" {
		t.Errorf("route labels: %v / %v", first, fb)
	}
	if fb["route.from"] != "claude" || !strings.Contains(fb["route.reason"], "changed nothing") || first["route.from"] != "" {
		t.Errorf("route.from/reason: %v / %v", first, fb)
	}
	// The primary ran exactly what was typed.
	if first := (*seen)[0].argv; first[len(first)-1] != "fix the bug" || first[len(first)-2] != "-p" {
		t.Errorf("primary argv %q", first)
	}
}

// A failed run that changed files is a failed attempt, not an outage; one
// whose workspace could not be compared counts as work done. Neither retries:
// a wrong retry puts a second agent on top of the first one's edits.
func TestRoutedRunDoesNotRetryWorkOrTheUnknown(t *testing.T) {
	for name, a := range map[string]attempt{"changed": {1, ptr(true)}, "unknown": {1, nil}, "succeeded": {0, ptr(false)}} {
		seen := routedEnv(t, a)
		code, err := routed(t, "codex")
		if err != nil || code != a.code || len(*seen) != 1 {
			t.Errorf("%s: code %d err %v attempts %d", name, code, err, len(*seen))
		}
	}
}

// The last agent's exit code is the run's.
func TestRoutedRunEndsWithTheLastAgentsCode(t *testing.T) {
	seen := routedEnv(t, attempt{1, ptr(false)}, attempt{1, ptr(false)}, attempt{7, ptr(false)})
	code, err := routed(t, "codex", "gemini")
	if err != nil || code != 7 || len(*seen) != 3 {
		t.Fatalf("code %d err %v attempts %d", code, err, len(*seen))
	}
}

func TestRoutedRunRefusals(t *testing.T) {
	routedEnv(t)
	d, _ := agents.LookupInteractive("claude")
	if _, err := routedRun(context.Background(), &runFlags{project: t.TempDir(), fallback: []string{"codex"}, detach: true}, d, []string{"-p", "x"}); err == nil {
		t.Error("a detached run with a fallback was accepted; nothing would watch it fail")
	}
	if _, err := routedRun(context.Background(), &runFlags{project: t.TempDir(), fallback: []string{"nope"}}, d, []string{"-p", "x"}); err == nil {
		t.Error("an unknown fallback was accepted")
	}
	// The prompt is needed only once a fallback runs, and then it must exist.
	routedEnv(t, attempt{1, ptr(false)})
	if _, err := routedRun(context.Background(), &runFlags{project: t.TempDir(), fallback: []string{"codex"}}, d, []string{"--verbose"}); err == nil || !strings.Contains(err.Error(), "flag rather than a prompt") {
		t.Errorf("an unrecoverable prompt: %v", err)
	}
}

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

	msgs := readTranscript(ctx, c, sb.ID, "claude")
	if len(msgs) != 1 || msgs[0].Text != "this session" {
		t.Fatalf("got %+v", msgs)
	}
	if readTranscript(ctx, c, sb.ID, "codex") != nil {
		t.Error("an agent whose format is unverified had its transcript read")
	}
}
