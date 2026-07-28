package fleet

import (
	"context"
	"fmt"
	"io"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// Planned is what a task would do, without doing it. Used by `fleet run
// --dry-run`.
//
// It deliberately does not render a docker command line the way `run --dry-run`
// does: a task's worktree may not exist yet, and building a spec against a
// directory that is not there would either fail or — worse — quietly render a
// mount source that is not the one the real run will use.
type Planned struct {
	Branch         string
	WorktreePath   string
	WorktreeExists bool // false => the run would create it
	Command        []string
	Memory, CPUs   string
	Allow          []string
	Labels         map[string]string
	AlreadyRunning bool   // an agent already holds this branch; the run would refuse
	RunningInName  string // that agent's container name
}

// Plan resolves what Launch would do for each task. It touches nothing.
func (r *Runner) Plan(ctx context.Context, spec Spec) ([]Planned, error) {
	agent, ok := agents.Lookup(spec.Agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", spec.Agent)
	}
	if r.Repo == "" {
		return nil, fmt.Errorf("fleet needs a git repository: run it from inside one")
	}

	base := worktree.HeadBranch(r.Repo)

	out := make([]Planned, 0, len(spec.Tasks))
	for _, t := range spec.Tasks {
		path, exists, err := worktree.Path(r.Repo, t.Branch)
		if err != nil {
			return nil, err
		}
		p := Planned{
			Branch:         t.Branch,
			WorktreePath:   path,
			WorktreeExists: exists,
			Command:        withVerify(agent.Autonomous(t.Prompt, t.Args), t.Verify),
			Memory:         spec.Defaults.Memory,
			CPUs:           spec.Defaults.CPUs,
			Allow:          spec.Defaults.Allow,
			Labels: map[string]string{
				sandbox.LabelCLI:    "1",
				sandbox.LabelFleet:  "1",
				sandbox.LabelRepo:   r.RepoID,
				sandbox.LabelBranch: t.Branch,
				sandbox.LabelAgent:  agent.Name,
			},
		}
		// Omitted when empty, exactly as containerLabels does it, so the plan shows
		// the labels the run will actually carry rather than a blank one.
		if base != "" {
			p.Labels[sandbox.LabelBase] = base
		}
		if t.Verify != "" {
			p.Labels[sandbox.LabelVerify] = t.Verify
		}
		if busy, err := r.runningFor(ctx, t.Branch); err == nil && busy != nil {
			p.AlreadyRunning = true
			p.RunningInName = busy.Name
		}
		out = append(out, p)
	}
	return out, nil
}

// Logs streams the output of the most recent container for branch.
func (r *Runner) Logs(ctx context.Context, branch string, follow bool, stdout, stderr io.Writer) error {
	c, err := r.latestFor(ctx, branch)
	if err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("no container for branch %q (has it been run, or already cleaned?)", branch)
	}
	if r.Controller == nil {
		return errNoController
	}
	return r.Controller.Logs(ctx, c.ID, follow, stdout, stderr)
}

// Stop stops the running agent on branch, or every running agent in this
// repository when branch is empty. It returns the branches actually stopped.
func (r *Runner) Stop(ctx context.Context, branch string) ([]string, error) {
	if r.Controller == nil {
		return nil, errNoController
	}
	infos, err := r.containers(ctx, branch)
	if err != nil {
		return nil, err
	}
	var stopped []string
	for _, c := range infos {
		if !c.Running() {
			continue
		}
		if err := r.Controller.Stop(ctx, c.ID); err != nil {
			return stopped, err
		}
		// Report the container name when there is no branch label. Appending the
		// label unguarded printed a bare "stopped " line for a container the caller
		// then had no way to identify.
		if b := c.Labels[sandbox.LabelBranch]; b != "" {
			stopped = append(stopped, b)
		} else {
			stopped = append(stopped, c.Name)
		}
	}
	return stopped, nil
}

// CleanResult reports what Clean reaped and what it deliberately left alone.
type CleanResult struct {
	Containers []string // branches whose exited containers were removed
	Worktrees  []string // branches whose worktrees were removed
	Kept       []string // human-readable reasons something was not removed
}

// Clean removes the exited containers of this repository's fleet, and — only
// when worktrees is set — their checkouts too.
//
// Containers are safe to reap: they hold logs and an exit code, both of which
// the user has had the chance to read. Worktrees are not: uncommitted work in
// one exists nowhere else, so a dirty worktree is always kept and reported
// rather than removed, and a branch whose agent is still running is never
// touched at all.
func (r *Runner) Clean(ctx context.Context, branch string, worktrees bool) (CleanResult, error) {
	var res CleanResult
	if r.Controller == nil {
		return res, errNoController
	}
	infos, err := r.containers(ctx, branch)
	if err != nil {
		return res, err
	}

	// Branches with a live agent are off limits, including for the worktree pass.
	running := map[string]bool{}
	for _, c := range infos {
		if c.Running() {
			b := c.Labels[sandbox.LabelBranch]
			running[b] = true
			res.Kept = append(res.Kept, fmt.Sprintf("%s: agent still running (%s)", b, c.Name))
		}
	}

	reaped := map[string]bool{}
	for _, c := range infos {
		if c.Running() {
			continue
		}
		if err := r.Controller.Remove(ctx, c.ID); err != nil {
			return res, err
		}
		b := c.Labels[sandbox.LabelBranch]
		if b != "" && !reaped[b] {
			reaped[b] = true
			res.Containers = append(res.Containers, b)
		}
	}
	if !worktrees {
		return res, nil
	}

	for _, b := range r.worktreeBranches(branch, infos) {
		if running[b] {
			continue // already reported above
		}
		if dirty := worktree.Dirty(r.Repo, b, 1); len(dirty) > 0 {
			res.Kept = append(res.Kept, fmt.Sprintf(
				"%s: worktree has uncommitted work; commit it or use `worktree rm --force %s`", b, b))
			continue
		}
		if err := worktree.Remove(r.Repo, b, false); err != nil {
			res.Kept = append(res.Kept, fmt.Sprintf("%s: %v", b, err))
			continue
		}
		res.Worktrees = append(res.Worktrees, b)
	}
	return res, nil
}

// worktreeBranches lists the sandbox worktrees this *fleet* created, narrowed to
// one branch when given.
//
// Intersected with the fleet's own containers rather than taken from
// worktree.List alone. `worktree list` reports every sandbox-managed checkout of
// the repository, including ones an ordinary `--worktree` run made, and removing
// those is not what `fleet clean --worktrees` promises. Committed work survives on
// the branch either way, so this was never data loss — but a command that removes
// more than its name says will eventually be run by someone who believed the name.
//
// A branch whose container has already been reaped by an earlier `clean` is
// therefore not removed by a later one. That is the right trade: the alternative
// is guessing from a directory name, and the fleet is not the only thing allowed
// to make worktrees.
func (r *Runner) worktreeBranches(branch string, fleetContainers []runtime.ContainerInfo) []string {
	launched := make(map[string]bool, len(fleetContainers))
	for _, c := range fleetContainers {
		if b := c.Labels[sandbox.LabelBranch]; b != "" {
			launched[b] = true
		}
	}
	wts, err := worktree.List(r.Repo)
	if err != nil {
		return nil
	}
	var out []string
	for _, wt := range wts {
		if !launched[wt.Branch] {
			continue
		}
		if branch == "" || wt.Branch == branch {
			out = append(out, wt.Branch)
		}
	}
	return out
}

// containers returns this repository's containers, optionally narrowed to one
// branch.
func (r *Runner) containers(ctx context.Context, branch string) ([]runtime.ContainerInfo, error) {
	// sandbox.fleet, not just sandbox.repo: everything built on this — stop, clean,
	// logs, and the max_parallel slot count — must reach only what `fleet run`
	// started. Filtering by repository alone would sweep in an interactive
	// `sandbox-cli claude --detach` in the same project, which is someone's live
	// session.
	labels := map[string]string{
		sandbox.LabelCLI:   "1",
		sandbox.LabelFleet: "1",
		sandbox.LabelRepo:  r.RepoID,
	}
	if branch != "" {
		labels[sandbox.LabelBranch] = branch
	}
	return r.Inspector.Containers(ctx, labels)
}

// latestFor returns the most recent container for branch, or nil.
func (r *Runner) latestFor(ctx context.Context, branch string) (*runtime.ContainerInfo, error) {
	infos, err := r.containers(ctx, branch)
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, nil
	}
	return &infos[0], nil // Containers returns newest first
}

var errNoController = fmt.Errorf("this runtime backend cannot control containers")
