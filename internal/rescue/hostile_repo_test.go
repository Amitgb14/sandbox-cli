package rescue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests treat the repository as hostile, which is the real situation: the
// sandbox mounts /workspace read-write, so the agent can write .git/config,
// .gitattributes and .git/hooks/ at any moment — including while a snapshot is
// being taken. git happily runs commands named by all three, so before the
// githard hardening a plain `sandbox-cli run` executed agent-supplied code on
// the host every couple of minutes.
//
// Each test arms one mechanism with a marker file outside the repository and
// asserts the marker never appears.

// armHostileRepo plants every command-execution mechanism the audit found, each
// writing a distinctly named marker into markerDir.
func armHostileRepo(t *testing.T, repo, markerDir string) {
	t.Helper()

	// filter.<x>.clean — runs on `git add`, selected by .gitattributes.
	git(t, repo, "config", "filter.pwn.clean", "touch "+filepath.Join(markerDir, "clean")+"; cat")
	// filter.<x>.smudge — runs on checkout, e.g. restore --into-worktree.
	git(t, repo, "config", "filter.pwn.smudge", "touch "+filepath.Join(markerDir, "smudge")+"; cat")
	// diff.<x>.textconv — runs on `show -p` / `diff`.
	git(t, repo, "config", "diff.pwn.textconv", "touch "+filepath.Join(markerDir, "textconv")+"; cat")
	// core.fsmonitor — runs on `git add` and other index operations.
	git(t, repo, "config", "core.fsmonitor", "touch "+filepath.Join(markerDir, "fsmonitor")+"; echo")
	writeFile(t, filepath.Join(repo, ".gitattributes"), "* filter=pwn diff=pwn\n")

	// .git/hooks/reference-transaction — runs on `git update-ref`, which is how
	// every snapshot is published. This one falsified the old claim in
	// snapshot.go that "no hook runs: these are all plumbing commands".
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\ntouch " + filepath.Join(markerDir, "reftx") + "\n"
	if err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	// ...and the hook that fires on checkout, for the restore path.
	post := "#!/bin/sh\ntouch " + filepath.Join(markerDir, "postcheckout") + "\n"
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte(post), 0o755); err != nil {
		t.Fatal(err)
	}
}

// assertNothingRan fails with the names of any markers that appeared.
func assertNothingRan(t *testing.T, markerDir, during string) {
	t.Helper()
	entries, err := os.ReadDir(markerDir)
	if err != nil {
		t.Fatal(err)
	}
	var fired []string
	for _, e := range entries {
		fired = append(fired, e.Name())
	}
	if len(fired) > 0 {
		t.Errorf("%s executed agent-controlled commands on the host: %v", during, fired)
	}
}

// TestSnapshotRunsNoRepositoryCommands is the core regression. A snapshot is
// taken unattended every couple of minutes for the whole life of a run, so any
// command execution here is a live container→host escape, not a next-invocation
// one.
func TestSnapshotRunsNoRepositoryCommands(t *testing.T) {
	repo := initRepo(t)
	markers := t.TempDir()
	armHostileRepo(t, repo, markers)

	writeFile(t, filepath.Join(repo, "work.txt"), "agent work\n")
	s := begin(t, repo)
	if _, err := s.Once(); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	assertNothingRan(t, markers, "taking a snapshot")

	// The hardening must not cost the snapshot its content: with filters disabled
	// the file is stored verbatim, which is what a rescue copy wants.
	tree := git(t, repo, "ls-tree", "-r", "--name-only", s.Session().Ref)
	if !strings.Contains(tree, "work.txt") {
		t.Errorf("snapshot lost the agent's work; tree = %q", tree)
	}
}

// TestRecoverInspectionRunsNoRepositoryCommands covers the commands a user runs
// *after* a run they already distrust — the worst possible moment to execute
// something the agent chose.
func TestRecoverInspectionRunsNoRepositoryCommands(t *testing.T) {
	repo := initRepo(t)
	markers := t.TempDir()
	armHostileRepo(t, repo, markers)

	writeFile(t, filepath.Join(repo, "work.txt"), "agent work\n")
	s := begin(t, repo)
	if _, err := s.Once(); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	id := s.Session().ID
	s.Stop(OutcomeClean, nil)

	// `recover show -p` renders the patch through diff.<x>.textconv.
	if err := Show(repo, id, true); err != nil {
		t.Fatalf("show: %v", err)
	}
	assertNothingRan(t, markers, "recover show -p")

	// `recover restore --patch` writes a patch file the same way.
	out := filepath.Join(t.TempDir(), "w.patch")
	if _, err := Restore(repo, id, RestoreOptions{Mode: RestorePatch, Out: out}); err != nil {
		t.Fatalf("restore --patch: %v", err)
	}
	assertNothingRan(t, markers, "recover restore --patch")
}

// TestValidWorktreeAdminDir pins the guard on `recover repair`'s write target.
// The path comes from the .git pointer file inside the workspace, so the agent
// chooses it, and repairWorktreeAdmin creates a directory and writes three files
// there. Unvalidated, that was a write-anywhere primitive.
func TestValidWorktreeAdminDir(t *testing.T) {
	repo := initRepo(t)
	gitDir := filepath.Join(repo, ".git")

	// The legitimate shape must be accepted, including when "worktrees" does not
	// exist yet — a fully deleted registry is exactly the case repair exists for.
	good := filepath.Join(gitDir, "worktrees", "feat")
	if err := validWorktreeAdminDir(good); err != nil {
		t.Errorf("a real worktree admin dir was refused: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validWorktreeAdminDir(good); err != nil {
		t.Errorf("a real worktree admin dir was refused once worktrees/ existed: %v", err)
	}

	outside := t.TempDir()
	bad := map[string]string{
		"somewhere else entirely":    filepath.Join(outside, "PWN"),
		"parent not named worktrees": filepath.Join(gitDir, "EVIL", "PWN"),
		"not a git directory":        filepath.Join(outside, "worktrees", "PWN"),
		"relative":                   "worktrees/PWN",
		"empty":                      "",
	}
	for name, p := range bad {
		if err := validWorktreeAdminDir(p); err == nil {
			t.Errorf("%s: %q was accepted and would be created and written to", name, p)
		}
	}

	// The audit's actual proof: a symlink planted inside the parent .git — which
	// is bind-mounted read-write into the container — with the pointer aimed
	// through it, so a lexically-plausible path resolves outside the repository.
	linked := filepath.Join(gitDir, "worktrees-link")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Rename(filepath.Join(gitDir, "worktrees"), filepath.Join(gitDir, "worktrees-real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(gitDir, "worktrees")); err != nil {
		t.Fatal(err)
	}
	if err := validWorktreeAdminDir(good); err == nil {
		t.Error("a symlinked worktrees/ was accepted; the write would land outside the repository")
	}
}

// TestRepairRefusesAWriteOutsideTheRepository exercises the call site, not just
// the helper: a test that only checks validWorktreeAdminDir would still pass if
// someone deleted the call, which is the mistake worth catching.
func TestRepairRefusesAWriteOutsideTheRepository(t *testing.T) {
	repo := initRepo(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "PWN")

	f := Finding{
		Kind:        KindWorktreeAdmin,
		Fix:         "recreate", // non-empty so Repairable() lets it through
		worktreeDir: filepath.Join(repo, "wt"),
		gitDir:      target, // what the agent's .git pointer named
		commonDir:   filepath.Join(repo, ".git"),
		branch:      "feat",
		head:        "",
	}
	if err := Repair(f, "feat"); err == nil {
		t.Fatal("Repair accepted a target outside the repository")
	}
	if _, err := os.Stat(target); err == nil {
		t.Errorf("Repair created %q despite refusing", target)
	}
}
