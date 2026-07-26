package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// initRepo builds a throwaway git repository with one commit, or skips the test
// when git is unavailable or refuses to run.
func initRepo(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-qm", "init"},
	} {
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}
	return dir
}

// A linked worktree must be mounted at its own host path as well as at
// /workspace. The parent repo records every worktree by absolute path and treats
// a record whose path has vanished as a deleted worktree, so without this mount
// git inside the container reports each one as "prunable: gitdir file points to
// non-existent location". The parent .git is mounted read-write so the agent can
// commit, which means one `git worktree prune` — or the one `git gc` runs for
// itself — reaches out of the container and deletes the user's whole worktree
// registry on the host, orphaning even the worktree it is running in.
func TestWorktreeMountedAtItsHostPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := initRepo(t)
	t.Chdir(repo) // the scenario under test, and keeps this repo's own config out

	info, err := worktree.Resolve(repo, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	gitDir, ok := worktree.GitCommonDir(info.Path)
	if !ok {
		t.Fatal("GitCommonDir returned ok=false for a worktree")
	}

	_, opts, err := newSession(&runFlags{project: info.Path})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}

	// The parent .git, so git resolves at all.
	if want := gitDir + ":" + gitDir + ":rw"; !containsStr(opts.ExtraMounts, want) {
		t.Errorf("parent .git not mounted at %q: %#v", gitDir, opts.ExtraMounts)
	}
	// The worktree at its own host path, so its record does not read as stale.
	wt, err := filepath.Abs(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if want := wt + ":" + wt + ":rw"; !containsStr(opts.ExtraMounts, want) {
		t.Errorf("worktree not mounted at its host path %q: %#v", wt, opts.ExtraMounts)
	}
}

// An ordinary checkout needs neither mount: its .git is a directory already
// inside the workspace, and there is no worktree record anywhere to protect.
// Mounting a second copy of the project would be pure extra surface.
func TestNormalRepoGetsNoExtraGitMounts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := initRepo(t)
	t.Chdir(repo) // the scenario under test, and keeps this repo's own config out

	_, opts, err := newSession(&runFlags{project: repo})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	for _, m := range opts.ExtraMounts {
		if strings.HasPrefix(m, repo+":") {
			t.Errorf("normal repo got an extra mount of itself: %q", m)
		}
	}
}
