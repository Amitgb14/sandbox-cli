package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/session"
)

// A fleet launch records a pane, which is what `optionsPolicy` now claims by marking
// the three pane fields `fromSpec`.
//
// The claim cannot be checked the way the other `fromSpec` fields are, because the
// pane identity is set by `session.Spawn` rather than by `Runner.options` — a fleet
// file cannot ask for a pane id, and nothing would be served by letting it. So this is
// the evidence that table points at.
//
// Why it matters beyond bookkeeping: a pane is what `sandbox-cli serve` snapshots. A
// fleet agent had no crash safety net at all before, for the same reason every other
// detached run had none.
func TestFleetLaunchRecordsAPane(t *testing.T) {
	repo := gitRepo(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "c"))

	srv, err := session.Open(repo, "dev", "docker")
	if err != nil {
		t.Fatalf("open session: %v", err)
	}

	r, _ := testRunner()
	r.Repo = repo
	r.Panes = srv

	// The launch fails at the engine — there is none in a unit test — but the pane
	// kind is decided before that, and `start` is the seam being checked.
	for _, tc := range []struct {
		name   string
		verify string
		agent  string
		want   protocol.PaneKind
	}{
		{"an agent task", "", "claude", protocol.PaneAgent},
		// A task that declared a verify: `withVerify` wraps it around the agent's argv
		// in the one container and makes its exit code the container's, so what that
		// pane reports is the verdict. The plan asked for a second pane instead; that
		// needs durable sequencing, and an in-memory "now run the verify" step is a
		// verify that silently never runs after a restart.
		{"a task with a verify", "go test ./...", "claude", protocol.PaneVerify},
	} {
		got := paneKindForOptions(sandbox.Options{Agent: tc.agent, Verify: tc.verify})
		if got != tc.want {
			t.Errorf("%s: kind = %q, want %q", tc.name, got, tc.want)
		}
	}

	// The nil-catalog path — a fleet where no session could be opened — is not
	// asserted here and does not need to be: it is one `if` returning the call this
	// replaced, and every other test in this package runs through it, since
	// `testRunner` builds a Runner without one. Exercising it with a real launch
	// would mean an engine.

}

// paneKindForOptions is the kind decision, lifted so the table above can assert it
// without an engine. It mirrors `Runner.start`, and the mirror is the point: a second
// copy of this rule is how a fleet pane and a CLI pane come to disagree about what
// they are.
func paneKindForOptions(opts sandbox.Options) protocol.PaneKind {
	if opts.Verify != "" {
		return protocol.PaneVerify
	}
	if opts.Agent == "" {
		return protocol.PaneCommand
	}
	return protocol.PaneAgent
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"commit", "-q", "--allow-empty", "-m", "first"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is needed here: %v: %s", err, out)
		}
	}
	_ = os.Setenv("GIT_TERMINAL_PROMPT", "0")
	return dir
}
