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
			sandbox.LabelFleet:  "1",
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

	// A branch with no container has no elapsed time to report. How that renders
	// is the CLI's business (cli.fleetState), not this package's.
	if got := (Status{Branch: "x"}).Running(); got {
		t.Error("a branch with no container reports Running")
	}
}

func ptr(c runtime.ContainerInfo) *runtime.ContainerInfo { return &c }

// The distinction the VERIFY column exists for: `exited 0` is the same code for
// "its check passed" and "nothing checked it", and this repository's own first
// fleet run read the second as the first — on a branch whose agent had died on
// an API error and whose verify said yes unconditionally.
func TestVerifyStateSeparatesPassedFromUnchecked(t *testing.T) {
	now := time.Now()
	withVerify := func(c runtime.ContainerInfo, code int) runtime.ContainerInfo {
		c.Labels[sandbox.LabelVerify] = "go test ./..."
		c.ExitCode = code
		return c
	}
	exited := func(branch string) runtime.ContainerInfo {
		return container(branch, "exited", now.Add(-time.Hour), now)
	}

	cases := []struct {
		name string
		in   *runtime.ContainerInfo
		want VerifyState
	}{
		{"no container to ask", nil, VerifyUnknown},
		{"ran with no verify declared", ptr(exited("a")), VerifyNone},
		{"declared and passed", ptr(withVerify(exited("b"), 0)), VerifyPassed},
		{"declared and failed", ptr(withVerify(exited("c"), VerifyFailedExit)), VerifyFailed},
		{"died before its verify", ptr(withVerify(exited("d"), 137)), VerifyUnchecked},
		{"still running", ptr(withVerify(container("e", "running", now, time.Time{}), 0)), VerifyPending},
	}
	for _, tc := range cases {
		if got := verifyState(tc.in); got != tc.want {
			t.Errorf("%s: verifyState = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The column is only useful if Status actually fills it in.
func TestStatusReportsVerifyState(t *testing.T) {
	now := time.Now()
	c := container("feature-a", "exited", now.Add(-time.Hour), now)
	c.Labels[sandbox.LabelVerify] = "make test"
	c.ExitCode = VerifyFailedExit

	r := &Runner{Inspector: &fakeInspector{containers: []runtime.ContainerInfo{c}}, Repo: "/repo", RepoID: testRepoID}
	rows, err := r.Status(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	if rows[0].Verify != VerifyFailed {
		t.Errorf("Verify = %q, want %q", rows[0].Verify, VerifyFailed)
	}
}
