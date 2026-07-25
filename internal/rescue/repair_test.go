package rescue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The failure that motivated this whole package: a `git worktree prune` inside
// the container — or the one `git gc` runs for itself — reaches out through the
// read-write .git mount and deletes the worktree's administrative directory in
// the parent repository. Every git command in the checkout then fails with
// "not a git repository", so the user cannot see, diff, or commit files that are
// sitting right there on disk.
//
// `git worktree repair` does not fix this: it reconnects worktrees that *moved*,
// and a deleted admin directory has nothing left to reconnect. Repair rebuilds
// it, and must do so without discarding the uncommitted work that is the entire
// reason anyone cares.
func TestRepairRebuildsADeletedWorktreeAdminDir(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "feature", wt)

	// Work the agent left behind: one commit and one uncommitted file.
	writeFile(t, filepath.Join(wt, "committed.txt"), "agent commit\n")
	git(t, wt, "add", "-A")
	git(t, wt, "commit", "-qm", "agent work")
	writeFile(t, filepath.Join(wt, "wip.txt"), "uncommitted\n")

	// A session record is what lets Repair know which branch this was, since the
	// admin directory that held that fact is about to disappear.
	s := begin(t, wt)
	if _, err := s.Once(); err != nil {
		t.Fatalf("Once: %v", err)
	}
	s.Stop(OutcomeSignalled, nil)

	adminDir := filepath.Join(repo, ".git", "worktrees", "wt")
	if !isDir(adminDir) {
		t.Fatalf("test setup: no admin dir at %s", adminDir)
	}
	if err := os.RemoveAll(adminDir); err != nil {
		t.Fatal(err)
	}
	if out, err := gitCmd(wt, "status").CombinedOutput(); err == nil {
		t.Fatalf("test setup: git still works after deleting the admin dir: %s", out)
	}

	findings, err := Diagnose(wt)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	var admin *Finding
	for i := range findings {
		if findings[i].Kind == KindWorktreeAdmin {
			admin = &findings[i]
		}
	}
	if admin == nil {
		t.Fatalf("Diagnose missed the missing admin dir: %+v", findings)
	}
	if !admin.Repairable() {
		t.Fatalf("finding is not repairable, so the branch was not recovered from the session: %+v", admin)
	}

	if err := Repair(*admin, ""); err != nil {
		t.Fatalf("Repair: %v", err)
	}

	// Everything git has to work again, on the right branch, with the work intact.
	if got := git(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Errorf("repaired worktree is on %q, want feature", got)
	}
	if got := git(t, wt, "log", "--oneline", "-1", "--format=%s"); got != "agent work" {
		t.Errorf("the agent's commit is not on the repaired branch: %q", got)
	}
	if got := git(t, wt, "status", "--porcelain"); !strings.Contains(got, "wip.txt") {
		t.Errorf("uncommitted work is not visible after repair: %q", got)
	}
	if got := readFile(t, filepath.Join(wt, "wip.txt")); got != "uncommitted\n" {
		t.Errorf("repair damaged the uncommitted file: %q", got)
	}
	if git(t, wt, "diff", "--cached", "--name-only") != "" {
		t.Error("repair left changes staged; `reset` should have rebuilt the index from HEAD")
	}

	// And a second Diagnose is clean.
	after, err := Diagnose(wt)
	if err != nil {
		t.Fatalf("Diagnose after repair: %v", err)
	}
	for _, f := range after {
		if f.Kind == KindWorktreeAdmin {
			t.Errorf("still broken after repair: %+v", f)
		}
	}
}

// A killed git leaves index.lock behind and every later git write refuses to
// run. Only an old one is called stale: a lock that was touched a moment ago may
// belong to a live process.
func TestDiagnoseFindsOnlyStaleLocks(t *testing.T) {
	repo := initRepo(t)
	lock := filepath.Join(repo, ".git", "index.lock")
	writeFile(t, lock, "")

	findings, err := Diagnose(repo)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(findings, KindStaleLock); n != 0 {
		t.Errorf("a lock touched just now was reported stale (%d findings)", n)
	}

	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	findings, err = Diagnose(repo)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(findings, KindStaleLock); n != 1 {
		t.Fatalf("stale lock not reported (%d findings): %+v", n, findings)
	}
	for _, f := range findings {
		if f.Kind != KindStaleLock {
			continue
		}
		if err := Repair(f, ""); err != nil {
			t.Fatalf("Repair: %v", err)
		}
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Error("the stale lock survived repair")
	}
}

// An interrupted merge is reported and never touched: finishing it or throwing
// it away is the user's decision, and guessing wrong discards real work.
func TestInterruptedOperationsAreReportedNotRepaired(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, filepath.Join(repo, ".git", "MERGE_HEAD"), "0000000000000000000000000000000000000000\n")

	findings, err := Diagnose(repo)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range findings {
		if f.Kind != KindInProgress {
			continue
		}
		found = true
		if f.Repairable() {
			t.Error("an interrupted merge was offered as a repair")
		}
		if f.Advice == "" {
			t.Error("an interrupted merge was reported with no advice")
		}
		if err := Repair(f, ""); err == nil {
			t.Error("Repair acted on a report-only finding")
		}
	}
	if !found {
		t.Errorf("Diagnose missed the interrupted merge: %+v", findings)
	}
}

// A healthy repository must produce no findings at all, or `recover` cries wolf
// and nobody reads it when it matters.
func TestDiagnoseIsQuietOnAHealthyRepo(t *testing.T) {
	repo := initRepo(t)
	findings, err := Diagnose(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("healthy repository reported %d problem(s): %+v", len(findings), findings)
	}
}

func count(findings []Finding, kind string) int {
	n := 0
	for _, f := range findings {
		if f.Kind == kind {
			n++
		}
	}
	return n
}
