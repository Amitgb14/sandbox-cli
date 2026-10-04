package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// routedEnv points the user config at a temp dir whose provider overrides
// decide the probes without the network: "" is not probed, and 127.0.0.1:1
// refuses the connection, which is a provider that is down. It scripts each
// attempt's exit code.
func routedEnv(t *testing.T, providers string, codes ...int) *[]runSpec {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	os.MkdirAll(filepath.Join(cfg, "sandbox"), 0o700)
	os.WriteFile(filepath.Join(cfg, "sandbox", "config.yaml"), []byte(providers), 0o600)
	var seen []runSpec
	old := execute
	t.Cleanup(func() { execute = old })
	execute = func(_ context.Context, _ *runFlags, rs runSpec) (int, error) {
		seen = append(seen, rs)
		return codes[len(seen)-1], nil
	}
	return &seen
}

const allUp = "providers:\n  claude: \"\"\n  codex: \"\"\n  gemini: \"\"\n"

func routed(t *testing.T, fallbacks ...string) (int, error) {
	t.Helper()
	d, _ := agents.LookupInteractive("claude")
	rf := &runFlags{project: t.TempDir(), fallback: fallbacks}
	return routedRun(context.Background(), rf, d, []string{"-p", "fix the bug"})
}

// A primary whose provider is down is skipped before a sandbox is made for it,
// and the next agent gets the task in its own spelling.
func TestRoutedRunSkipsAnAgentWhoseProviderIsDown(t *testing.T) {
	seen := routedEnv(t, "providers:\n  claude: \"127.0.0.1:1\"\n  codex: \"\"\n", 0)
	code, err := routed(t, "codex")
	if err != nil || code != 0 || len(*seen) != 1 {
		t.Fatalf("code %d err %v attempts %d", code, err, len(*seen))
	}
	run := (*seen)[0]
	if run.agent.Name != "codex" || run.argv[len(run.argv)-1] != "fix the bug" {
		t.Fatalf("fallback: agent %v argv %q", run.agent.Name, run.argv)
	}
	if l := run.labels; l["route.id"] == "" || l["route.attempt"] != "2" || l["route.from"] != "claude" || l["route.reason"] == "" {
		t.Errorf("route labels: %v", l)
	}
}

// A run that started is never retried with another agent, whatever its exit:
// with no repository there is no telling whether it did work, and a retry
// would put a second agent on top of the first one's.
func TestRoutedRunDoesNotRetryARunThatStarted(t *testing.T) {
	for _, code := range []int{0, 1, 7} {
		seen := routedEnv(t, allUp, code)
		got, err := routed(t, "codex", "gemini")
		if err != nil || got != code || len(*seen) != 1 || (*seen)[0].agent.Name != "claude" {
			t.Errorf("exit %d: code %d err %v attempts %d", code, got, err, len(*seen))
		}
	}
}

// The primary runs exactly what was typed, labelled as the first attempt.
func TestRoutedRunPrimaryRunsWhatWasTyped(t *testing.T) {
	seen := routedEnv(t, allUp, 0)
	if _, err := routed(t, "codex"); err != nil {
		t.Fatal(err)
	}
	first := (*seen)[0]
	if a := first.argv; a[len(a)-1] != "fix the bug" || a[len(a)-2] != "-p" {
		t.Errorf("primary argv %q", a)
	}
	if first.labels["route.attempt"] != "1" || first.labels["route.from"] != "" {
		t.Errorf("primary labels %v", first.labels)
	}
}

// Every agent down is an error naming each, and nothing runs.
func TestRoutedRunWithEveryProviderDown(t *testing.T) {
	seen := routedEnv(t, "providers:\n  claude: \"127.0.0.1:1\"\n  codex: \"127.0.0.1:1\"\n")
	if _, err := routed(t, "codex"); err == nil || !strings.Contains(err.Error(), "no agent in the chain") {
		t.Errorf("all down: %v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("%d attempts ran", len(*seen))
	}
}

func TestRoutedRunRefusals(t *testing.T) {
	routedEnv(t, allUp)
	d, _ := agents.LookupInteractive("claude")
	if _, err := routedRun(context.Background(), &runFlags{project: t.TempDir(), fallback: []string{"codex"}, detach: true}, d, []string{"-p", "x"}); err == nil {
		t.Error("a detached run with a fallback was accepted; nothing would watch it fail")
	}
	if _, err := routedRun(context.Background(), &runFlags{project: t.TempDir(), fallback: []string{"nope"}}, d, []string{"-p", "x"}); err == nil {
		t.Error("an unknown fallback was accepted")
	}
	// The prompt is needed only once a fallback runs, and then it must exist.
	routedEnv(t, "providers:\n  claude: \"127.0.0.1:1\"\n  codex: \"\"\n", 0)
	if _, err := routedRun(context.Background(), &runFlags{project: t.TempDir(), fallback: []string{"codex"}}, d, []string{"--verbose"}); err == nil || !strings.Contains(err.Error(), "flag rather than a prompt") {
		t.Errorf("an unrecoverable prompt: %v", err)
	}
}
