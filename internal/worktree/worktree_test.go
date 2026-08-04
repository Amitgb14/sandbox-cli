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

// One branch must always name one path, whichever way the answer is reached.
// Resolve builds the path itself when creating a worktree but asks git for it
// afterwards, and git always reports a symlink-resolved path — so on a config
// directory reached through a symlink the two disagreed, and Resolve returned a
// different string the second time it was called for the same branch. macOS hits
// this on every run, because /var is a symlink to /private/var; this test forces
// the same condition everywhere so the platform without the symlink still
// catches a regression.
func TestPathsAreConsistentUnderASymlinkedConfigDir(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "cfg-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", link)

	repo := t.TempDir()
	runOrSkip(t, git, repo, "init", "-q")
	runOrSkip(t, git, repo, "config", "user.email", "t@example.com")
	runOrSkip(t, git, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, repo, "add", ".")
	runOrSkip(t, git, repo, "commit", "-qm", "init")

	created, err := Resolve(repo, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	reused, err := Resolve(repo, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	if created.Path != reused.Path {
		t.Errorf("same branch, two paths:\n  created: %s\n  reused:  %s", created.Path, reused.Path)
	}

	// Path and List have to agree with Resolve, or `worktree rm` and the
	// --worktree mount would be addressing a different string than the one the
	// user was shown.
	got, exists, err := Path(repo, "feature/x")
	if err != nil || !exists {
		t.Fatalf("Path = (%q, %v, %v)", got, exists, err)
	}
	if got != created.Path {
		t.Errorf("Path = %q, want %q (what Resolve reported)", got, created.Path)
	}
	infos, err := List(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("List returned %d worktrees, want 1: %+v", len(infos), infos)
	}
	if infos[0].Path != created.Path {
		t.Errorf("List path = %q, want %q", infos[0].Path, created.Path)
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

// TestRepoID_SharedByEveryWorktree pins the property the whole addressing scheme
// rests on: every branch of one repository reports one identity. A linked
// worktree's `rev-parse --show-toplevel` is its own directory, so an
// implementation that used it alone would hand each branch a different id — and
// then "show me every container for this repo" would return one row.
func TestRepoID_SharedByEveryWorktree(t *testing.T) {
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

	main, err := RepoID(repo)
	if err != nil {
		t.Fatal(err)
	}
	if main == "" {
		t.Fatal("empty repo id")
	}

	info, err := Resolve(repo, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	fromWorktree, err := RepoID(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fromWorktree != main {
		t.Errorf("RepoID(worktree) = %q, want the parent's %q", fromWorktree, main)
	}

	// It is a container-name and path segment, so it must survive being used as
	// one unchanged.
	if SanitizeName(main) != main {
		t.Errorf("repo id %q is not already a safe segment (%q)", main, SanitizeName(main))
	}

	// Not a repository: an error, not a fabricated identity.
	if id, err := RepoID(t.TempDir()); err == nil {
		t.Errorf("RepoID(non-repo) = %q, want an error", id)
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

// TestGitCommonDir_RejectsAForgedPointer covers the sharpest finding of the
// audit. The `.git` pointer file lives inside the workspace, which the sandbox
// mounts read-write, so the agent can rewrite it at any time — and the caller
// bind-mounts whatever this returns, read-write, at its own host path.
//
// The old check was isDir, and the fallback takes two directories up from the
// `gitdir:` string, so `gitdir: <home>/x/y` yielded the home directory and
// `gitdir: <home>` yielded the filesystem root. Both were mounted read-write on
// the user's next run, from one file the agent already had write access to.
func TestGitCommonDir_RejectsAForgedPointer(t *testing.T) {
	proj := t.TempDir()
	victim := filepath.Join(t.TempDir(), "sensitive")
	if err := os.MkdirAll(filepath.Join(victim, "x", "y"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{
		filepath.Join(victim, "x", "y"), // two up -> victim
		victim,                          // two up -> its parent
		"/etc/foo/bar",
	} {
		if err := os.WriteFile(filepath.Join(proj, ".git"), []byte("gitdir: "+target+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := GitCommonDir(proj); ok {
			t.Errorf("gitdir: %q was accepted and would be mounted rw at %q", target, got)
		}
	}
}

// TestGitCommonDir_AcceptsARealWorktree is the other half: the check must not be
// so strict that it breaks the case the mount exists for. Without the parent
// .git mounted, every git command inside the container fails with "not a git
// repository" and the agent can edit files but never commit them.
func TestGitCommonDir_AcceptsARealWorktree(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	runOrSkip(t, git, root, "init", "-q")
	runOrSkip(t, git, root, "config", "user.email", "t@example.com")
	runOrSkip(t, git, root, "config", "user.name", "t")
	runOrSkip(t, git, root, "commit", "-qm", "init", "--allow-empty")

	wt, err := Resolve(root, "feat")
	if err != nil {
		t.Fatalf("creating worktree: %v", err)
	}
	got, ok := GitCommonDir(wt.Path)
	if !ok {
		t.Fatal("a real linked worktree must resolve its parent .git")
	}
	if filepath.Base(got) != ".git" {
		t.Errorf("GitCommonDir = %q, want the parent repository's .git", got)
	}
	if _, err := os.Stat(filepath.Join(got, "HEAD")); err != nil {
		t.Errorf("resolved path is not a git directory: %v", err)
	}
}

// TestDirtyStripsTerminalControlSequences pins that a filename cannot act on the
// terminal that prints it. These paths are shown by the "you left work here"
// warning at the end of every --worktree run and on Ctrl-C, they come from the
// workspace so the agent names them, and `--porcelain -z` deliberately does NOT
// quote — so an ESC in a filename used to reach the user's terminal verbatim.
func TestDirtyStripsTerminalControlSequences(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	runOrSkip(t, git, root, "init", "-q")
	runOrSkip(t, git, root, "config", "user.email", "t@example.com")
	runOrSkip(t, git, root, "config", "user.name", "t")
	runOrSkip(t, git, root, "commit", "-qm", "init", "--allow-empty")

	wt, err := Resolve(root, "feat")
	if err != nil {
		t.Fatalf("creating worktree: %v", err)
	}
	// An untracked file whose *name* is a terminal command.
	hostile := "\x1b]0;PWNED\x07\x1b[31mevil.txt"
	if err := os.WriteFile(filepath.Join(wt.Path, hostile), []byte("x"), 0o644); err != nil {
		t.Skipf("filesystem rejected the name: %v", err)
	}

	files := Dirty(root, "feat", 10)
	if len(files) == 0 {
		t.Fatal("the dirty file was not reported at all")
	}
	for _, f := range files {
		for _, r := range f {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Errorf("Dirty returned %q, which still carries control character %q", f, r)
			}
		}
	}
}

// The directory name is not evidence of what is in it.
//
// Reported as #54, from a real repository: a worktree created for one branch,
// an agent that ran `git checkout -b` inside it, and the original branch then
// deleted. `lookup` correctly finds nothing, and the old fallback reused the
// name-derived directory without asking what it held — so a run launched for
// feature/enable-team-plan got a workspace sitting on feature/metering-hardening
// and a container labelled with the branch that was asked for. Every later
// command reads that label as fact.
func TestResolveRefusesADirectoryHoldingAnotherBranch(t *testing.T) {
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

	// The worktree is made for one branch, then moved to another from inside —
	// exactly what an agent does — and the original branch is deleted, so
	// lookup can no longer find it.
	info, err := Resolve(repo, "feature/enable-team-plan")
	if err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, info.Path, "checkout", "-q", "-b", "feature/metering-hardening")
	runOrSkip(t, git, repo, "branch", "-D", "feature/enable-team-plan")

	_, err = Resolve(repo, "feature/enable-team-plan")
	if err == nil {
		t.Fatal("Resolve reused a directory holding a different branch; the run would have " +
			"edited feature/metering-hardening while labelled feature/enable-team-plan")
	}
	// The refusal has to name both branches: which one is there decides whether
	// you switch it back or go work where your branch actually is.
	for _, want := range []string{"feature/metering-hardening", "feature/enable-team-plan"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %q, got: %v", want, err)
		}
	}
}

// And the reuse it exists for still works: same branch, same directory, no
// second worktree added for something git already has checked out.
func TestResolveStillReusesTheSameBranchsDirectory(t *testing.T) {
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

	first, err := Resolve(repo, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Resolve(repo, "feature/x")
	if err != nil {
		t.Fatalf("Resolve refused to reuse its own worktree: %v", err)
	}
	if second.Path != first.Path || second.Created {
		t.Errorf("Resolve = %+v, want reuse of %s", second, first.Path)
	}
}

// The third outcome: a directory git cannot read a branch out of. This is the
// shape a pruned admin dir leaves — `.git` points at a worktrees/<name> entry
// that no longer exists — and it is the arm most likely to fire in the wild,
// so it gets the same pinning as the other two.
func TestResolveRefusesADirectoryGitCannotRead(t *testing.T) {
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

	info, err := Resolve(repo, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	// Break it the way a prune does: the directory and its .git pointer survive,
	// the administrative entry in the parent repository does not.
	admin := filepath.Join(repo, ".git", "worktrees")
	if err := os.RemoveAll(admin); err != nil {
		t.Fatal(err)
	}
	if !isDir(info.Path) {
		t.Fatal("the worktree directory should still be there")
	}

	_, err = Resolve(repo, "feature/x")
	if err == nil {
		t.Fatal("Resolve reused a directory git can no longer read; the run would have got a broken .git")
	}
	for _, want := range []string{"cannot say which branch", "recover repair"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must mention %q, got: %v", want, err)
		}
	}
}

// The remedy a refusal offers has to be one you can actually run. In the case
// this was written for the requested branch has been *deleted* — that is why
// lookup found nothing — so a plain `git checkout <branch>` answers "pathspec
// did not match any file(s) known to git". The suggestion switches to -b when
// the branch is gone.
func TestRefusalSuggestsACheckoutThatWorks(t *testing.T) {
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

	info, err := Resolve(repo, "feature/gone")
	if err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, info.Path, "checkout", "-q", "-b", "moved-on")

	// Branch still present: an ordinary checkout is the right advice.
	if _, err := Resolve(repo, "feature/gone"); err == nil {
		t.Fatal("expected a refusal")
	} else if !strings.Contains(err.Error(), "checkout feature/gone") ||
		strings.Contains(err.Error(), "checkout -b feature/gone") {
		t.Errorf("with the branch present the advice should be a plain checkout, got: %v", err)
	}

	// Branch deleted — the case from the report. Now it has to be -b.
	runOrSkip(t, git, repo, "branch", "-D", "feature/gone")
	if _, err := Resolve(repo, "feature/gone"); err == nil {
		t.Fatal("expected a refusal")
	} else if !strings.Contains(err.Error(), "checkout -b feature/gone") {
		t.Errorf("with the branch deleted the advice must create it, got: %v", err)
	}
}

// Path and Resolve must give the same answer about a drifted directory.
//
// Resolve refuses it; Path used to report it as an existing worktree, and Path
// is the *plan* side — fleet's dry run reads WorktreeExists from it and land
// checks there is something to land from. A rehearsal that promises what the
// run declines is the one thing a rehearsal must never do, which is the same
// inconsistency NameHeldBy was added to close for container names.
func TestPathAndResolveAgreeAboutADriftedDirectory(t *testing.T) {
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

	info, err := Resolve(repo, "feature/planned")
	if err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, info.Path, "checkout", "-q", "-b", "moved-on")
	runOrSkip(t, git, repo, "branch", "-D", "feature/planned")

	_, exists, perr := Path(repo, "feature/planned")
	_, rerr := Resolve(repo, "feature/planned")

	if rerr == nil {
		t.Fatal("Resolve should refuse a drifted directory")
	}
	if perr == nil || exists {
		t.Fatalf("Path reported exists=%v err=%v; the dry run would promise a worktree the run refuses",
			exists, perr)
	}
	if !strings.Contains(perr.Error(), "moved-on") {
		t.Errorf("Path's error must name the branch actually there, got: %v", perr)
	}
}

// And CommitAll still refuses to commit into it — that is where getting this
// wrong costs the most, since `add -A` would put the work on the wrong branch.
func TestCommitAllRefusesADriftedWorktree(t *testing.T) {
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

	info, err := Resolve(repo, "feature/tocommit")
	if err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, info.Path, "checkout", "-q", "-b", "elsewhere")
	runOrSkip(t, git, repo, "branch", "-D", "feature/tocommit")
	if err := os.WriteFile(filepath.Join(info.Path, "new.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	committed, err := CommitAll(repo, "feature/tocommit", "msg")
	if err == nil || committed {
		t.Fatalf("CommitAll committed=%v err=%v; it must not commit onto the branch the agent moved to",
			committed, err)
	}
	if !strings.Contains(err.Error(), "elsewhere") {
		t.Errorf("the refusal must name the branch actually there, got: %v", err)
	}
}

// Git cannot hold `bugfix` and `bugfix/observability` at once — refs are paths,
// so one name is either a branch or a directory of branches. That is not ours to
// fix, but the message was: git's arrives after "Preparing worktree" has already
// printed, says `cannot lock ref … 'refs/heads/bugfix' exists`, and names
// neither the branch in the way nor anything to do about it (#55).
func TestResolveExplainsARefConflict(t *testing.T) {
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

	t.Run("a branch above the requested name", func(t *testing.T) {
		runOrSkip(t, git, repo, "branch", "bugfix")
		defer func() { _ = exec.Command(git, "-C", repo, "branch", "-D", "bugfix").Run() }()

		_, err := Resolve(repo, "bugfix/observability")
		if err == nil {
			t.Fatal("expected a refusal")
		}
		for _, want := range []string{`"bugfix"`, "cannot hold both", "git branch -m", "bugfix-observability"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal must mention %q, got: %v", want, err)
			}
		}
		// git's own wording must not be what the user reads.
		if strings.Contains(err.Error(), "cannot lock ref") || strings.Contains(err.Error(), "exit status") {
			t.Errorf("git's raw error leaked through: %v", err)
		}
	})

	t.Run("a branch below the requested name", func(t *testing.T) {
		runOrSkip(t, git, repo, "branch", "feature/a")
		defer func() { _ = exec.Command(git, "-C", repo, "branch", "-D", "feature/a").Run() }()

		_, err := Resolve(repo, "feature")
		if err == nil {
			t.Fatal("expected a refusal")
		}
		// The other direction: the name is already a namespace, so renaming the
		// other branch is not the move — the advice has to differ.
		if !strings.Contains(err.Error(), "already a namespace") {
			t.Errorf("refusal should say the name is a namespace, got: %v", err)
		}
	})
}

// And a name that merely shares a prefix is not a conflict: refs collide on path
// segments, so "feature-x" and "feature/y" coexist happily.
func TestRefConflictIgnoresMerePrefixes(t *testing.T) {
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
	runOrSkip(t, git, repo, "branch", "feature/y")

	if got := refConflict(repo, "feature-x"); got != "" {
		t.Errorf("refConflict = %q; a shared prefix is not a path collision", got)
	}
	if got := refConflict(repo, "feature/z"); got != "" {
		t.Errorf("refConflict = %q; siblings under one namespace do not collide", got)
	}
}

// A refusal must leave the machine as it found it.
//
// The ref-conflict check used to sit after MkdirAll, so declining to create a
// worktree still created three directories on the way to saying no. Harmless in
// effect, wrong in principle, and the comment above it claimed otherwise.
//
// Asserted by walking the whole config tree rather than stat-ing the path this
// test computes: worktreePath here yields /var/... while Resolve works from a
// symlink-resolved /private/var/... root, so both of the obvious probes report
// "clean" whether or not anything was created. That is the same /var vs
// /private/var trap this package documents for git worktree list.
func TestARefusalCreatesNothing(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)

	repo := t.TempDir()
	runOrSkip(t, git, repo, "init", "-q", ".")
	runOrSkip(t, git, repo, "config", "user.email", "t@example.com")
	runOrSkip(t, git, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runOrSkip(t, git, repo, "add", "-A")
	runOrSkip(t, git, repo, "commit", "-qm", "init")
	runOrSkip(t, git, repo, "branch", "bugfix")

	if _, err := Resolve(repo, "bugfix/observability"); err == nil {
		t.Fatal("expected a refusal")
	}

	var created []string
	_ = filepath.Walk(cfg, func(p string, fi os.FileInfo, e error) error {
		if e == nil && p != cfg {
			created = append(created, p[len(cfg):])
		}
		return nil
	})
	if len(created) != 0 {
		t.Errorf("a refused Resolve created %v", created)
	}
}
