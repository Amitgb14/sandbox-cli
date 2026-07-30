package fleet

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

func threeTasks() Spec {
	return Spec{Agent: "claude", Tasks: []Task{
		{Branch: "a", Prompt: "do a"},
		{Branch: "b", Prompt: "do b"},
		{Branch: "c", Prompt: "do c"},
	}}
}

func TestOnlyKeepsTheNamedBranchesInFileOrder(t *testing.T) {
	// Named out of order on purpose: max_parallel schedules in file order, so a
	// --only that reordered the fleet would be a different run.
	got, err := only(threeTasks().Tasks, []string{"c", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Branch != "a" || got[1].Branch != "c" {
		t.Errorf("--only should keep file order, got %v", branchesOf(got))
	}
}

// A --only that matches nothing must not look like a fleet that ran perfectly.
func TestOnlyRejectsAnUnknownBranch(t *testing.T) {
	_, err := only(threeTasks().Tasks, []string{"a", "typo"})
	if err == nil {
		t.Fatal("expected an error for a branch the file does not have")
	}
	if !strings.Contains(err.Error(), "typo") || !strings.Contains(err.Error(), "a, b, c") {
		t.Errorf("error should name the typo and list what exists, got: %v", err)
	}
}

// Resume is what makes an interrupted `fleet run` recoverable: the agents that
// are still working are left alone, the ones that finished cleanly are not
// re-done, and everything else is started.
func TestResumeSkipsRunningAndSucceeded(t *testing.T) {
	now := time.Now()
	running := container("a", "running", now.Add(-time.Minute), time.Time{})
	passed := container("b", "exited", now.Add(-time.Hour), now.Add(-time.Minute))
	passed.ExitCode = 0
	failed := container("c", "exited", now.Add(-time.Hour), now.Add(-time.Minute))
	failed.ExitCode = VerifyFailedExit

	r, _ := testRunner(running, passed, failed)
	got, err := r.selectTasks(context.Background(), threeTasks(), LaunchOptions{Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Branch != "c" {
		t.Errorf("resume should retry only the branch that failed its verify, got %v", branchesOf(got))
	}
}

// A branch that was never launched is not "already done": resume has to start
// it, or an interrupted fan-out never finishes.
func TestResumeStartsBranchesWithNoContainer(t *testing.T) {
	r, _ := testRunner()
	got, err := r.selectTasks(context.Background(), threeTasks(), LaunchOptions{Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("expected all three tasks, got %v", branchesOf(got))
	}
}

func TestOnlyAndResumeCompose(t *testing.T) {
	now := time.Now()
	passed := container("a", "exited", now.Add(-time.Hour), now)
	passed.ExitCode = 0
	r, _ := testRunner(passed)

	got, err := r.selectTasks(context.Background(), threeTasks(), LaunchOptions{Only: []string{"a", "b"}, Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Branch != "b" {
		t.Errorf("expected only b (a already passed), got %v", branchesOf(got))
	}
}

// The slot count is the regression this package documents everywhere else and
// did not enforce here: max_parallel must count only the fleet's own containers,
// or one open `sandbox-cli claude --detach` session in the same repository holds
// a slot it never frees and a max_parallel: 1 fleet waits behind it forever.
func TestSlotCountIgnoresInteractiveSessions(t *testing.T) {
	now := time.Now()
	interactive := container("someones-branch", "running", now, time.Time{})
	delete(interactive.Labels, sandbox.LabelFleet)

	r, _ := testRunner(interactive)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// With the interactive session counted, this blocks until the deadline.
	if err := r.waitForSlot(ctx, 1); err != nil {
		t.Fatalf("waitForSlot blocked on an interactive session: %v", err)
	}
}

func TestSlotCountStillWaitsOnFleetContainers(t *testing.T) {
	now := time.Now()
	r, _ := testRunner(container("a", "running", now, time.Time{}))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := r.waitForSlot(ctx, 1); err == nil {
		t.Error("a full fleet should wait for a slot rather than launching over its cap")
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"4g":   4 << 30,
		"4G":   4 << 30,
		"4gb":  4 << 30,
		"512m": 512 << 20,
		"1024": 1024,
		"1.5g": 1536 << 20,
	}
	for in, want := range cases {
		got, err := parseSize(in)
		if err != nil {
			t.Errorf("parseSize(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
	if _, err := parseSize("lots"); err == nil {
		t.Error("expected an error for a value docker would not accept either")
	}
}

// fakeSizer stands in for a runtime backend that can report host memory.
type fakeSizer struct {
	*runtime.DockerCLI
	bytes int64
	known bool
}

func (f fakeSizer) HostMemoryBytes(context.Context) (int64, bool) { return f.bytes, f.known }

func capacityRunner(t *testing.T, hostBytes int64, known bool) *Runner {
	t.Helper()
	sess := sandbox.New(config.Default())
	sess.Runtime = fakeSizer{bytes: hostBytes, known: known}
	return &Runner{Session: sess, Inspector: &fakeInspector{}, Repo: "/repo", RepoID: testRepoID, Out: io.Discard}
}

// The arithmetic that matters is the *concurrent* demand, not the task count —
// bounding concurrency is exactly what max_parallel is for, so a long fleet on a
// small machine is an ordinary thing to run.
func TestCapacityCountsConcurrentAgentsNotTasks(t *testing.T) {
	spec := Spec{Agent: "claude", MaxParallel: 2, Defaults: Defaults{Memory: "4g"}}
	for i := 0; i < 20; i++ {
		spec.Tasks = append(spec.Tasks, Task{Branch: string(rune('a' + i)), Prompt: "p"})
	}
	r := capacityRunner(t, 16<<30, true)
	if err := r.checkCapacity(context.Background(), spec, len(spec.Tasks)); err != nil {
		t.Errorf("20 tasks at max_parallel 2 need 8g of 16g; refused anyway: %v", err)
	}
}

func TestCapacityRefusesAnUnrunnableFleet(t *testing.T) {
	spec := Spec{Agent: "claude", Defaults: Defaults{Memory: "8g"}, Tasks: []Task{
		{Branch: "a", Prompt: "p"}, {Branch: "b", Prompt: "p"}, {Branch: "c", Prompt: "p"},
	}}
	r := capacityRunner(t, 8<<30, true)
	err := r.checkCapacity(context.Background(), spec, len(spec.Tasks))
	if err == nil {
		t.Fatal("24g of agents on an 8g machine should be refused before anything starts")
	}
	// The message has to carry the arithmetic and both ways out, or it is just a
	// refusal to argue with.
	for _, want := range []string{"24g", "8g", "max_parallel"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q: %v", want, err)
		}
	}
}

// A task that raises its own memory is the case an average would get wrong.
func TestCapacityUsesTheWidestPerTaskCap(t *testing.T) {
	spec := Spec{Agent: "claude", MaxParallel: 2, Defaults: Defaults{Memory: "1g"}, Tasks: []Task{
		{Branch: "a", Prompt: "p"},
		{Branch: "b", Prompt: "p", Memory: "12g"},
	}}
	r := capacityRunner(t, 16<<30, true)
	if err := r.checkCapacity(context.Background(), spec, len(spec.Tasks)); err == nil {
		t.Error("2 × the widest cap (12g) is 24g and does not fit in 16g")
	}
}

// Resource sanity is not a boundary control: a host that cannot be measured runs
// the fleet rather than refusing it.
func TestCapacityProceedsWhenTheHostCannotBeMeasured(t *testing.T) {
	spec := Spec{Agent: "claude", Defaults: Defaults{Memory: "64g"}, Tasks: []Task{{Branch: "a", Prompt: "p"}}}
	r := capacityRunner(t, 0, false)
	if err := r.checkCapacity(context.Background(), spec, 1); err != nil {
		t.Errorf("an unmeasurable host must not refuse the fleet: %v", err)
	}
}

// `memory: "0"` is a deliberate opt-out of the cap, so there is no arithmetic to
// do — and nothing to refuse.
func TestCapacitySaysNothingIsCapped(t *testing.T) {
	spec := Spec{Agent: "claude", Defaults: Defaults{Memory: ""}, Tasks: []Task{{Branch: "a", Prompt: "p"}}}
	r := capacityRunner(t, 1<<30, true)
	if err := r.checkCapacity(context.Background(), spec, 1); err != nil {
		t.Errorf("an uncapped fleet has no footprint to check: %v", err)
	}
}

func branchesOf(ts []Task) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Branch)
	}
	return out
}
