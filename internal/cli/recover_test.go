package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
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
	want := map[string]bool{"list": false, "show": false, "restore": false, "fetch": false, "repair": false, "prune": false}
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

// A snapshot id is resolved against the bucket the same way a session reference
// is resolved against a container listing: an unambiguous prefix is enough, and
// ambiguity refuses and names the candidates. Guessing here restores the wrong
// work, which is a cost nobody discovers until they look at the diff.
func TestFetchResolvesAPrefixAndRefusesAnAmbiguousOne(t *testing.T) {
	found := []rescue.Session{
		{ID: "20260901-120000-aaaa"},
		{ID: "20260901-120000-bbbb"},
		{ID: "20260902-090000-cccc"},
	}

	got, err := pickRemote(found, "20260902")
	if err != nil {
		t.Fatalf("an unambiguous prefix was refused: %v", err)
	}
	if got.ID != "20260902-090000-cccc" {
		t.Errorf("resolved to %q", got.ID)
	}

	// An exact id wins over being a prefix of nothing else, and never has to be
	// disambiguated against itself.
	if got, err := pickRemote(found, "20260901-120000-aaaa"); err != nil || got.ID != "20260901-120000-aaaa" {
		t.Errorf("exact id resolved to %q (%v)", got.ID, err)
	}

	_, err = pickRemote(found, "20260901")
	if err == nil {
		t.Fatal("an ambiguous prefix resolved to one snapshot")
	}
	for _, want := range []string{"20260901-120000-aaaa", "20260901-120000-bbbb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name candidate %s: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "20260902-090000-cccc") {
		t.Errorf("the refusal names a snapshot that does not match: %v", err)
	}

	if _, err := pickRemote(found, "20261231"); err == nil {
		t.Error("a prefix matching nothing resolved")
	}
}

// Mirroring is off until a bucket is configured, so the answer to "fetch"
// without one is where to configure it — and it must come from the user's own
// config, never a project .sandbox.yaml, which is refused snapshot.s3 precisely
// because it names a network destination and the credential to reach it.
func TestFetchWithNoBucketNamesWhereToConfigureOne(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := initRepo(t)

	_, err := snapshotBucket(repo)
	if err == nil {
		t.Fatal("a repository with no bucket configured resolved one")
	}
	if !strings.Contains(err.Error(), "snapshot.s3.bucket") {
		t.Errorf("the refusal does not name the setting: %v", err)
	}
	if !strings.Contains(err.Error(), config.UserConfigPath()) {
		t.Errorf("the refusal does not name the file to set it in: %v", err)
	}
}
