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
)

// fakeController records what it was asked to do, so the safety rules (never
// touch a running agent) are assertable without docker.
type fakeController struct {
	stopped []string
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

func (f *fakeController) Remove(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
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

	res, err := r.Clean(context.Background(), "", false)
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

	res, err := r.Clean(context.Background(), "", false)
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
	})
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
	if _, err := r.Plan(context.Background(), Spec{Agent: "claude", Tasks: []Task{{Branch: "a", Prompt: "p"}}}); err == nil {
		t.Fatal("expected an error outside a git repository")
	}
}
