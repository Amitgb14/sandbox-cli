package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// --dry-run must stay a pure question: it prints the docker command it *would*
// run and touches nothing. Snapshotting is the first thing on the run path that
// writes outside the process, so it is the first thing that could break that.
func TestDryRunTakesNoSnapshotAndRecordsNoSession(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	repo := initRepo(t)

	out := renderDryRun(t, newClaudeCmd(), []string{"--project", repo})
	if !strings.Contains(out, "docker") {
		t.Fatalf("dry run printed no docker command:\n%s", out)
	}
	if entries, err := os.ReadDir(filepath.Join(cfgHome, "sandbox", "rescue")); err == nil && len(entries) > 0 {
		t.Errorf("--dry-run recorded %d rescue session bucket(s)", len(entries))
	}
	if refs := gitOut(t, repo, "for-each-ref", "--format=%(refname)", "refs/sandbox"); refs != "" {
		t.Errorf("--dry-run wrote snapshot refs:\n%s", refs)
	}
}

// The safety net is on unless the user says otherwise: a default that has to be
// switched on is not a safety net.
func TestSnapshotsAreOnByDefault(t *testing.T) {
	cfg := config.Default()
	if !cfg.Snapshot.IsEnabled() {
		t.Error("snapshots are off in the built-in defaults")
	}
	if got := cfg.Snapshot.EveryDuration(); got != config.DefaultSnapshotInterval {
		t.Errorf("default interval is %s, want %s", got, config.DefaultSnapshotInterval)
	}
	if got := cfg.Snapshot.RetentionDuration(); got != config.DefaultSnapshotRetention {
		t.Errorf("default retention is %s, want %s", got, config.DefaultSnapshotRetention)
	}
}

// recover is a host-side utility like stats: it must not need Docker, a config
// file, or a running sandbox to answer.
func TestRecoverCommandTree(t *testing.T) {
	cmd := newRecoverCmd()
	want := map[string]bool{"list": false, "show": false, "restore": false, "repair": false, "prune": false}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected recover subcommand %q", name)
			continue
		}
		want[name] = true
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing recover subcommand %q", name)
		}
	}
	// Restoring must default to the mode that cannot destroy anything, so the
	// destructive one has to be an explicit flag.
	if cmd, _, err := cmd.Find([]string{"restore"}); err != nil {
		t.Fatal(err)
	} else if cmd.Flags().Lookup("into-worktree") == nil {
		t.Error("restore has no --into-worktree flag, so its default may not be the safe one")
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
