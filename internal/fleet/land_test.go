package fleet

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// gitIn runs a git command in dir, skipping the test if git is unhappy — these
// are exercising land's logic, not git's installation.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("git %v in %s failed: %s", args, dir, out)
	}
	return strings.TrimSpace(string(out))
}

// landFixture builds a repo, a feature-branch worktree, and a runner wired to an
// empty inspector (no agent running). The worktree is returned so a test can put
// work in it however it likes.
func landFixture(t *testing.T) (repo, base, worktreePath string, r *Runner) {
	t.Helper()
	repo = newTestRepo(t) // skips if git is missing; sets XDG_CONFIG_HOME
	base = worktree.HeadBranch(repo)
	if base == "" {
		t.Skip("repo has a detached HEAD after init")
	}
	info, err := worktree.Resolve(repo, "feature-a")
	if err != nil {
		t.Fatalf("Resolve worktree: %v", err)
	}
	r = &Runner{Inspector: &fakeInspector{}, Repo: repo, RepoID: testRepoID, Out: io.Discard}
	return repo, base, info.Path, r
}

func TestLandMergesAgentCommit(t *testing.T) {
	repo, base, wt, r := landFixture(t)

	// The agent committed its own work in the worktree.
	if err := os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-qm", "implement feature")

	res, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if res.Committed {
		t.Error("nothing was uncommitted; land should not have committed")
	}
	if !res.Merged || res.Base != base {
		t.Errorf("unexpected result: %+v (base %q)", res, base)
	}
	// The file the agent produced is now present in the base checkout.
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Errorf("landed file not in base checkout: %v", err)
	}
	// --no-ff, so there is a merge commit.
	if subject := gitIn(t, repo, "log", "-1", "--pretty=%s"); !strings.Contains(subject, "Merge") {
		t.Errorf("expected a merge commit, got %q", subject)
	}
}

func TestLandCommitsDirtyWorktree(t *testing.T) {
	repo, _, wt, r := landFixture(t)

	// The agent left work uncommitted; land must commit it before merging.
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := r.Land(context.Background(), "feature-a", LandOptions{Message: "land the wip"})
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if !res.Committed {
		t.Error("expected land to commit the dirty worktree")
	}
	if !res.Merged {
		t.Error("expected the branch to be merged")
	}
	if _, err := os.Stat(filepath.Join(repo, "wip.txt")); err != nil {
		t.Errorf("landed file not in base checkout: %v", err)
	}
}

func TestLandRefusesWhileRunning(t *testing.T) {
	repo, _, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "x")

	// An agent is still on this branch.
	c := container("feature-a", "running", time.Now(), time.Time{})
	c.Labels[sandbox.LabelRepo] = testRepoID
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	_, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("expected a refusal while running, got %v", err)
	}
}

func TestLandRefusesDirtyBase(t *testing.T) {
	repo, _, wt, r := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "x")

	// The user's own checkout has uncommitted work.
	if err := os.WriteFile(filepath.Join(repo, "local.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("expected a refusal for a dirty base, got %v", err)
	}
}

func TestLandNothingToLand(t *testing.T) {
	_, _, _, r := landFixture(t)
	// The worktree has no commits beyond base and nothing uncommitted.
	_, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err == nil || !strings.Contains(err.Error(), "nothing to land") {
		t.Fatalf("expected a nothing-to-land error, got %v", err)
	}
}

// exitedOn returns an exited container for branch, labelled as having been
// launched to land on base — what `fleet run` stamps and what land reads back.
func exitedOn(repo, branch, base string) runtime.ContainerInfo {
	c := container(branch, "exited", time.Now().Add(-time.Minute), time.Now())
	c.Labels[sandbox.LabelRepo] = testRepoID
	if base != "" {
		c.Labels[sandbox.LabelBase] = base
	}
	return c
}

// verifiedRun returns an exited container for branch that declared a verify
// command and ended with the given code — what land reads back to decide whether
// the work passed its own definition of done.
func verifiedRun(repo, branch string, exitCode int) runtime.ContainerInfo {
	c := container(branch, "exited", time.Now().Add(-time.Minute), time.Now())
	c.Labels[sandbox.LabelRepo] = testRepoID
	c.Labels[sandbox.LabelVerify] = "go test ./..."
	c.ExitCode = exitCode
	return c
}

// The point of the whole verify field: work that failed its own check does not
// land.
func TestLandRefusesWorkThatFailedItsVerify(t *testing.T) {
	repo, _, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	c := verifiedRun(repo, "feature-a", VerifyFailedExit)
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	_, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err == nil || !strings.Contains(err.Error(), "did not pass its verify") {
		t.Fatalf("expected a refusal for a failed verify, got %v", err)
	}
	// The message must name the command that failed and how to see why.
	for _, want := range []string{"go test ./...", "fleet logs", "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A run killed before it reached its verify is unchecked, not passed — and it
// must not be reported as a verify failure either, because nothing ran the verify.
func TestLandRefusesARunThatNeverReachedItsVerify(t *testing.T) {
	repo, _, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	c := verifiedRun(repo, "feature-a", 137) // OOM-killed
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	_, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err == nil || !strings.Contains(err.Error(), "without reaching its verify") {
		t.Fatalf("expected an unchecked-run refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "137") {
		t.Errorf("error %q does not report the exit code", err)
	}
}

// The positive twin: a passing verify lands, and says so.
func TestLandProceedsWhenVerifyPassed(t *testing.T) {
	repo, _, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	c := verifiedRun(repo, "feature-a", 0)
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	res, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if !res.Verified || res.Forced {
		t.Errorf("expected a verified landing, got %+v", res)
	}
}

// --force lands a failed run, and the result says it was forced so the caller
// cannot report it as an ordinary success.
func TestLandForceOverridesAFailedVerify(t *testing.T) {
	repo, _, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	c := verifiedRun(repo, "feature-a", VerifyFailedExit)
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	res, err := r.Land(context.Background(), "feature-a", LandOptions{Force: true})
	if err != nil {
		t.Fatalf("Land --force: %v", err)
	}
	if !res.Forced || res.Verified {
		t.Errorf("expected a forced, unverified landing, got %+v", res)
	}
}

// A run that declared no verify still lands — this is a fleet of agents, not a CI
// system — but it is never reported as having passed anything.
func TestLandWithoutAVerifyLandsUnverified(t *testing.T) {
	repo, _, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	c := container("feature-a", "exited", time.Now().Add(-time.Minute), time.Now())
	c.Labels[sandbox.LabelRepo] = testRepoID
	c.ExitCode = 1 // the agent itself failed; with no verify, nothing judged the work
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	res, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if res.Verified || res.Forced {
		t.Errorf("a run with no verify is neither verified nor forced, got %+v", res)
	}
}

// The merge rewrites files in the main checkout, so an agent working there must
// stop first. Identified by its branch label: git will not let the base branch be
// checked out anywhere else, so an agent on it is in the main checkout.
func TestLandRefusesWhileAnAgentWorksTheBaseCheckout(t *testing.T) {
	repo, base, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	c := container(base, "running", time.Now(), time.Time{})
	c.Labels[sandbox.LabelRepo] = testRepoID
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	_, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err == nil || !strings.Contains(err.Error(), "checkout itself") {
		t.Fatalf("expected a refusal while an agent works the base checkout, got %v", err)
	}
	// It has to name the container, or there is nothing to go and stop.
	if !strings.Contains(err.Error(), c.Name) {
		t.Errorf("error %q does not name the container %q", err, c.Name)
	}
}

// An agent that has *finished* in the base checkout blocks nothing: the refusal
// is about files moving under a live process, not about history. Without this the
// check above could refuse on any container at all and still pass.
func TestLandProceedsWhenTheBaseAgentHasExited(t *testing.T) {
	repo, base, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	c := container(base, "exited", time.Now().Add(-time.Minute), time.Now())
	c.Labels[sandbox.LabelRepo] = testRepoID
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	if _, err := r.Land(context.Background(), "feature-a", LandOptions{}); err != nil {
		t.Fatalf("Land: %v", err)
	}
}

// The checkout moving between launch and landing is the case the label exists
// for: merging into whatever happens to be checked out would put a merge commit
// on a branch nobody chose.
func TestLandRefusesWhenCheckoutMovedOffTheRecordedBase(t *testing.T) {
	repo, base, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "x")

	c := exitedOn(repo, "feature-a", "some-other-base")
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	_, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err == nil || !strings.Contains(err.Error(), "launched to land on") {
		t.Fatalf("expected a refusal for a moved checkout, got %v", err)
	}
	// The message has to say both branches and how to proceed, or it is a wall.
	for _, want := range []string{"some-other-base", base, "--onto"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// The ordinary case: the recorded base is what is checked out, and nothing new
// gets in the way. The positive twin of the refusal above — without it the check
// could refuse everything and still pass.
func TestLandProceedsWhenRecordedBaseMatches(t *testing.T) {
	repo, base, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	c := exitedOn(repo, "feature-a", base)
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	res, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if !res.Merged || res.Base != base {
		t.Errorf("unexpected result: %+v (base %q)", res, base)
	}
}

// --onto is the deliberate override: it replaces the recorded expectation, and is
// still checked against what is actually checked out.
func TestLandOntoOverridesTheRecordedBase(t *testing.T) {
	repo, base, wt, _ := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	c := exitedOn(repo, "feature-a", "some-other-base")
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: repo, RepoID: testRepoID, Out: io.Discard}

	if _, err := r.Land(context.Background(), "feature-a", LandOptions{Onto: base}); err != nil {
		t.Fatalf("Land --onto %s: %v", base, err)
	}
}

// Naming a branch that is not checked out is a mistake git cannot carry out, and
// must not be reinterpreted as "land wherever I am".
func TestLandOntoRefusesABranchThatIsNotCheckedOut(t *testing.T) {
	_, _, wt, r := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	_, err := r.Land(context.Background(), "feature-a", LandOptions{Onto: "not-checked-out"})
	if err == nil || !strings.Contains(err.Error(), "checked out") {
		t.Fatalf("expected a refusal for an unchecked-out --onto, got %v", err)
	}
}

// A reaped container leaves no expectation to violate, so landing still works —
// `fleet clean` must not quietly break `fleet land`.
func TestLandWithNoRecordedBaseStillLands(t *testing.T) {
	_, base, wt, r := landFixture(t)
	gitIn(t, wt, "commit", "--allow-empty", "-qm", "work")

	res, err := r.Land(context.Background(), "feature-a", LandOptions{})
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if !res.Merged || res.Base != base {
		t.Errorf("unexpected result: %+v (base %q)", res, base)
	}
}

func TestLandUnknownBranch(t *testing.T) {
	repo := newTestRepo(t)
	r := &Runner{Inspector: &fakeInspector{}, Repo: repo, RepoID: testRepoID, Out: io.Discard}
	_, err := r.Land(context.Background(), "no-such-branch", LandOptions{})
	if err == nil || !strings.Contains(err.Error(), "no worktree") {
		t.Fatalf("expected a no-worktree error, got %v", err)
	}
}
