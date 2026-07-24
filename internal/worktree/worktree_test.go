package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeBranch(t *testing.T) {
	cases := map[string]string{
		"main":            "main",
		"feature/login":   "feature-login",
		"user/fix.bug_v2": "user-fix.bug_v2",
		"///weird//":      "weird",
		"a  b":            "a-b",
	}
	for in, want := range cases {
		if got := sanitizeBranch(in); got != want {
			t.Errorf("sanitizeBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorktreePath_StableAndNamespaced(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p1 := worktreePath("/repos/alpha", "feature/x")
	// Deterministic.
	if p1 != worktreePath("/repos/alpha", "feature/x") {
		t.Error("worktreePath not deterministic")
	}
	// Branch is sanitized into the final segment.
	if filepath.Base(p1) != "feature-x" {
		t.Errorf("leaf = %q, want feature-x", filepath.Base(p1))
	}
	// Same-named repos at different paths get different bases (hash namespacing).
	if worktreeBase("/repos/alpha") == worktreeBase("/other/alpha") {
		t.Error("expected different bases for same-named repos at different paths")
	}
}

func TestResolveAndList_RealGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Build a throwaway repo with one commit.
	repo := t.TempDir()
	runOrSkip(t, git, repo, "init", "-q")
	runOrSkip(t, git, repo, "config", "user.email", "t@example.com")
	runOrSkip(t, git, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, repo, "add", ".")
	runOrSkip(t, git, repo, "commit", "-qm", "init")

	// New branch: created from HEAD.
	info, err := Resolve(repo, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Created {
		t.Error("expected Created=true for a new worktree")
	}
	if !isDir(info.Path) {
		t.Errorf("worktree dir not created: %s", info.Path)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "README")); err != nil {
		t.Errorf("worktree missing repo content: %v", err)
	}

	// Idempotent reuse.
	again, err := Resolve(repo, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	if again.Created {
		t.Error("expected reuse (Created=false) on the second Resolve")
	}
	if again.Path != info.Path {
		t.Errorf("reuse path %q != %q", again.Path, info.Path)
	}

	// List shows it.
	infos, err := List(repo)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, wt := range infos {
		if wt.Branch == "feature/x" {
			found = true
		}
	}
	if !found {
		t.Errorf("List missing feature/x: %+v", infos)
	}

	// Remove it.
	if err := Remove(repo, "feature/x", false); err != nil {
		t.Fatal(err)
	}
	if isDir(info.Path) {
		t.Errorf("worktree dir still present after Remove: %s", info.Path)
	}
}

func TestResolve_NotAGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if _, err := Resolve(dir, "x"); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected a not-a-git-repo error, got %v", err)
	}
}

// Branch backs the branch label on the metrics gauge: a name for a normal
// checkout, a commit id when HEAD is detached, and nothing at all outside a repo
// (the sandbox does not require git).
func TestBranch(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runOrSkip(t, git, repo, "init", "-q", "-b", "main")
	runOrSkip(t, git, repo, "config", "user.email", "t@example.com")
	runOrSkip(t, git, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, repo, "add", ".")
	runOrSkip(t, git, repo, "commit", "-qm", "init")

	if got := Branch(repo); got != "main" {
		t.Errorf("Branch = %q, want %q", got, "main")
	}

	// Detached HEAD: the short commit id stands in for the missing branch name.
	runOrSkip(t, git, repo, "checkout", "-q", "--detach")
	got := Branch(repo)
	if got == "" || got == "HEAD" {
		t.Errorf("detached Branch = %q, want a short commit id", got)
	}

	if got := Branch(t.TempDir()); got != "" {
		t.Errorf("Branch outside a repo = %q, want \"\"", got)
	}
}

func runOrSkip(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git %v failed (%v): %s", args, err, out)
	}
}

// GitCommonDir must report nothing for an ordinary repository (its .git is a
// directory, already inside the mounted workspace) and the parent repo's .git
// for a worktree (whose .git is a pointer file to a path outside it).
func TestGitCommonDir(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	repo := t.TempDir()
	runOrSkip(t, git, repo, "init", "-q", ".")
	runOrSkip(t, git, repo, "config", "user.email", "t@example.com")
	runOrSkip(t, git, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, repo, "add", "-A")
	runOrSkip(t, git, repo, "commit", "-qm", "init")

	// A normal checkout needs no extra mount.
	if p, ok := GitCommonDir(repo); ok {
		t.Errorf("GitCommonDir(main repo) = %q, true; want ok=false", p)
	}

	// A worktree resolves to the parent repo's .git.
	info, err := Resolve(repo, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := GitCommonDir(info.Path)
	if !ok {
		t.Fatalf("GitCommonDir(worktree) returned ok=false; want the parent .git")
	}
	want, err := filepath.EvalSymlinks(filepath.Join(repo, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved, _ := filepath.EvalSymlinks(got); resolved != want {
		t.Errorf("GitCommonDir(worktree) = %q, want %q", resolved, want)
	}

	// Not a repository at all.
	if p, ok := GitCommonDir(t.TempDir()); ok {
		t.Errorf("GitCommonDir(non-repo) = %q, true; want ok=false", p)
	}
}

// Remove must refuse to destroy uncommitted work unless --force is given.
func TestRemove_RefusesDirtyWorktreeWithoutForce(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	repo := t.TempDir()
	runOrSkip(t, git, repo, "init", "-q", ".")
	runOrSkip(t, git, repo, "config", "user.email", "t@example.com")
	runOrSkip(t, git, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, repo, "add", "-A")
	runOrSkip(t, git, repo, "commit", "-qm", "init")

	info, err := Resolve(repo, "dirty")
	if err != nil {
		t.Fatal(err)
	}
	// Untracked file: exists only here, so removal must be refused.
	if err := os.WriteFile(filepath.Join(info.Path, "scratch.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = Remove(repo, "dirty", false)
	if err == nil {
		t.Fatal("Remove without --force deleted a dirty worktree; want refusal")
	}
	if !strings.Contains(err.Error(), "uncommitted work") {
		t.Errorf("error should explain the refusal, got: %v", err)
	}
	if !isDir(info.Path) {
		t.Error("worktree was removed despite the error")
	}

	if err := Remove(repo, "dirty", true); err != nil {
		t.Fatalf("Remove with --force: %v", err)
	}
	if isDir(info.Path) {
		t.Error("worktree dir still present after forced Remove")
	}
}

// An agent that runs `git checkout -b other` inside its worktree leaves the
// directory named after the *original* branch, so a branch name no longer maps
// to a path by string manipulation. Path/Remove/Resolve must ask git which
// worktree holds the branch instead of guessing from the name.
func TestBranchSwitchedInsideWorktree(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	repo := t.TempDir()
	runOrSkip(t, git, repo, "init", "-q", ".")
	runOrSkip(t, git, repo, "config", "user.email", "t@example.com")
	runOrSkip(t, git, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, repo, "add", "-A")
	runOrSkip(t, git, repo, "commit", "-qm", "init")

	info, err := Resolve(repo, "feature/other-adapter")
	if err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, info.Path, "checkout", "-q", "-b", "adapters-continued")

	// The directory keeps its old name; only git knows the new branch lives there.
	path, exists, err := Path(repo, "adapters-continued")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || path != info.Path {
		t.Fatalf("Path = %q, exists=%v; want %q, true", path, exists, info.Path)
	}

	// Resolve must reuse that worktree rather than try to add a second one for a
	// branch git already has checked out.
	again, err := Resolve(repo, "adapters-continued")
	if err != nil {
		t.Fatalf("Resolve on the renamed branch: %v", err)
	}
	if again.Path != info.Path || again.Created {
		t.Errorf("Resolve = %+v, want reuse of %s", again, info.Path)
	}

	if err := Remove(repo, "adapters-continued", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if isDir(info.Path) {
		t.Error("worktree dir still present after Remove")
	}
}

// Removing a branch that has no worktree is sandbox-cli's error to explain, not
// a raw "is not a working tree" from git about a path the user never typed.
func TestRemove_UnknownBranch(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	repo := t.TempDir()
	runOrSkip(t, git, repo, "init", "-q", ".")

	err = Remove(repo, "never-existed", false)
	if err == nil {
		t.Fatal("Remove of an unknown branch succeeded")
	}
	if !strings.Contains(err.Error(), "no sandbox worktree") {
		t.Errorf("error should say there is no worktree, got: %v", err)
	}
}

func TestPathAndDirty(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	repo := t.TempDir()
	runOrSkip(t, git, repo, "init", "-q", ".")
	runOrSkip(t, git, repo, "config", "user.email", "t@example.com")
	runOrSkip(t, git, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, repo, "add", "-A")
	runOrSkip(t, git, repo, "commit", "-qm", "init")

	// Path reports the location before the worktree exists, but exists=false.
	p, exists, err := Path(repo, "wip")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Errorf("Path reports exists=true before creation: %s", p)
	}
	// Nothing to warn about when there is no worktree.
	if got := Dirty(repo, "wip", 10); got != nil {
		t.Errorf("Dirty on a missing worktree = %v, want nil", got)
	}

	info, err := Resolve(repo, "wip")
	if err != nil {
		t.Fatal(err)
	}
	p, exists, err = Path(repo, "wip")
	if err != nil {
		t.Fatal(err)
	}
	if !exists || p != info.Path {
		t.Errorf("Path = (%q, %v), want (%q, true)", p, exists, info.Path)
	}
	// A clean worktree must not warn.
	if got := Dirty(repo, "wip", 10); got != nil {
		t.Errorf("Dirty on a clean worktree = %v, want nil", got)
	}

	// Modified and untracked files are both reported.
	if err := os.WriteFile(filepath.Join(info.Path, "f.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Dirty(repo, "wip", 10)
	if len(got) != 2 {
		t.Fatalf("Dirty = %v, want 2 entries", got)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "f.txt") || !strings.Contains(joined, "new.txt") {
		t.Errorf("Dirty = %v, want both f.txt and new.txt", got)
	}

	// limit caps the result so the warning stays short.
	if got := Dirty(repo, "wip", 1); len(got) != 1 {
		t.Errorf("Dirty with limit 1 = %v, want 1 entry", got)
	}
}

// Dirty must report renames and paths needing quoting correctly. Plain
// --porcelain renders these as "R  old -> new" and "\"weird name.txt\"", which a
// naive line[3:] parse would surface to the user verbatim.
func TestDirty_RenamesAndAwkwardPaths(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	repo := t.TempDir()
	runOrSkip(t, git, repo, "init", "-q", ".")
	runOrSkip(t, git, repo, "config", "user.email", "t@example.com")
	runOrSkip(t, git, repo, "config", "user.name", "t")
	for _, n := range []string{"old.txt", "keep.txt"} {
		if err := os.WriteFile(filepath.Join(repo, n), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runOrSkip(t, git, repo, "add", "-A")
	runOrSkip(t, git, repo, "commit", "-qm", "init")

	info, err := Resolve(repo, "awkward")
	if err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, info.Path, "mv", "old.txt", "new.txt")
	if err := os.WriteFile(filepath.Join(info.Path, "weird name.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Dirty(repo, "awkward", 0)
	want := map[string]bool{"new.txt": true, "weird name.txt": true}
	if len(got) != len(want) {
		t.Fatalf("Dirty = %v, want %d entries (%v)", got, len(want), want)
	}
	for _, f := range got {
		if !want[f] {
			t.Errorf("unexpected entry %q in %v; renames should report the destination "+
				"and paths must not be quoted", f, got)
		}
	}
}
