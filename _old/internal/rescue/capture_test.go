package rescue

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// A capture is the whole feature in one assertion: something changed, somebody
// asked, and the result is a commit that can be found and put back.
func TestCaptureRecordsARestorableSnapshot(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, filepath.Join(dir, "work.txt"), "in progress\n")

	snap, err := Capture(dir, CaptureOptions{Agent: "claude", Label: "before the refactor", Source: SourceSDK})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if snap.Commit == "" || !snap.Reachable {
		t.Fatalf("captured nothing usable: %+v", snap)
	}
	if snap.Label != "before the refactor" || snap.Source != SourceSDK {
		t.Errorf("label/source not recorded: %q / %q", snap.Label, snap.Source)
	}

	// Findable afterwards by the id it reported, which is the only handle a
	// caller keeps.
	found, err := Find(dir, snap.ID)
	if err != nil {
		t.Fatalf("Find(%s): %v", snap.ID, err)
	}
	if found.Commit != snap.Commit {
		t.Errorf("Find returned %s, capture reported %s", found.Commit, snap.Commit)
	}

	// And the content is actually in it.
	out := git(t, dir, "show", "--name-only", "--format=", snap.Commit)
	if !strings.Contains(out, "work.txt") {
		t.Errorf("snapshot does not contain work.txt:\n%s", out)
	}
}

// A capture is not a run, and must never be counted as one that died. This is
// the whole reason OutcomeManual exists: `sandbox-cli recover` leads with
// crashes, and every deliberate checkpoint appearing there would drown the one
// thing that screen is for.
func TestCaptureIsNotACrashedRun(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, filepath.Join(dir, "work.txt"), "in progress\n")

	snap, err := Capture(dir, CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if snap.Crashed() {
		t.Error("a capture reports itself as a crashed run")
	}
	if got := snap.Status(); got != "snapshot" {
		t.Errorf("Status() = %q, want %q", got, "snapshot")
	}
	if snap.Outcome != OutcomeManual {
		t.Errorf("Outcome = %q, want %q", snap.Outcome, OutcomeManual)
	}
}

// An unchanged tree is the one case where a capture and the background loop
// disagree on purpose: the loop shrugs, a caller who asked is told. And nothing
// is left behind — a manifest for a snapshot that does not exist would sit in
// every later listing claiming to be recoverable.
func TestCaptureRefusesAnUnchangedTree(t *testing.T) {
	dir := initRepo(t)

	before, err := Sessions(mustRepoRoot(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Capture(dir, CaptureOptions{}); !errors.Is(err, ErrNothingToSnapshot) {
		t.Fatalf("Capture on a clean tree: got %v, want ErrNothingToSnapshot", err)
	}
	after, err := Sessions(mustRepoRoot(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("a refused capture left %d session(s) behind", len(after)-len(before))
	}
}

// The reason Capture and Begin are different functions: Begin is silent because
// the run path has nothing useful to say, and Capture answers because somebody
// asked in as many words.
func TestCaptureSaysWhyThereIsNoSnapshot(t *testing.T) {
	dir := t.TempDir() // not a repository
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if Begin(dir, "test", time.Minute, Retention{}) != nil {
		t.Fatal("Begin returned a Snapshotter for a non-repository")
	}
	_, err := Capture(dir, CaptureOptions{})
	if err == nil {
		t.Fatal("Capture succeeded outside a git repository")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error does not say what is wrong: %v", err)
	}
}

// The safety argument from snapshot_test.go, restated for the new entry point:
// a capture writes into refs/sandbox and nowhere else.
func TestCaptureLeavesTheRepositoryAlone(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, filepath.Join(dir, "work.txt"), "in progress\n")

	head := git(t, dir, "rev-parse", "HEAD")
	branch := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	status := git(t, dir, "status", "--porcelain")
	index, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Capture(dir, CaptureOptions{}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if got := git(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved: %s -> %s", head, got)
	}
	if got := git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Errorf("branch changed: %s -> %s", branch, got)
	}
	if got := git(t, dir, "status", "--porcelain"); got != status {
		t.Errorf("working tree changed:\n%s\n---\n%s", status, got)
	}
	after, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(index) {
		t.Error("the repository's own index was written")
	}
}

func TestSetRetentionRoundTrips(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, filepath.Join(dir, "work.txt"), "in progress\n")
	snap, err := Capture(dir, CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}

	got, err := SetRetention(dir, snap.ID, "72h")
	if err != nil {
		t.Fatalf("SetRetention: %v", err)
	}
	if got.Retention != "72h" {
		t.Errorf("Retention = %q, want %q", got.Retention, "72h")
	}
	// Persisted, not just returned.
	found, err := Find(dir, snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Retention != "72h" {
		t.Errorf("reloaded Retention = %q, want %q", found.Retention, "72h")
	}

	// An empty value clears the override rather than meaning "delete immediately".
	if got, err = SetRetention(dir, snap.ID, ""); err != nil {
		t.Fatalf("SetRetention(\"\"): %v", err)
	}
	if got.Retention != "" {
		t.Errorf("Retention = %q, want empty", got.Retention)
	}

	// Validated here, because an unparseable duration in the manifest would not
	// surface until pruning ran — the one moment nobody is watching.
	if _, err := SetRetention(dir, snap.ID, "next tuesday"); err == nil {
		t.Error("SetRetention accepted a value that is not a duration")
	}
}

// Retention.For is the whole per-snapshot rule, and each case is one sentence of
// it.
func TestRetentionFor(t *testing.T) {
	defaults := Retention{Run: 14 * 24 * time.Hour, Manual: 7 * 24 * time.Hour}
	for _, tc := range []struct {
		name string
		sess Session
		want time.Duration
	}{
		{"a run uses the run default", Session{}, 14 * 24 * time.Hour},
		{"a capture uses the manual default", Session{Outcome: OutcomeManual}, 7 * 24 * time.Hour},
		{"an explicit value wins", Session{Outcome: OutcomeManual, Retention: "1h"}, time.Hour},
		{"and wins for a run too", Session{Retention: "1h"}, time.Hour},
		// Keeping a snapshot forever costs disk; deleting one on a value nobody
		// can parse costs the work it was holding.
		{"an unparseable value keeps it", Session{Retention: "next tuesday"}, 0},
		{"so does a negative one", Session{Retention: "-5h"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaults.For(tc.sess); got != tc.want {
				t.Errorf("For() = %v, want %v", got, tc.want)
			}
		})
	}
	// An unset manual default is the package constant rather than "keep forever",
	// so a caller that only knows about run retention still ages captures out.
	if got := (Retention{Run: time.Hour}).For(Session{Outcome: OutcomeManual}); got != DefaultManualRetention {
		t.Errorf("unset manual default = %v, want %v", got, DefaultManualRetention)
	}
}

// PruneExpired asks each session rather than applying one cutoff — the point of
// the whole per-snapshot retention design.
func TestPruneExpiredHonoursEachSnapshot(t *testing.T) {
	dir := initRepo(t)
	root := mustRepoRoot(t, dir)

	writeFile(t, filepath.Join(dir, "keep.txt"), "keep\n")
	keep, err := Capture(dir, CaptureOptions{Retention: "720h"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "drop.txt"), "drop\n")
	drop, err := Capture(dir, CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Age both a day past the manual default. The one that asked for a month
	// survives it; the one that did not, does not.
	old := time.Now().Add(-DefaultManualRetention - 24*time.Hour)
	for _, id := range []string{keep.ID, drop.ID} {
		age(t, root, id, old)
	}

	n, err := PruneExpired(root, Retention{Run: time.Hour, Manual: DefaultManualRetention})
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d sessions, want 1", n)
	}
	if _, err := Find(dir, keep.ID); err != nil {
		t.Errorf("the snapshot that asked for 720h was pruned: %v", err)
	}
	if _, err := Find(dir, drop.ID); err == nil {
		t.Error("the expired snapshot survived")
	}
}

// `recover prune --older-than` is a direct instruction and outranks a
// per-snapshot retention, which is a default to age by rather than a veto.
func TestPruneIgnoresPerSnapshotRetention(t *testing.T) {
	dir := initRepo(t)
	root := mustRepoRoot(t, dir)
	writeFile(t, filepath.Join(dir, "work.txt"), "work\n")
	snap, err := Capture(dir, CaptureOptions{Retention: "8760h"})
	if err != nil {
		t.Fatal(err)
	}
	age(t, root, snap.ID, time.Now().Add(-48*time.Hour))

	n, err := Prune(root, time.Hour)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned %d, want 1", n)
	}
}

func mustRepoRoot(t *testing.T, dir string) string {
	t.Helper()
	root, err := MainRepoRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// age backdates a session's recorded activity, so retention can be tested
// without waiting for it.
func age(t *testing.T, repoRoot, id string, when time.Time) {
	t.Helper()
	sess, err := findSession(repoRoot, id)
	if err != nil {
		t.Fatal(err)
	}
	sess.StartedAt = when
	sess.EndedAt = &when
	sess.LastSnapshotAt = &when
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
}

// The default is stated in both packages — this one imports config, so config
// cannot import it back and the constant cannot be shared — and a pair of
// numbers that must agree while living apart is exactly what a test is for.
func TestManualRetentionDefaultAgreesWithConfig(t *testing.T) {
	if DefaultManualRetention != config.DefaultManualSnapshotRetention {
		t.Errorf("rescue says %v, config says %v", DefaultManualRetention, config.DefaultManualSnapshotRetention)
	}
}

// The case the first version got wrong, found by running it rather than by
// testing it: two captures of an unchanged **dirty** tree.
//
// Seeding the comparison from HEAD alone made this succeed — the tree did
// differ from HEAD, so there was "something to snapshot" by a definition nobody
// holds. What a caller means by unchanged is "nothing has happened since the
// last checkpoint", and a workspace with uncommitted work in it is the normal
// state for the whole feature: an agent is mid-task, which is why somebody is
// taking checkpoints at all.
func TestCaptureRefusesADuplicateOfADirtyTree(t *testing.T) {
	dir := initRepo(t)
	writeFile(t, filepath.Join(dir, "work.txt"), "in progress\n")

	first, err := Capture(dir, CaptureOptions{Label: "one"})
	if err != nil {
		t.Fatalf("first capture: %v", err)
	}

	if _, err := Capture(dir, CaptureOptions{Label: "two"}); !errors.Is(err, ErrNothingToSnapshot) {
		t.Fatalf("second capture with nothing changed: got %v, want ErrNothingToSnapshot", err)
	}

	// And it is still a *capture*, not a refusal to ever capture again: one more
	// edit and the next one goes through.
	writeFile(t, filepath.Join(dir, "work.txt"), "further along\n")
	third, err := Capture(dir, CaptureOptions{Label: "three"})
	if err != nil {
		t.Fatalf("capture after an edit: %v", err)
	}
	if third.Commit == first.Commit {
		t.Error("a changed tree produced the same commit as the first snapshot")
	}
}
