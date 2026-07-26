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
