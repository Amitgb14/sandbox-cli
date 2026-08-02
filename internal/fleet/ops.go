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
	Agent          string // the agent this task resolved to, task-level or fleet-wide
	WorktreePath   string
	WorktreeExists bool // false => the run would create it

	// Command is the *agent's* argv, and Verify the task's check. The container
	// actually runs withVerify(Command, Verify) — the two composed into a shell
	// wrapper. They are reported apart because a plan is read to answer "will this
	// task do what I meant", and a multi-line wrapper script pasted into that
	// answer buries the prompt and the check that are the whole of it.
	Command      []string
	Verify       string
	Memory, CPUs string
	Allow        []string

	// Mounts are the host paths this task would get on top of its workspace — the
	// linked worktree's .git, and `--share`'s handoff directory when asked for.
	// Reported because a mount is the widest thing a launch option can add, and a
	// dry run that did not mention it would be describing a smaller container than
	// the one about to start.
	Mounts []string

	Labels         map[string]string
	AlreadyRunning bool   // an agent already holds this branch; the run would refuse
	RunningInName  string // that agent's container name

	// NameHeldBy is a *finished* container still occupying this branch's container
	// name, which the run would also refuse. Reported separately from
	// AlreadyRunning because the fix is the opposite: one is waited out or
	// stopped, the other is reaped. A plan that said "will start" and then hit
	// docker's name conflict is the inconsistency this field exists to close.
	//
	// Empty under --resume, which reaps the stale container instead of refusing:
	// the field says what the *run* would do, so reporting a refusal the run will
	// not make would reopen that same inconsistency from the other side.
	NameHeldBy string
}

// wouldRefuseName reports whether launchOne would refuse a branch whose name is
// still held by a finished container, rather than reaping it. Shared with Plan so
// the rehearsal and the run cannot disagree about it — the one thing NameHeldBy
// exists to guarantee.
func wouldRefuseName(opts LaunchOptions, ctl runtime.Controller) bool {
	return !opts.Resume || ctl == nil
}

// Plan resolves what Launch would do for each task. It touches nothing.
//
// opts narrows the plan exactly as it narrows the run, so `--dry-run --only
// feature-a` describes the command it is a rehearsal for rather than the whole
// file.
func (r *Runner) Plan(ctx context.Context, spec Spec, opts LaunchOptions) ([]Planned, error) {
	if r.Repo == "" {
		return nil, fmt.Errorf("fleet needs a git repository: run it from inside one")
	}
	tasks, err := r.selectTasks(ctx, spec, opts)
	if err != nil {
		return nil, err
	}

	base := worktree.HeadBranch(r.Repo)

	out := make([]Planned, 0, len(tasks))
	for _, t := range tasks {
		agent, ok := agents.Lookup(spec.AgentFor(t))
		if !ok {
			return nil, fmt.Errorf("unknown agent %q for branch %q", spec.AgentFor(t), t.Branch)
		}
		path, exists, err := worktree.Path(r.Repo, t.Branch)
		if err != nil {
			return nil, err
		}
		lim := spec.LimitsFor(t)
		p := Planned{
			Branch:         t.Branch,
			Agent:          agent.Name,
			WorktreePath:   path,
			WorktreeExists: exists,
			Command:        agent.Invocation(t.Prompt, t.Args),
			Verify:         t.Verify,
			Memory:         lim.Memory,
			CPUs:           lim.CPUs,
			Allow:          lim.Allow,
			Mounts:         append(sandbox.LinkedWorktreeMounts(path), opts.ExtraMounts...),
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
		// These errors propagate rather than being dropped into "nothing is in the
		// way". Plan touches nothing, but it is read to decide whether to run, and an
		// inspector that could not answer would otherwise produce a plan saying
		// "will start" for every branch the run is about to refuse — a rehearsal that
		// is wrong in exactly the direction that wastes the run.
		busy, err := r.runningFor(ctx, t.Branch)
		if err != nil {
			return nil, err
		}
		if busy != nil {
			p.AlreadyRunning = true
			p.RunningInName = busy.Name
		} else {
			stale, err := r.exitedFor(ctx, t.Branch)
			if err != nil {
				return nil, err
			}
			switch {
			case stale != nil:
				if wouldRefuseName(opts, r.Controller) {
					p.NameHeldBy = stale.Name
				}
			default:
				// The name is not the fleet's namespace, so the run also refuses on an
				// interactive session holding it. Reported here for the same reason the
				// fleet's own stale container is: a plan that omits a refusal the run
				// will make is the inconsistency this field exists to close.
				held, err := r.nameHolder(ctx, t.Branch)
				if err != nil {
					return nil, err
				}
				if held != nil {
					p.NameHeldBy = held.Name
				}
			}
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

// unlandedWork reports why branch's container must not be reaped yet, or "".
//
// Docker is the state store, and that cuts both ways: `land` reads the branch,
// its base and its verify result off the container, so removing one does not
// merely discard logs — it discards the record that makes the work landable.
// This protected running agents and nothing else, which meant the tidy-up
// command run just before landing could make landing impossible, and a branch
// that had passed its verify could only be merged by hand afterwards.
//
// The test is whether the branch still has something to land: commits not in its
// base, or uncommitted files in its worktree. `fleet land` zeroes both, so the
// ordinary land-then-clean sequence reaps exactly what it did before.
func (r *Runner) unlandedWork(branch string, c runtime.ContainerInfo) string {
	if branch == "" || r.Repo == "" {
		// No repository is not the same as a repository that cannot answer, and only
		// the second is the unanswerable question the fallback below keeps for. With
		// no repo there is no worktree and no branch to hold anything landable, so
		// there is nothing here to protect — refusing would make clean unusable
		// wherever a Runner has containers but no git.
		return ""
	}
	// The recorded base, not the checked-out one: `land` treats the label as the
	// intent, and counting against a branch the user happens to be standing on
	// would call work unlanded on the strength of where their HEAD is.
	base := c.Labels[sandbox.LabelBase]
	if base == "" {
		// No label means the launch was from a detached HEAD, which Launch
		// deliberately records nothing for. HeadBranch, not Branch: Branch stands a
		// short commit id in for a detached HEAD, and "commits not in <sha>" answers
		// a different question than the one being asked — silently, and against a
		// ref that moves.
		//
		// When there is no branch either, keep the container. This is a data-loss
		// guard, and an unanswerable question at one of those fails toward keeping:
		// the cost of being wrong is a container that needed one more `--force`,
		// against a landable branch whose only record was reaped.
		if base = worktree.HeadBranch(r.Repo); base == "" {
			// HeadBranch says "" for two different things, and only one of them is the
			// unanswerable question worth keeping a container for. Branch separates
			// them: it stands a short commit id in for a detached HEAD, and answers ""
			// only when there is no repository to ask at all. No repository means no
			// branch that could be holding landable work, so there is nothing to
			// protect and the container is reaped as it always was.
			if worktree.Branch(r.Repo) == "" {
				return ""
			}
			return "a detached HEAD and no recorded base, so what is left to land cannot be read"
		}
	}
	if n := worktree.Ahead(r.Repo, branch, base); n > 0 {
		return fmt.Sprintf("%d commit(s) not in %s to land", n, base)
	}
	if n := len(worktree.Dirty(r.Repo, branch, dirtyLimit)); n > 0 {
		return fmt.Sprintf("%d uncommitted file(s) to land", n)
	}
	return ""
}

// keptMessage says why clean kept a container and what to do about it, following
// the verify result rather than assuming one.
//
// `land` refuses a branch whose verify failed, and equally one whose run never
// reached its verify — so sending either to `fleet land` sends it to the next
// refusal, and the two commands point at each other with nothing in between that
// works. That is precisely the branch the VERIFY column was added to make
// visible, which makes it the case this guard has to get right rather than the
// edge it can round off.
func keptMessage(branch, why string, vs VerifyState) string {
	switch vs {
	case VerifyFailed:
		return fmt.Sprintf("%s: %s, but its verify failed; read it with "+
			"`sandbox-cli fleet logs %s`, then `fleet run --resume` to retry it, or "+
			"`fleet clean --force` to reap it", branch, why, branch)
	case VerifyUnchecked:
		return fmt.Sprintf("%s: %s, but nothing checked it — the run never reached its verify; "+
			"read it with `sandbox-cli fleet logs %s`, then `fleet run --resume` to retry it, or "+
			"`fleet clean --force` to reap it", branch, why, branch)
	default:
		return fmt.Sprintf("%s: %s; `sandbox-cli fleet land %s` first, or reap it anyway "+
			"with `fleet clean --force`", branch, why, branch)
	}
}

// Clean removes the exited containers of this repository's fleet, and — only
// when worktrees is set — their checkouts too.
//
// Three things are kept rather than removed, and the reason is the same each
// time: what exists nowhere else is not the command's to discard. A branch whose
// agent is still running is untouched entirely; a dirty worktree is kept; and a
// container whose branch still has work to land is kept, because it is the only
// record `land` can read. force overrides the last of those — the first two are
// not negotiable here, and `worktree rm --force` is where that decision lives.
func (r *Runner) Clean(ctx context.Context, branch string, worktrees, force bool) (CleanResult, error) {
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
	held := map[string]bool{}
	for _, c := range infos {
		if c.Running() {
			continue
		}
		b := c.Labels[sandbox.LabelBranch]
		if !force {
			// Held once per branch, and checked *before* the guard rather than after
			// it: unlandedWork asks git two questions per container (rev-list, then
			// status --porcelain), so a branch with several old containers used to pay
			// two subprocesses each to reprint a refusal it then suppressed.
			if held[b] {
				continue
			}
			if why := r.unlandedWork(b, c); why != "" {
				held[b] = true
				res.Kept = append(res.Kept, keptMessage(b, why, verifyState(&c)))
				continue
			}
		}
		if err := r.Controller.Remove(ctx, c.ID); err != nil {
			return res, err
		}
		if b != "" && !reaped[b] {
			reaped[b] = true
			res.Containers = append(res.Containers, b)
		}
	}
	if !worktrees {
		return res, nil
	}

	for _, b := range r.worktreeBranches(branch, infos) {
		if running[b] || held[b] {
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
