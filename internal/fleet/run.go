package fleet

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// Runner launches and inspects the containers of one fleet, for one repository.
type Runner struct {
	// Session is the configured sandbox session every task is started through, so
	// fleet containers get the same boundary as interactive ones.
	Session *sandbox.Session
	// Inspector finds containers this repository already has running.
	Inspector runtime.Inspector
	// Controller stops, reaps and reads the logs of those containers. Optional:
	// only the commands that act on existing containers need it.
	Controller runtime.Controller
	// Repo is the absolute path of the main repository (not a worktree). This is
	// what git is asked about — worktrees, branches, commits.
	Repo string
	// RepoID is worktree.RepoID for that path: the stable identity every container
	// of this project is labelled with. Two identities rather than one because
	// they answer different questions, and using the path as the label would give
	// two clones of a same-named repo one label namespace.
	RepoID string
	// Out receives progress lines; nil means os.Stderr.
	Out io.Writer
}

// LaunchResult is the outcome of one task's launch. A failed task carries Err
// and does not stop the rest of the fleet: with several independent agents, one
// bad branch should not cost you the other launches.
type LaunchResult struct {
	Branch       string
	WorktreePath string
	ContainerID  string
	Err          error
}

// slotPoll is how often Launch re-checks for a free slot when max_parallel caps
// the fleet. Agent runs last minutes, so a slow poll costs nothing and keeps the
// docker calls negligible.
const slotPoll = 2 * time.Second

// Launch starts every task in spec, honoring max_parallel.
//
// With max_parallel unset (or >= the task count) this returns as soon as the
// containers are started — the agents keep running in the background. With a
// lower cap it must stay attached, waiting for a container to exit before
// starting the next task; the caller's context cancels that wait.
func (r *Runner) Launch(ctx context.Context, spec Spec, forceBuild bool) ([]LaunchResult, error) {
	agent, ok := agents.Lookup(spec.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", spec.Agent)
	}
	if r.Repo == "" {
		return nil, fmt.Errorf("fleet needs a git repository: run it from inside one")
	}

	// The branch the fleet is expected to land on, resolved once and stamped on
	// every container. Read here rather than at land time because this is the
	// moment it is still true: the user launched a fleet from a checkout that was
	// on some branch, and that is what the work is for. Empty (a detached HEAD)
	// records nothing rather than guessing, and land falls back to its old
	// behavior for those.
	base := worktree.HeadBranch(r.Repo)

	results := make([]LaunchResult, 0, len(spec.Tasks))
	for i, task := range spec.Tasks {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if spec.MaxParallel > 0 {
			if err := r.waitForSlot(ctx, spec.MaxParallel); err != nil {
				return results, err
			}
		}
		// forceBuild on the first task only. Session.Start already builds the image
		// before it launches anything — deliberately, so a fan-out against a cold
		// image does not trigger one concurrent build per container — so the fleet
		// needs no build step of its own. What it must not do is ask for a *rebuild*
		// N times: the launches are sequential, so the first one leaves an image the
		// rest reuse.
		results = append(results, r.launchOne(ctx, spec, agent, task, base, forceBuild && i == 0))
	}
	return results, nil
}

// launchOne resolves a task's worktree and starts its container.
func (r *Runner) launchOne(ctx context.Context, spec Spec, agent agents.Descriptor, task Task, base string, forceBuild bool) LaunchResult {
	res := LaunchResult{Branch: task.Branch}

	// One agent per worktree. Two agents editing one checkout is silent data
	// loss, so a branch that is already working is refused rather than joined.
	busy, err := r.runningFor(ctx, task.Branch)
	if err != nil {
		res.Err = err
		return res
	}
	if busy != nil {
		res.Err = fmt.Errorf("an agent is already running on %q (container %s); "+
			"stop it with `sandbox-cli fleet stop %s` or wait for it to finish",
			task.Branch, busy.Name, task.Branch)
		return res
	}

	info, err := worktree.Resolve(r.Repo, task.Branch)
	if err != nil {
		res.Err = err
		return res
	}
	res.WorktreePath = info.Path

	opts, err := r.options(spec, agent, task, info.Path, base)
	if err != nil {
		res.Err = err
		return res
	}
	id, err := r.Session.Start(ctx, opts, forceBuild)
	if err != nil {
		res.Err = err
		return res
	}
	res.ContainerID = id

	verb := "reusing"
	if info.Created {
		verb = "created"
	}
	r.logf("started %s on %s (%s worktree %s)", short(id), task.Branch, verb, info.Path)
	return res
}

// options turns a task into the sandbox options for its container. Everything
// that defines the boundary comes from the same place an interactive run gets it;
// fleet only adds the branch, the labels and Detach.
func (r *Runner) options(spec Spec, agent agents.Descriptor, task Task, worktreePath, base string) (sandbox.Options, error) {
	opts := sandbox.Options{
		Project: worktreePath,
		Detach:  true,
		RepoID:  r.RepoID,
		Branch:  task.Branch,
		Agent:   agent.Name,
		Base:    base,
		Verify:  task.Verify,
		Command: withVerify(agent.Autonomous(task.Prompt, task.Args), task.Verify),

		EnvAllow:    agent.EnvAllow,
		Memory:      spec.Defaults.Memory,
		CPUs:        spec.Defaults.CPUs,
		Allow:       spec.Defaults.Allow,
		Cache:       spec.Defaults.Cache,
		GitIdentity: spec.Defaults.Git,

		// A fleet task always runs in a linked worktree, so the parent repo's .git
		// must come along or the agent can edit files it can never commit.
		ExtraMounts: sandbox.LinkedWorktreeMounts(worktreePath),
	}

	// Persist the agent's login, exactly as the interactive wrapper does. Without
	// it a detached agent starts, finds itself logged out, and dies unattended.
	if dir := config.AgentStateDir(agent.PersistDir); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return sandbox.Options{}, fmt.Errorf("creating auth persist dir %s: %w", dir, err)
		}
		opts.AuthPersistDir = dir
	}
	return opts, nil
}

// runningFor returns the running container working branch, or nil.
func (r *Runner) runningFor(ctx context.Context, branch string) (*runtime.ContainerInfo, error) {
	infos, err := r.Inspector.Containers(ctx, map[string]string{
		sandbox.LabelCLI:    "1",
		sandbox.LabelRepo:   r.RepoID,
		sandbox.LabelBranch: branch,
	})
	if err != nil {
		return nil, err
	}
	for i := range infos {
		if infos[i].Running() {
			return &infos[i], nil
		}
	}
	return nil, nil
}

// waitForSlot blocks until fewer than max of this repository's containers are
// running.
func (r *Runner) waitForSlot(ctx context.Context, max int) error {
	announced := false
	for {
		infos, err := r.Inspector.Containers(ctx, map[string]string{
			sandbox.LabelCLI:  "1",
			sandbox.LabelRepo: r.RepoID,
		})
		if err != nil {
			return err
		}
		running := 0
		for _, c := range infos {
			if c.Running() {
				running++
			}
		}
		if running < max {
			return nil
		}
		if !announced {
			r.logf("%d/%d agents running; waiting for a free slot", running, max)
			announced = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(slotPoll):
		}
	}
}

func (r *Runner) logf(format string, args ...any) {
	w := r.Out
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "sandbox-cli: "+format+"\n", args...)
}

// short truncates a container id to the 12 characters docker itself displays.
func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
