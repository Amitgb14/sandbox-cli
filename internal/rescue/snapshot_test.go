package rescue

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initRepo builds a throwaway git repository with one commit, or skips the test
// when git is unavailable or refuses to run. It also isolates the rescue
// directory, so no test can see another's sessions or the developer's own.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tracked.txt"), "hello\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "ignored/\n")
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-qm", "init"},
	} {
		if out, err := gitCmd(dir, args...).CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}
	return dir
}

func gitCmd(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	return cmd
}

// git runs git in dir and fails the test if it errors.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitCmd(dir, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// begin starts a Snapshotter for dir without the ticker, so tests drive it
// snapshot by snapshot.
func begin(t *testing.T, dir string) *Snapshotter {
	t.Helper()
	s := Begin(dir, "test", time.Minute, 14*24*time.Hour)
	if s == nil {
		t.Fatal("Begin returned nil for a git repository")
	}
	return s
}

// The whole safety argument for snapshotting a live repository is that it writes
// nothing the user can see. If a snapshot ever touched the index, HEAD, a branch
// or the working tree, it would be corrupting the very work it exists to
// protect — so pin all four, byte for byte.
func TestSnapshotLeavesTheRepositoryUntouched(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	writeFile(t, filepath.Join(repo, "untracked.txt"), "new\n")

	indexBefore := readFile(t, filepath.Join(repo, ".git", "index"))
	headBefore := git(t, repo, "rev-parse", "HEAD")
	statusBefore := git(t, repo, "status", "--porcelain")
	branchesBefore := git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads")

	s := begin(t, repo)
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}

	if got := readFile(t, filepath.Join(repo, ".git", "index")); got != indexBefore {
		t.Error("the repository index was modified by a snapshot")
	}
	if got := git(t, repo, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("HEAD moved: %s -> %s", headBefore, got)
	}
	if got := git(t, repo, "status", "--porcelain"); got != statusBefore {
		t.Errorf("working tree changed:\n%s\n->\n%s", statusBefore, got)
	}
	if got := git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"); got != branchesBefore {
		t.Errorf("branches changed:\n%s\n->\n%s", branchesBefore, got)
	}
	// The only new ref is ours, under refs/sandbox.
	for _, ref := range strings.Split(git(t, repo, "for-each-ref", "--format=%(refname)"), "\n") {
		if ref == "" || strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, RefPrefix) {
			continue
		}
		t.Errorf("snapshot created an unexpected ref: %s", ref)
	}
}

// A snapshot has to capture the work that exists nowhere else — uncommitted
// edits and untracked files — while leaving ignored paths alone, because
// snapshotting node_modules every two minutes would make the safety net cost
// more than it saves.
func TestSnapshotCapturesWorkAndSkipsIgnored(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	writeFile(t, filepath.Join(repo, "untracked.txt"), "new\n")
	writeFile(t, filepath.Join(repo, "ignored", "junk.txt"), "noise\n")

	s := begin(t, repo)
	commit, err := s.Once()
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if commit == "" {
		t.Fatal("no snapshot was taken despite changes")
	}

	files := git(t, repo, "ls-tree", "-r", "--name-only", commit)
	for _, want := range []string{"tracked.txt", "untracked.txt"} {
		if !strings.Contains(files, want) {
			t.Errorf("snapshot is missing %s:\n%s", want, files)
		}
	}
	if strings.Contains(files, "ignored/") {
		t.Errorf("snapshot captured a gitignored path:\n%s", files)
	}
	if got := git(t, repo, "show", commit+":tracked.txt"); got != "changed" {
		t.Errorf("snapshot has stale content for tracked.txt: %q", got)
	}
	if got := git(t, repo, "rev-parse", s.Session().Ref); got != commit {
		t.Errorf("ref %s points at %s, want %s", s.Session().Ref, got, commit)
	}
}

// An idle agent must cost nothing. Without the unchanged-tree check every
// interval would add a commit to the object store for no reason.
func TestSnapshotIsANoOpWhenNothingChanged(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")

	s := begin(t, repo)
	first, err := s.Once()
	if err != nil || first == "" {
		t.Fatalf("first snapshot: %q %v", first, err)
	}
	second, err := s.Once()
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if second != "" {
		t.Errorf("an unchanged workspace produced a second commit %s", second)
	}
	if got := git(t, repo, "rev-parse", s.Session().Ref); got != first {
		t.Errorf("ref moved without a change: %s -> %s", first, got)
	}
	if n := s.Session().Snapshots; n != 1 {
		t.Errorf("session recorded %d snapshots, want 1", n)
	}
}

// The failure this feature exists for: an agent commits, then throws its own
// commit away. `git reset --hard` leaves nothing on any branch, and the user
// never sees the reflog. Carrying HEAD as a snapshot parent is what keeps that
// commit — and the uncommitted work on top of it — reachable.
func TestSnapshotSurvivesAResetHard(t *testing.T) {
	repo := initRepo(t)
	s := begin(t, repo)

	writeFile(t, filepath.Join(repo, "feature.txt"), "the agent's work\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "agent work")
	agentCommit := git(t, repo, "rev-parse", "HEAD")

	writeFile(t, filepath.Join(repo, "wip.txt"), "not committed yet\n")
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}

	// The agent (or a botched rebase) wipes it out.
	git(t, repo, "reset", "-q", "--hard", "HEAD~1")
	if _, err := gitCmd(repo, "merge-base", "--is-ancestor", agentCommit, "HEAD").Output(); err == nil {
		t.Fatal("test setup: the agent commit is still on the branch")
	}

	res, err := Restore(repo, s.Session().ID, RestoreOptions{})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if out, err := gitCmd(repo, "merge-base", "--is-ancestor", agentCommit, res.Branch).Output(); err != nil {
		t.Fatalf("the agent's commit is not reachable from %s: %v %s", res.Branch, err, out)
	}
	if got := git(t, repo, "show", res.Branch+":wip.txt"); got != "not committed yet" {
		t.Errorf("uncommitted work was not recovered: %q", got)
	}
}

// Restoring must never be the thing that loses work: the default mode adds a
// branch and changes nothing else.
func TestRestoreOntoABranchChangesNothingElse(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	s := begin(t, repo)
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}

	headBefore := git(t, repo, "rev-parse", "HEAD")
	branchBefore := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	statusBefore := git(t, repo, "status", "--porcelain")

	res, err := Restore(repo, s.Session().ID, RestoreOptions{})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !strings.HasPrefix(res.Branch, RestoreBranchPrefix) {
		t.Errorf("restored branch %q is outside the %s namespace", res.Branch, RestoreBranchPrefix)
	}
	if got := git(t, repo, "rev-parse", res.Branch); got != s.Session().LastSnapshot {
		t.Errorf("branch points at %s, want the snapshot %s", got, s.Session().LastSnapshot)
	}
	if got := git(t, repo, "rev-parse", "HEAD"); got != headBefore {
		t.Error("restore moved HEAD")
	}
	if got := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != branchBefore {
		t.Error("restore switched branches")
	}
	if got := git(t, repo, "status", "--porcelain"); got != statusBefore {
		t.Error("restore changed the working tree")
	}

	// A second restore must not silently clobber the first.
	if _, err := Restore(repo, s.Session().ID, RestoreOptions{Branch: res.Branch}); err == nil {
		t.Error("restoring onto an existing branch was allowed")
	}
}

// Restoring into the working tree is the one mode that overwrites files, so a
// dirty tree has to stop it: after a crash those files may themselves be the
// newest copy of the user's work.
func TestRestoreIntoWorktreeRefusesADirtyTree(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "snapshot content\n")
	s := begin(t, repo)
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}

	err := func() error {
		_, err := Restore(repo, s.Session().ID, RestoreOptions{Mode: RestoreWorktree})
		return err
	}()
	if err == nil {
		t.Fatal("restoring into a dirty working tree was allowed")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("unhelpful refusal: %v", err)
	}

	// Clean it, and the same restore should now put the snapshot back in place.
	git(t, repo, "checkout", "--", "tracked.txt")
	if _, err := Restore(repo, s.Session().ID, RestoreOptions{Mode: RestoreWorktree}); err != nil {
		t.Fatalf("Restore into a clean tree: %v", err)
	}
	if got := readFile(t, filepath.Join(repo, "tracked.txt")); got != "snapshot content\n" {
		t.Errorf("working tree not restored: %q", got)
	}
}

// The patch form answers "what did that run change?", so it is rendered against
// the commit the run started from — not against whatever the repository happens
// to be on now.
func TestRestorePatchIsAgainstWhereTheRunStarted(t *testing.T) {
	repo := initRepo(t)
	s := begin(t, repo)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "the agent's edit\n")
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}
	// The repository moves on underneath: the patch must still describe the run.
	git(t, repo, "checkout", "--", "tracked.txt")
	writeFile(t, filepath.Join(repo, "unrelated.txt"), "later work\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "unrelated later commit")

	out := filepath.Join(t.TempDir(), "w.patch")
	if _, err := Restore(repo, s.Session().ID, RestoreOptions{Mode: RestorePatch, Out: out}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	patch := readFile(t, out)
	if !strings.Contains(patch, "the agent's edit") {
		t.Errorf("patch is missing the run's change:\n%s", patch)
	}
	if strings.Contains(patch, "unrelated.txt") {
		t.Errorf("patch includes work the run had nothing to do with:\n%s", patch)
	}
	// git apply must accept it — a patch that does not apply is not a recovery.
	git(t, repo, "apply", "--check", out)
}

// A run in a repository with no commits yet produces a snapshot with no parent,
// so there is no base to diff against. Every file in it is new.
func TestRestorePatchFromAnUnbornRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main", "."}, {"config", "user.email", "t@e.c"}, {"config", "user.name", "t"}} {
		if out, err := gitCmd(repo, args...).CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	writeFile(t, filepath.Join(repo, "first.txt"), "before any commit\n")

	s := Begin(repo, "test", time.Minute, time.Hour)
	if s == nil {
		t.Fatal("Begin returned nil for a repository with no commits")
	}
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}
	res, err := Restore(repo, s.Session().ID, RestoreOptions{Mode: RestorePatch, Out: filepath.Join(t.TempDir(), "p.patch")})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if patch := readFile(t, res.Patch); !strings.Contains(patch, "first.txt") {
		t.Errorf("patch does not mention the only file in the snapshot:\n%s", patch)
	}
}

// A snapshot ref pins every object it reaches, so retention is what keeps the
// safety net from growing without bound.
func TestPruneDropsOldSessionsAndTheirRefs(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	s := begin(t, repo)
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}
	ref := s.Session().Ref

	// Nothing is old enough yet.
	if n, err := Prune(repo, time.Hour); err != nil || n != 0 {
		t.Fatalf("Prune removed %d fresh session(s) (err %v)", n, err)
	}
	if _, err := gitCmd(repo, "rev-parse", "--verify", ref).Output(); err != nil {
		t.Fatal("a fresh snapshot ref was deleted")
	}

	// Age the session past the window.
	older := time.Now().Add(-48 * time.Hour)
	sess := s.Session()
	sess.StartedAt = older
	sess.LastSnapshotAt = &older
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	n, err := Prune(repo, 24*time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Errorf("Prune removed %d sessions, want 1", n)
	}
	if _, err := gitCmd(repo, "rev-parse", "--verify", ref).Output(); err == nil {
		t.Errorf("ref %s survived pruning and is still pinning its objects", ref)
	}
	if list, err := List(repo, false); err != nil || len(list) != 0 {
		t.Errorf("pruned session is still listed: %v %v", list, err)
	}
}

// The normal end of a snapshot's life: you commit the work the agent left, and
// the snapshot becomes a duplicate of content that is now in real history. Only
// an exact tree match counts — a partial commit must leave the snapshot alone,
// since it is still the only copy of what was not committed.
func TestPruneSupersededOnlyWhenTheWorkIsFullyCommitted(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	writeFile(t, filepath.Join(repo, "untracked.txt"), "new\n")

	s := begin(t, repo)
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}
	s.Stop(OutcomeSignalled, nil) // the run ended, so pruning may consider it
	ref := s.Session().Ref

	// Nothing committed yet: the snapshot is the only copy.
	if n, err := PruneSuperseded(repo); err != nil || n != 0 {
		t.Fatalf("PruneSuperseded dropped %d uncommitted session(s) (err %v)", n, err)
	}

	// Commit only part of it — still the only copy of untracked.txt.
	git(t, repo, "add", "tracked.txt")
	git(t, repo, "commit", "-qm", "half of it")
	if n, err := PruneSuperseded(repo); err != nil || n != 0 {
		t.Fatalf("a partial commit pruned %d session(s) (err %v); the rest exists nowhere else", n, err)
	}
	if _, err := gitCmd(repo, "rev-parse", "--verify", ref).Output(); err != nil {
		t.Fatal("the snapshot ref was deleted after a partial commit")
	}

	// Commit the rest: now a branch holds the snapshot's exact tree.
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "the rest")
	n, err := PruneSuperseded(repo)
	if err != nil {
		t.Fatalf("PruneSuperseded: %v", err)
	}
	if n != 1 {
		t.Fatalf("committed work pruned %d session(s), want 1", n)
	}
	if _, err := gitCmd(repo, "rev-parse", "--verify", ref).Output(); err == nil {
		t.Errorf("ref %s survived and is still pinning duplicate objects", ref)
	}
	if list, err := List(repo, false); err != nil || len(list) != 0 {
		t.Errorf("superseded session is still listed: %v %v", list, err)
	}
}

// Pruning must not reach into a sandbox that is still running: deleting a live
// session's ref would break its next compare-and-swap. A session with no end
// record and recent activity is treated as possibly alive.
func TestPruneLeavesALiveSessionAlone(t *testing.T) {
	repo := initRepo(t)
	s := begin(t, repo) // started, never stopped — as a running sandbox looks
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}
	// Its tree is HEAD's (the run changed nothing), so it *is* superseded content.
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "unrelated")

	if n, err := PruneSuperseded(repo); err != nil || n != 0 {
		t.Fatalf("pruned %d session(s) belonging to a running sandbox (err %v)", n, err)
	}
	if n, err := Prune(repo, time.Nanosecond); err != nil || n != 0 {
		t.Fatalf("--older-than pruned %d live session(s) (err %v)", n, err)
	}

	// Once the run ends, it is fair game.
	s.Stop(OutcomeClean, nil)
	if n, err := PruneSuperseded(repo); err != nil || n != 1 {
		t.Fatalf("PruneSuperseded dropped %d finished session(s), want 1 (err %v)", n, err)
	}
}

// The sandbox runs on directories that are not repositories at all, and on hosts
// without git. Neither is an error — there is simply nothing to protect — and
// the run path calls Start/Stop unconditionally, so nil has to be safe.
func TestBeginIsNilOutsideAGitRepository(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := Begin(t.TempDir(), "test", time.Minute, time.Hour)
	if s != nil {
		t.Fatal("Begin returned a Snapshotter for a directory that is not a repository")
	}
	// Every method must tolerate it.
	s.Start()
	if _, err := s.Once(); err != nil {
		t.Errorf("Once on a nil Snapshotter: %v", err)
	}
	s.Stop(OutcomeClean, nil)
	if s.Session() != nil {
		t.Error("a nil Snapshotter reported a session")
	}
}

// Snapshots taken in a --worktree sandbox have to be visible from the user's
// normal checkout: that is where they are standing when the crash sends them
// looking. Refs and objects are shared, so the bucket key must be the main
// repository, not the worktree.
func TestSnapshotsFromAWorktreeAreVisibleFromTheMainRepo(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "feature", wt)
	writeFile(t, filepath.Join(wt, "wip.txt"), "worktree work\n")

	s := begin(t, wt)
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}
	if s.Session().Branch != "feature" {
		t.Errorf("session recorded branch %q, want feature", s.Session().Branch)
	}

	snaps, err := List(repo, false)
	if err != nil {
		t.Fatalf("List from the main repo: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("main repo sees %d sessions, want 1", len(snaps))
	}
	if !snaps[0].Reachable {
		t.Error("the worktree's snapshot is not reachable from the main repository")
	}
	if got := git(t, repo, "show", snaps[0].Commit+":wip.txt"); got != "worktree work" {
		t.Errorf("worktree content not readable from the main repo: %q", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
