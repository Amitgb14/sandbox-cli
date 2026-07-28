package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// fakeInspector stands in for docker so the join logic is testable without a
// daemon.
type fakeInspector struct {
	containers []runtime.ContainerInfo
	gotFilters map[string]string
}

func (f *fakeInspector) Containers(_ context.Context, labels map[string]string) ([]runtime.ContainerInfo, error) {
	f.gotFilters = labels
	var out []runtime.ContainerInfo
	for _, c := range f.containers {
		match := true
		for k, v := range labels {
			if c.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, c)
		}
	}
	return out, nil
}

// testRepoID is the stable repo identity every fake container is labelled with —
// containers carry worktree.RepoID, not a path, so a fixture that used a path
// would match no filter the Runner builds.
const testRepoID = "app-1234abcd"

func container(branch, state string, started, finished time.Time) runtime.ContainerInfo {
	return runtime.ContainerInfo{
		ID:   "id-" + branch + "-" + state,
		Name: "sandbox-app-" + branch,
		Labels: map[string]string{
			sandbox.LabelCLI:    "1",
			sandbox.LabelRepo:   testRepoID,
			sandbox.LabelBranch: branch,
		},
		State:      state,
		StartedAt:  started,
		FinishedAt: finished,
	}
}

// The status query must be scoped to this repository, or a fleet in one project
// would report agents belonging to another.
func TestStatusFiltersByRepo(t *testing.T) {
	fake := &fakeInspector{}
	r := &Runner{Inspector: fake, Repo: "/repo", RepoID: testRepoID}
	if _, err := r.Status(context.Background(), "main"); err != nil {
		t.Fatal(err)
	}
	if fake.gotFilters[sandbox.LabelRepo] != testRepoID {
		t.Errorf("expected a repo-scoped query, got %v", fake.gotFilters)
	}
	if fake.gotFilters[sandbox.LabelCLI] != "1" {
		t.Errorf("expected the managed filter, got %v", fake.gotFilters)
	}
}

// Re-running a branch leaves older exited containers behind; the table must show
// the current run, not the first one ever started.
func TestStatusKeepsMostRecentContainerPerBranch(t *testing.T) {
	now := time.Now()
	old := container("feature-a", "exited", now.Add(-2*time.Hour), now.Add(-90*time.Minute))
	old.ID = "older"
	cur := container("feature-a", "running", now.Add(-5*time.Minute), time.Time{})
	cur.ID = "current"

	// Newest first, as the docker backend returns them.
	fake := &fakeInspector{containers: []runtime.ContainerInfo{cur, old}}
	r := &Runner{Inspector: fake, Repo: "/repo", RepoID: testRepoID}

	got, err := r.Status(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one branch line, got %d: %+v", len(got), got)
	}
	if got[0].Container.ID != "current" {
		t.Errorf("expected the most recent container, got %s", got[0].Container.ID)
	}
	if !got[0].Running() {
		t.Error("branch should report as running")
	}
}

// A container with no branch label is a plain `sandbox-cli run`, not a fleet
// agent; it must not appear as a nameless row.
func TestStatusIgnoresUnlabelledContainers(t *testing.T) {
	c := container("", "running", time.Now(), time.Time{})
	delete(c.Labels, sandbox.LabelBranch)
	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: "/repo", RepoID: testRepoID}

	got, err := r.Status(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no rows, got %+v", got)
	}
}

func TestStatusStateAndElapsed(t *testing.T) {
	now := time.Now()

	// Running: elapsed counts up to now.
	running := elapsed(ptr(container("a", "running", now.Add(-3*time.Minute), time.Time{})), now)
	if running < 2*time.Minute || running > 4*time.Minute {
		t.Errorf("running elapsed = %v, want ~3m", running)
	}

	// Exited: elapsed is how long it actually ran, not how long ago it finished.
	ex := container("b", "exited", now.Add(-time.Hour), now.Add(-30*time.Minute))
	if got := elapsed(ptr(ex), now); got != 30*time.Minute {
		t.Errorf("exited elapsed = %v, want 30m", got)
	}

	// Never started, and no container at all.
	if got := elapsed(ptr(container("c", "created", time.Time{}, time.Time{})), now); got != 0 {
		t.Errorf("unstarted elapsed = %v, want 0", got)
	}
	if got := elapsed(nil, now); got != 0 {
		t.Errorf("nil container elapsed = %v, want 0", got)
	}

	// A branch with no container reports a placeholder rather than an empty cell.
	if got := (Status{Branch: "x"}).State(); got != "—" {
		t.Errorf("State() with no container = %q", got)
	}
}

func ptr(c runtime.ContainerInfo) *runtime.ContainerInfo { return &c }
