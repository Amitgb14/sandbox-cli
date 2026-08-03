package fleet

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

func testOptions(t *testing.T, cfg config.Config, task Task) sandbox.Options {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // never touch the real persisted HOME
	agent, ok := agents.Lookup("claude")
	if !ok {
		t.Fatal("no claude descriptor")
	}
	r := &Runner{Session: sandbox.New(cfg), Repo: "/repo", RepoID: testRepoID}
	opts, err := r.options(Spec{Agent: "claude"}, LaunchOptions{}, agent, task, t.TempDir(), "main")
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	return opts
}

// The credential leak this guards is the substantive one prod exists to prevent:
// the default auth path is an OAuth refresh token in the persisted HOME, readable
// by the agent, and prod's answer is not to mount it at all. BuildSpec mounts
// AuthPersistDir whenever it is non-empty and does not re-consult the config, so
// every path that builds Options has to apply the gate itself — and ValidateProfile
// cannot stand in for this one, because it validates the resolved Config while the
// leak would be in the Options.
func TestOptionsDoNotPersistAuthWhenTheProfileForbidsIt(t *testing.T) {
	cfg := config.Default()
	cfg.PersistAuth = new(bool) // *false — what profileBase sets under prod

	if got := testOptions(t, cfg, Task{Branch: "feature-a", Prompt: "x"}).AuthPersistDir; got != "" {
		t.Errorf("AuthPersistDir = %q with persist_auth off; the refresh token must not be mounted", got)
	}
}

// The positive twin: with persistence on, a detached agent must get its login or
// it starts, finds itself logged out, and dies with nobody watching.
func TestOptionsPersistAuthByDefault(t *testing.T) {
	if got := testOptions(t, config.Default(), Task{Branch: "feature-a", Prompt: "x"}).AuthPersistDir; got == "" {
		t.Error("AuthPersistDir is empty by default; a detached agent would start logged out")
	}
}

// A fleet container must be findable as a fleet container. Without this label
// `fleet stop --all` reaches an interactive detached session, `fleet clean` reaps
// it, and max_parallel counts it — one open `sandbox-cli claude --detach` would
// then block a max_parallel: 1 fleet forever on a slot that never frees.
func TestOptionsMarkTheContainerAsFleetOwned(t *testing.T) {
	if !testOptions(t, config.Default(), Task{Branch: "feature-a", Prompt: "x"}).Fleet {
		t.Error("fleet containers are not marked as fleet-owned")
	}
}

// The per-task agent has to reach the container, not just the validation: the
// argv it starts, the label `fleet status` reads back, and the persisted HOME it
// logs in through must all be that agent's rather than the fleet-wide one.
func TestOptionsUseThePerTaskAgent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	codex, ok := agents.Lookup("codex")
	if !ok {
		t.Skip("no codex descriptor")
	}
	spec := Spec{Agent: "claude", Tasks: []Task{{Branch: "feature-b", Prompt: "do it", Agent: "codex"}}}

	r := &Runner{Session: sandbox.New(config.Default()), Repo: "/repo", RepoID: testRepoID}
	opts, err := r.options(spec, LaunchOptions{}, codex, spec.Tasks[0], t.TempDir(), "main")
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if opts.Agent != "codex" {
		t.Errorf("container label says %q; `fleet status` would name the wrong agent", opts.Agent)
	}
	if len(opts.Command) == 0 || opts.Command[0] != "codex" {
		t.Errorf("argv should start codex, got %v", opts.Command)
	}
	// The persisted HOME is per agent, so a task running codex must not be handed
	// claude's login directory.
	if !strings.Contains(opts.AuthPersistDir, codex.PersistDir) {
		t.Errorf("AuthPersistDir = %q, want codex's own", opts.AuthPersistDir)
	}
}

// Per-task limits must reach docker, or the key is decorative.
func TestOptionsApplyPerTaskLimits(t *testing.T) {
	spec := Spec{
		Agent:    "claude",
		Defaults: Defaults{Memory: "4g", CPUs: "2", Allow: []string{"example.com"}},
		Tasks:    []Task{{Branch: "big", Prompt: "p", Memory: "16g", Allow: []string{"docs.example.com"}}},
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	agent, ok := agents.Lookup("claude")
	if !ok {
		t.Fatal("no claude descriptor")
	}
	r := &Runner{Session: sandbox.New(config.Default()), Repo: "/repo", RepoID: testRepoID}
	opts, err := r.options(spec, LaunchOptions{}, agent, spec.Tasks[0], t.TempDir(), "main")
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if opts.Memory != "16g" {
		t.Errorf("memory = %q, want the task's 16g", opts.Memory)
	}
	if opts.CPUs != "2" {
		t.Errorf("cpus = %q, want the inherited default", opts.CPUs)
	}
	if len(opts.Allow) != 2 {
		t.Errorf("allow should be the union of fleet and task, got %v", opts.Allow)
	}
}

// Docker's duplicate-name refusal is what enforces one agent per branch, so a
// finished container holding the name is a decision this tool made — not a
// docker malfunction. Left to the engine it surfaces as `Conflict. The container
// name "/sandbox-<repo>-<branch>" is already in use by container "<64 hex>"`,
// which names neither the branch, nor the fleet, nor the command that clears it.
func TestLaunchRefusesWhenAFinishedContainerHoldsTheName(t *testing.T) {
	now := time.Now()
	done := container("feature-a", "exited", now.Add(-time.Hour), now)
	done.ExitCode = 1
	r, _ := testRunner(done)

	res := r.launchOne(context.Background(), Spec{Agent: "claude"}, LaunchOptions{},
		Task{Branch: "feature-a", Prompt: "do it"}, "main", false)
	if res.Err == nil {
		t.Fatal("expected a refusal, got none")
	}
	for _, want := range []string{"feature-a", "fleet logs", "fleet clean"} {
		if !strings.Contains(res.Err.Error(), want) {
			t.Errorf("refusal must mention %q, got: %v", want, res.Err)
		}
	}
}

// But it must not fire under --resume, which is the caller having already said
// "retry this one". unfinished() selects a task precisely *because* its last
// container exited non-zero — a failed verify above all — so a refusal here would
// leave --resume working only for tasks whose containers had already been reaped:
// the one set it was not written for.
func TestResumeReapsTheContainerHoldingTheBranchName(t *testing.T) {
	now := time.Now()
	done := container("feature-a", "exited", now.Add(-time.Hour), now)
	done.ExitCode = VerifyFailedExit
	r, ctl := testRunner(done)

	res := r.launchOne(context.Background(), Spec{Agent: "claude"}, LaunchOptions{Resume: true},
		Task{Branch: "feature-a", Prompt: "do it"}, "main", false)
	if res.Err != nil && strings.Contains(res.Err.Error(), "holding its name") {
		t.Errorf("--resume was refused by the name guard it exists to get past: %v", res.Err)
	}
	if len(ctl.removed) != 1 || ctl.removed[0] != done.ID {
		t.Errorf("--resume did not reap the container holding the name, removed %v", ctl.removed)
	}
}

// A backend that cannot remove containers has nothing to reap with, so the
// refusal stands rather than being skipped: --resume must not become a way to
// reach docker's raw name conflict.
func TestResumeStillRefusesWithoutAController(t *testing.T) {
	now := time.Now()
	done := container("feature-a", "exited", now.Add(-time.Hour), now)
	done.ExitCode = VerifyFailedExit
	r, _ := testRunner(done)
	r.Controller = nil

	res := r.launchOne(context.Background(), Spec{Agent: "claude"}, LaunchOptions{Resume: true},
		Task{Branch: "feature-a", Prompt: "do it"}, "main", false)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "holding its name") {
		t.Errorf("expected the name refusal with no controller, got: %v", res.Err)
	}
}

// And the plan must agree with the run about it. NameHeldBy exists to stop a
// dry-run saying "will start" and the run then refusing; reporting a refusal
// --resume will not make reopens the same gap from the other side.
func TestPlanDoesNotReportANameRefusalResumeWillNotMake(t *testing.T) {
	now := time.Now()
	done := container("feature-a", "exited", now.Add(-time.Hour), now)
	done.ExitCode = VerifyFailedExit
	r, _ := testRunner(done)
	r.Repo = newTestRepo(t)
	gitIn(t, r.Repo, "branch", "feature-a")

	spec := Spec{Agent: "claude", Tasks: []Task{{Branch: "feature-a", Prompt: "do it"}}}
	plans, err := r.Plan(context.Background(), spec, LaunchOptions{Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if plans[0].NameHeldBy != "" {
		t.Errorf("plan reports a name conflict --resume will reap, got %q", plans[0].NameHeldBy)
	}
}

// The plan must also say *which* holder it found, because the way out differs and
// naming the wrong command turns the refusal into a dead end: `fleet clean`
// filters on the fleet label, so it will never clear an interactive session.
// Caught by running it — the dry-run called a running interactive container "a
// finished container" and sent the user to `fleet clean`, twice wrong.
func TestPlanSeparatesAFleetNameHolderFromAnInteractiveOne(t *testing.T) {
	now := time.Now()
	repo := newTestRepo(t)
	gitIn(t, repo, "branch", "feature-a")
	spec := Spec{Agent: "claude", Tasks: []Task{{Branch: "feature-a", Prompt: "do it"}}}

	for _, tc := range []struct {
		name      string
		c         runtime.ContainerInfo
		wantFleet bool
	}{
		{"fleet's own stale container", container("feature-a", "exited", now.Add(-time.Hour), now), true},
		{"interactive session", interactiveContainer("feature-a", "running", now, time.Time{}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := testRunner(tc.c)
			r.Repo = repo
			plans, err := r.Plan(context.Background(), spec, LaunchOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if plans[0].NameHeldBy != tc.c.Name {
				t.Fatalf("NameHeldBy = %q, want %q", plans[0].NameHeldBy, tc.c.Name)
			}
			if plans[0].NameHeldByFleet != tc.wantFleet {
				t.Errorf("NameHeldByFleet = %v, want %v", plans[0].NameHeldByFleet, tc.wantFleet)
			}
		})
	}
}

// And it must not fire on a branch that has never run, or every first launch
// would refuse.
func TestLaunchProceedsWhenNoContainerHoldsTheName(t *testing.T) {
	r, _ := testRunner()
	res := r.launchOne(context.Background(), Spec{Agent: "claude"}, LaunchOptions{},
		Task{Branch: "fresh-branch", Prompt: "do it"}, "main", false)
	// It gets past the name check and fails later, on the worktree, because this
	// runner has no real repository. Both halves are asserted: that it failed at
	// all, and *which* refusal it was. Checking only the second would keep passing
	// if launchOne ever started succeeding here for some unrelated reason, which is
	// the way a guard test quietly stops testing its guard.
	if res.Err == nil {
		t.Fatal("expected the launch to fail at the worktree, got no error")
	}
	if strings.Contains(res.Err.Error(), "holding its name") {
		t.Errorf("refused a branch with no container: %v", res.Err)
	}
}

// Docker's name namespace does not know about labels, so the branch's name can
// be held by a session the fleet did not start — an interactive `--detach` on a
// branch that is also in the fleet file. Every lookup in this package filters on
// sandbox.fleet, correctly, which left that case falling through to docker's raw
// conflict: the hole in the "it explains itself" promise, and the case a user is
// least likely to work out unaided.
func TestLaunchExplainsAnInteractiveSessionHoldingTheName(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name  string
		state string
		want  string
	}{
		{"finished", "exited", "sandbox-cli clean"},
		{"running", "running", "sandbox-cli kill"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := interactiveContainer("feature-a", tc.state, now.Add(-time.Hour), now)
			r, ctl := testRunner(held)

			res := r.launchOne(context.Background(), Spec{Agent: "claude"}, LaunchOptions{},
				Task{Branch: "feature-a", Prompt: "do it"}, "main", false)
			if res.Err == nil {
				t.Fatal("expected a refusal, got none")
			}
			for _, want := range []string{"feature-a", "interactive session", tc.want} {
				if !strings.Contains(res.Err.Error(), want) {
					t.Errorf("refusal must mention %q, got: %v", want, res.Err)
				}
			}
			if len(ctl.removed) != 0 {
				t.Errorf("reaped a session the fleet did not start: %v", ctl.removed)
			}
		})
	}
}

// And --resume must not reach it either. Reaping the fleet's own stale container
// is the caller retrying their own run; reaping someone's interactive session is
// the fleet acting outside what it started, which is the whole reason the fleet
// label exists.
func TestResumeDoesNotReapAnInteractiveSession(t *testing.T) {
	now := time.Now()
	held := interactiveContainer("feature-a", "exited", now.Add(-time.Hour), now)
	r, ctl := testRunner(held)

	res := r.launchOne(context.Background(), Spec{Agent: "claude"}, LaunchOptions{Resume: true},
		Task{Branch: "feature-a", Prompt: "do it"}, "main", false)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "interactive session") {
		t.Errorf("expected the interactive refusal even under --resume, got: %v", res.Err)
	}
	if len(ctl.removed) != 0 {
		t.Errorf("--resume reaped a session the fleet did not start: %v", ctl.removed)
	}
}
