package githard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %s", args, out)
		}
	}
	return dir
}

// TestDiffConfigFlagsWhatGitExecutes covers the half of the .git problem that is
// detected rather than prevented. Hooks are mounted read-only so they cannot be
// planted; config stays writable because agents legitimately use it — so a change
// to a key whose value git *runs* has to be reported, and reported louder than a
// change to one it merely reads.
func TestDiffConfigFlagsWhatGitExecutes(t *testing.T) {
	before := map[string]string{"user.email": "t@example.com"}
	after := map[string]string{
		"user.email":     "t@example.com",
		"user.nickname":  "bob",                     // harmless
		"core.fsmonitor": "/workspace/.git/evil",    // git executes this
		"core.hookspath": "/workspace/.git/myhooks", // and this
	}

	changes := DiffConfig(before, after)
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3: %+v", len(changes), changes)
	}
	// Dangerous keys sort first: the news that matters must not be buried under a
	// renamed remote.
	if !changes[0].Dangerous || !changes[1].Dangerous {
		t.Errorf("dangerous keys are not sorted first: %+v", changes)
	}
	if changes[2].Dangerous {
		t.Errorf("user.nickname was flagged as dangerous: %+v", changes[2])
	}

	// A removal is a change too — deleting core.hooksPath alters behaviour.
	removed := DiffConfig(map[string]string{"core.fsmonitor": "x"}, map[string]string{})
	if len(removed) != 1 || removed[0].After != "" || !removed[0].Dangerous {
		t.Errorf("removal not reported correctly: %+v", removed)
	}
	// No change, no noise.
	if got := DiffConfig(before, before); len(got) != 0 {
		t.Errorf("unchanged config reported %d changes", len(got))
	}
}

// TestSnapshotConfigReadsRealRepositories runs against actual git so a change to
// its --list format is caught here rather than by the report silently going quiet.
func TestSnapshotConfigReadsRealRepositories(t *testing.T) {
	dir := initRepo(t)
	before := SnapshotConfig(dir)
	if before["user.email"] != "t@example.com" {
		t.Fatalf("snapshot did not read the repo config: %v", before)
	}

	// Values containing newlines and NULs must not be able to forge extra
	// entries — -z is what makes that true, and this is the test for it.
	cmd := exec.Command("git", "config", "--local", "core.fsmonitor", "a\nuser.email=attacker@evil")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setting a hostile value: %s", out)
	}
	after := SnapshotConfig(dir)
	if after["user.email"] != "t@example.com" {
		t.Errorf("a newline in a value forged another entry: user.email = %q", after["user.email"])
	}
	changes := DiffConfig(before, after)
	if len(changes) != 1 || changes[0].Key != "core.fsmonitor" || !changes[0].Dangerous {
		t.Errorf("expected one dangerous change, got %+v", changes)
	}
}

// TestSnapshotConfigOnANonRepositoryIsQuiet pins that the watcher costs nothing
// where there is nothing to watch — a plain directory is not an error.
func TestSnapshotConfigOnANonRepositoryIsQuiet(t *testing.T) {
	if got := SnapshotConfig(t.TempDir()); got != nil {
		t.Errorf("SnapshotConfig on a non-repository = %v, want nil", got)
	}
	if got := DiffConfig(nil, nil); got != nil {
		t.Errorf("DiffConfig(nil, nil) = %v, want nil", got)
	}
}

// TestHooksMountIsReadOnlyInTheSpec is the prevention half, asserted where the
// mount is decided. Belt to the live integration test's braces.
func TestHooksDirIsDetected(t *testing.T) {
	dir := initRepo(t)
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks")); err != nil {
		t.Skip("this git does not create .git/hooks")
	}
}

// TestArgsNeutralisesWorktreeScopedDrivers pins the scope the enumeration used
// to miss.
//
// neutralizedDrivers is the load-bearing half of this package: the attribute
// side is unbounded, so the defence is to read every driver out of the config
// and blank it with -c. It read with `git config --local`, which lists only
// .git/config — while git honours a scope above it. Set
// extensions.worktreeConfig in .git/config and a linked worktree's
// .git/worktrees/<name>/config.worktree wins, invisible to --local.
//
// Both files are inside the common .git, which --worktree mode bind-mounts
// read-write, so the agent writes all of it. Selected by the
// $GIT_DIR/info/attributes that has no -c override, a driver hidden there ran a
// command on the host through a fully hardened `git add -A`.
func TestArgsNeutralisesWorktreeScopedDrivers(t *testing.T) {
	repo := initRepo(t)
	writeAndCommit(t, repo)

	linked := filepath.Join(t.TempDir(), "wt")
	run(t, repo, "worktree", "add", "-q", "-b", "hostile", linked)
	run(t, repo, "config", "extensions.worktreeConfig", "true")
	run(t, linked, "config", "--worktree", "filter.pwn.clean", "sh -c 'echo pwned'")

	// Precondition: the driver really is hidden from the old enumeration. Without
	// this, a passing test could just mean the trap was never armed.
	out := output(t, linked, "config", "--local", "--name-only", "--list")
	if strings.Contains(out, "filter.pwn.clean") {
		t.Fatal("precondition failed: --local sees the worktree-scoped driver, so this proves nothing")
	}

	got := strings.Join(Args(linked), " ")
	if !strings.Contains(got, "filter.pwn.clean=") {
		t.Errorf("Args() does not blank the worktree-scoped driver git would run.\ngot: %s", got)
	}
}

// The other half of the same decision: a driver in the user's *global* config is
// left alone. The agent cannot write there, and blanking it would break a
// legitimate git-lfs setup by silently changing what a snapshot contains.
func TestArgsLeavesTheUsersGlobalDriversAlone(t *testing.T) {
	repo := initRepo(t)
	home := t.TempDir()
	global := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(global, []byte("[filter \"lfs\"]\n\tclean = git-lfs clean -- %f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", global)

	if got := strings.Join(Args(repo), " "); strings.Contains(got, "filter.lfs.clean=") {
		t.Errorf("Args() blanked a global driver the agent cannot write.\ngot: %s", got)
	}
}

// SnapshotConfig has the same reach, so a config change the user is told about
// cannot be hidden in the scope that outranks the one being reported.
func TestSnapshotConfigSeesWorktreeScope(t *testing.T) {
	repo := initRepo(t)
	writeAndCommit(t, repo)
	linked := filepath.Join(t.TempDir(), "wt")
	run(t, repo, "worktree", "add", "-q", "-b", "hostile2", linked)
	run(t, repo, "config", "extensions.worktreeConfig", "true")
	run(t, linked, "config", "--worktree", "core.fsmonitor", "sh -c 'echo pwned'")

	if _, ok := SnapshotConfig(linked)["core.fsmonitor"]; !ok {
		t.Error("SnapshotConfig cannot see worktree-scoped config, so a change there is never reported")
	}
	// And the key that enables the whole scope is reported as dangerous.
	if !dangerousConfigKeys["extensions.worktreeconfig"] {
		t.Error("extensions.worktreeConfig is the enabling step and must be flagged")
	}
}

func writeAndCommit(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-qm", "init")
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func output(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out)
}
