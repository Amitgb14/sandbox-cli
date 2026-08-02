package fleet

import (
	"context"
	"errors"
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

// fakeController records what it was asked to do, so the safety rules (never
// touch a running agent) are assertable without docker.
type fakeController struct {
	stopped []string
	killed  []string
	removed []string
	logged  string
}

func (f *fakeController) Logs(_ context.Context, id string, _ bool, _, _ io.Writer) error {
	f.logged = id
	return nil
}

func (f *fakeController) Stop(_ context.Context, id string) error {
	f.stopped = append(f.stopped, id)
	return nil
}

// Kill is recorded separately from Stop: nothing in the fleet should reach for
// it, and a test that could not tell the two apart would not notice if something
// started to.
func (f *fakeController) Kill(_ context.Context, id string) error {
	f.killed = append(f.killed, id)
	return nil
}

func (f *fakeController) Remove(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}

// failingInspector answers every question with an error, which is the one shape
// a plan must not paper over: "I could not ask" is not "nothing is in the way".
type failingInspector struct{ err error }

func (f *failingInspector) Containers(context.Context, map[string]string) ([]runtime.ContainerInfo, error) {
	return nil, f.err
}

// interactiveContainer is a `--detach` session someone started by hand: labelled
// as sandbox-cli's and as this repo's and branch's, but not as the fleet's. It
// holds the same sandbox-<repo>-<branch> name a fleet agent would.
func interactiveContainer(branch, state string, started, finished time.Time) runtime.ContainerInfo {
	c := container(branch, state, started, finished)
	delete(c.Labels, sandbox.LabelFleet)
	c.ID = "interactive-" + branch
	return c
}

func testRunner(cs ...runtime.ContainerInfo) (*Runner, *fakeController) {
	ctl := &fakeController{}
	return &Runner{
		Inspector:  &fakeInspector{containers: cs},
		Controller: ctl,
		Repo:       "/repo",
		RepoID:     testRepoID,
		Out:        io.Discard,
	}, ctl
}

func TestStopOnlyStopsRunning(t *testing.T) {
	now := time.Now()
	run := container("feature-a", "running", now.Add(-time.Minute), time.Time{})
	done := container("feature-b", "exited", now.Add(-time.Hour), now.Add(-time.Minute))
	r, ctl := testRunner(run, done)

	stopped, err := r.Stop(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ctl.stopped) != 1 || ctl.stopped[0] != run.ID {
		t.Errorf("expected only the running container stopped, got %v", ctl.stopped)
	}
	if len(stopped) != 1 || stopped[0] != "feature-a" {
		t.Errorf("expected feature-a reported, got %v", stopped)
	}
}

func TestStopSingleBranch(t *testing.T) {
	now := time.Now()
	a := container("feature-a", "running", now, time.Time{})
	b := container("feature-b", "running", now, time.Time{})
	r, ctl := testRunner(a, b)

	if _, err := r.Stop(context.Background(), "feature-b"); err != nil {
		t.Fatal(err)
	}
	if len(ctl.stopped) != 1 || ctl.stopped[0] != b.ID {
		t.Errorf("expected only feature-b stopped, got %v", ctl.stopped)
	}
}

// The core safety rule of clean: a live agent's container is never removed, and
// the user is told why rather than left wondering what was skipped.
func TestCleanLeavesRunningAlone(t *testing.T) {
	now := time.Now()
	run := container("feature-a", "running", now.Add(-time.Minute), time.Time{})
	done := container("feature-b", "exited", now.Add(-time.Hour), now.Add(-time.Minute))
	r, ctl := testRunner(run, done)

	res, err := r.Clean(context.Background(), "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctl.removed) != 1 || ctl.removed[0] != done.ID {
		t.Errorf("expected only the exited container removed, got %v", ctl.removed)
	}
	if len(res.Containers) != 1 || res.Containers[0] != "feature-b" {
		t.Errorf("expected feature-b reported as reaped, got %v", res.Containers)
	}
	if len(res.Kept) != 1 || !strings.Contains(res.Kept[0], "feature-a") {
		t.Errorf("expected the running branch reported as kept, got %v", res.Kept)
	}
}

// Without --worktrees, clean must not touch checkouts at all.
func TestCleanWithoutWorktreesRemovesNoCheckouts(t *testing.T) {
	now := time.Now()
	r, _ := testRunner(container("feature-b", "exited", now.Add(-time.Hour), now))

	res, err := r.Clean(context.Background(), "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Worktrees) != 0 {
		t.Errorf("expected no worktrees removed, got %v", res.Worktrees)
	}
}

func TestLogsUsesMostRecentContainer(t *testing.T) {
	now := time.Now()
	newer := container("feature-a", "exited", now.Add(-time.Minute), now)
	newer.ID = "newer"
	older := container("feature-a", "exited", now.Add(-time.Hour), now.Add(-30*time.Minute))
	older.ID = "older"

	r, ctl := testRunner(newer, older) // fakeInspector preserves order: newest first
	if err := r.Logs(context.Background(), "feature-a", false, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if ctl.logged != "newer" {
		t.Errorf("expected the most recent container's logs, got %q", ctl.logged)
	}
}

func TestLogsUnknownBranch(t *testing.T) {
	r, _ := testRunner()
	err := r.Logs(context.Background(), "nope", false, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected an error for a branch with no container")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should name the branch, got: %v", err)
	}
}

// newTestRepo builds a throwaway git repository with one commit, so the parts
// of fleet that ask git real questions can be exercised.
func newTestRepo(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed: %s", args, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	return repo
}

// Plan must not create anything, and must surface the refusal that Launch would
// hit rather than letting the user discover it mid-fan-out.
func TestPlanFlagsAlreadyRunningBranch(t *testing.T) {
	repo := newTestRepo(t)
	now := time.Now()
	r, _ := testRunner(container("feature-a", "running", now, time.Time{}))
	r.Repo = repo
	// The fake inspector matches on labels, so point them at this repo.
	r.Inspector.(*fakeInspector).containers[0].Labels[sandbox.LabelRepo] = testRepoID

	plans, err := r.Plan(context.Background(), Spec{
		Agent: "claude",
		Tasks: []Task{{Branch: "feature-a", Prompt: "do it"}},
	}, LaunchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one plan, got %d", len(plans))
	}
	p := plans[0]
	if !p.AlreadyRunning {
		t.Error("expected the busy branch to be flagged")
	}
	if p.WorktreeExists {
		t.Error("no worktree exists for this fake repo; Plan must not report one")
	}
	if len(p.Command) == 0 || !strings.Contains(strings.Join(p.Command, " "), "do it") {
		t.Errorf("plan should carry the agent argv including the prompt, got %v", p.Command)
	}
	if p.Labels[sandbox.LabelBranch] != "feature-a" {
		t.Errorf("plan labels missing the branch: %v", p.Labels)
	}
}

func TestPlanRequiresRepo(t *testing.T) {
	r := &Runner{Inspector: &fakeInspector{}, Repo: ""}
	if _, err := r.Plan(context.Background(), Spec{Agent: "claude", Tasks: []Task{{Branch: "a", Prompt: "p"}}}, LaunchOptions{}); err == nil {
		t.Fatal("expected an error outside a git repository")
	}
}

// A plan is read to decide whether a task will do what was meant, so it reports
// the agent invocation and the check — not the shell bootstrap that finds the
// binary, which buried both when it was included.
func TestPlanReportsTheInvocationNotTheBootstrap(t *testing.T) {
	r, _ := testRunner()
	r.Repo = newTestRepo(t) // Plan resolves worktree paths, so it needs a real repo
	plans, err := r.Plan(context.Background(), Spec{
		Agent: "claude",
		Tasks: []Task{{Branch: "feature-a", Prompt: "do it", Verify: "go test ./..."}},
	}, LaunchOptions{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	got := strings.Join(plans[0].Command, " ")
	if strings.Contains(got, "install.sh") || strings.Contains(got, "$HOME") {
		t.Errorf("the plan carries the bootstrap script: %q", got)
	}
	if !strings.HasPrefix(got, "claude ") || !strings.Contains(got, "do it") {
		t.Errorf("the plan should read as `claude … do it`, got %q", got)
	}
	if plans[0].Verify != "go test ./..." {
		t.Errorf("verify not reported: %q", plans[0].Verify)
	}
}

// clean must not reap the record that land reads. Docker is the state store, so
// removing a finished container discards the branch, base and verify result
// `land` needs — which is how a branch that passed its check becomes mergeable
// only by hand. Observed on this repository's own first fleet run: a `clean`
// run before landing left `fleet land --all` reporting "no fleet branches".
func TestCleanKeepsAContainerWhoseBranchHasWorkToLand(t *testing.T) {
	repo := newTestRepo(t)
	base := gitIn(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	gitIn(t, repo, "checkout", "-qb", "feature-b")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "the agent's work")
	gitIn(t, repo, "checkout", "-q", base)

	now := time.Now()
	done := container("feature-b", "exited", now.Add(-time.Hour), now)
	done.Labels[sandbox.LabelBase] = base
	r, ctl := testRunner(done)
	r.Repo = repo

	res, err := r.Clean(context.Background(), "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctl.removed) != 0 {
		t.Errorf("reaped a container holding landable work: %v", ctl.removed)
	}
	if len(res.Kept) != 1 || !strings.Contains(res.Kept[0], "feature-b") {
		t.Fatalf("expected feature-b reported as kept, got %v", res.Kept)
	}
	if !strings.Contains(res.Kept[0], "fleet land") {
		t.Errorf("the refusal must say how to proceed, got %q", res.Kept[0])
	}

	// --force is the override, and it has to actually override: a guard with no
	// way past it becomes a reason to stop using the command.
	if _, err := r.Clean(context.Background(), "", false, true); err != nil {
		t.Fatal(err)
	}
	if len(ctl.removed) != 1 || ctl.removed[0] != done.ID {
		t.Errorf("--force did not reap the container, removed %v", ctl.removed)
	}
}

// The guard has to follow the verify result, or it closes a loop: `land` refuses
// a branch whose verify failed, so pointing that branch at `fleet land` points it
// at the next refusal and the two commands send the user back and forth with
// nothing in between that works. This is the branch the VERIFY column was added
// to make visible, which makes it the case the advice must get right.
func TestCleanDoesNotSendAFailedVerifyToLand(t *testing.T) {
	repo := newTestRepo(t)
	base := gitIn(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	gitIn(t, repo, "checkout", "-qb", "feature-b")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "the agent's work")
	gitIn(t, repo, "checkout", "-q", base)

	now := time.Now()
	done := container("feature-b", "exited", now.Add(-time.Hour), now)
	done.Labels[sandbox.LabelBase] = base
	done.Labels[sandbox.LabelVerify] = "go test ./..."
	done.ExitCode = VerifyFailedExit
	r, ctl := testRunner(done)
	r.Repo = repo

	res, err := r.Clean(context.Background(), "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctl.removed) != 0 {
		t.Errorf("reaped a container holding landable work: %v", ctl.removed)
	}
	if len(res.Kept) != 1 {
		t.Fatalf("expected feature-b reported as kept, got %v", res.Kept)
	}
	if strings.Contains(res.Kept[0], "fleet land") {
		t.Errorf("sent a failed verify to `fleet land`, which refuses it: %q", res.Kept[0])
	}
	for _, want := range []string{"verify failed", "fleet logs", "--force"} {
		if !strings.Contains(res.Kept[0], want) {
			t.Errorf("the refusal must mention %q, got %q", want, res.Kept[0])
		}
	}
}

// A branch with no recorded base is the detached-HEAD launch Launch deliberately
// records nothing for. The old fallback asked worktree.Branch, which stands a
// commit id in for a detached HEAD — so the guard silently counted commits "not
// in <sha>" and reaped on the answer. An unanswerable question at a data-loss
// guard fails toward keeping instead.
func TestCleanKeepsAContainerWhoseBaseCannotBeRead(t *testing.T) {
	repo := newTestRepo(t)
	gitIn(t, repo, "branch", "feature-b")
	gitIn(t, repo, "checkout", "-q", "--detach")

	now := time.Now()
	done := container("feature-b", "exited", now.Add(-time.Hour), now)
	delete(done.Labels, sandbox.LabelBase)
	r, ctl := testRunner(done)
	r.Repo = repo

	res, err := r.Clean(context.Background(), "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctl.removed) != 0 {
		t.Errorf("reaped a container whose base could not be read: %v", ctl.removed)
	}
	if len(res.Kept) != 1 || !strings.Contains(res.Kept[0], "detached HEAD") {
		t.Errorf("expected the unreadable base reported, got %v", res.Kept)
	}
}

// A plan is read to decide whether to run, so an inspector that could not answer
// has to say so. Dropping the error makes every branch plan as "will start" —
// wrong in the direction that wastes the run it is a rehearsal for.
func TestPlanFailsWhenTheInspectorCannotAnswer(t *testing.T) {
	repo := newTestRepo(t)
	gitIn(t, repo, "branch", "feature-a")
	boom := errors.New("engine unreachable")
	r := &Runner{
		Inspector: &failingInspector{err: boom},
		Repo:      repo,
		RepoID:    testRepoID,
		Out:       io.Discard,
	}

	spec := Spec{Agent: "claude", Tasks: []Task{{Branch: "feature-a", Prompt: "do it"}}}
	if _, err := r.Plan(context.Background(), spec, LaunchOptions{}); !errors.Is(err, boom) {
		t.Errorf("expected the inspector's error to reach the caller, got %v", err)
	}
}

// --force with --worktrees, pinned because it is the combination the guard does
// not cover: held stays empty under force, so a *clean* worktree on a branch with
// unlanded commits is removed along with the container.
//
// That is the intended reading of --force rather than an oversight — it says reap
// this anyway — and it is not data loss, which is the half worth asserting: the
// commits stay on the branch, and only the checkout goes. A dirty worktree is
// still kept, because that guard is not this flag's to override.
func TestCleanForceWithWorktreesRemovesACleanCheckout(t *testing.T) {
	repo := newTestRepo(t)
	base := gitIn(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	gitIn(t, repo, "checkout", "-qb", "feature-b")
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-qm", "the agent's work")
	gitIn(t, repo, "checkout", "-q", base)
	head := gitIn(t, repo, "rev-parse", "feature-b")

	if _, err := worktree.Resolve(repo, "feature-b"); err != nil {
		t.Skipf("could not create a worktree: %v", err)
	}

	now := time.Now()
	done := container("feature-b", "exited", now.Add(-time.Hour), now)
	done.Labels[sandbox.LabelBase] = base
	r, ctl := testRunner(done)
	r.Repo = repo

	res, err := r.Clean(context.Background(), "", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctl.removed) != 1 {
		t.Errorf("--force did not reap the container, removed %v", ctl.removed)
	}
	if len(res.Worktrees) != 1 || res.Worktrees[0] != "feature-b" {
		t.Errorf("expected the clean checkout removed, got %v", res.Worktrees)
	}
	// The half that makes the above acceptable: the work is still on the branch.
	if got := gitIn(t, repo, "rev-parse", "feature-b"); got != head {
		t.Errorf("the branch moved: %q, want %q", got, head)
	}
}

// The other half: once the work is landed there is nothing to protect, so the
// ordinary land-then-clean sequence must reap exactly what it always did.
func TestCleanReapsABranchWithNothingLeftToLand(t *testing.T) {
	repo := newTestRepo(t)
	base := gitIn(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	gitIn(t, repo, "branch", "feature-b")

	now := time.Now()
	done := container("feature-b", "exited", now.Add(-time.Hour), now)
	done.Labels[sandbox.LabelBase] = base
	r, ctl := testRunner(done)
	r.Repo = repo

	res, err := r.Clean(context.Background(), "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctl.removed) != 1 {
		t.Errorf("expected the container reaped, removed %v", ctl.removed)
	}
	if len(res.Kept) != 0 {
		t.Errorf("expected nothing kept, got %v", res.Kept)
	}
}
