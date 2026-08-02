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

// LaunchOptions are the caller's choices for one `fleet run`, as opposed to the
// file's. Everything here narrows or repeats what the fleet file already says;
// nothing in it can widen the boundary.
type LaunchOptions struct {
	// Only restricts the run to these branches. It is how the one task that failed
	// gets another go without editing the file — the alternative people reach for
	// is commenting the other tasks out, and a fleet file with half its tasks
	// commented out is one that will be run that way again by mistake.
	//
	// A branch named here that the file does not contain is an error rather than a
	// no-op: it is a typo, and silently launching nothing looks exactly like
	// success.
	Only []string

	// Resume skips tasks whose agent is already running or already finished
	// successfully, and starts the rest.
	//
	// The case it exists for: `max_parallel` below the task count keeps `fleet
	// run` attached to fill slots as agents exit, so a Ctrl-C there ends the
	// *scheduling* while the running containers carry on. Without this the only
	// way back is to re-run the file, which refuses every branch that still has a
	// live agent and re-does the ones that already passed.
	Resume bool

	// Build forces a rebuild of the base image before the first launch.
	Build bool

	// ExtraMounts are added to every task's container, on top of the linked
	// worktree's .git. Today this is only `--share`'s handoff directory.
	//
	// It is a launch option rather than a fleet.yaml key on purpose. `mounts:` is
	// one of the privilege-relevant keys config.trust refuses from a project file,
	// and while a fleet file has CLI-flag trust — reaching it required typing
	// `fleet run` — a mount is the one thing worth keeping out of a file that gets
	// copied between repositories. Naming it on the command line keeps the reach
	// visible in your shell history.
	ExtraMounts []string
}

// LaunchResult is the outcome of one task's launch. A failed task carries Err
// and does not stop the rest of the fleet: with several independent agents, one
// bad branch should not cost you the other launches.
type LaunchResult struct {
	Branch       string
	Agent        string
	WorktreePath string
	ContainerID  string
	Err          error
}

// slotPoll is how often Launch re-checks for a free slot when max_parallel caps
// the fleet. Agent runs last minutes, so a slow poll costs nothing and keeps the
// docker calls negligible.
const slotPoll = 2 * time.Second

// Launch starts the tasks opts selects from spec, honoring max_parallel.
//
// With max_parallel unset (or >= the task count) this returns as soon as the
// containers are started — the agents keep running in the background. With a
// lower cap it must stay attached, waiting for a container to exit before
// starting the next task; the caller's context cancels that wait.
//
// No results and no error means there was nothing to start, which only
// `--resume` can produce: everything the file asks for is already running or
// already done. It is a legitimate answer rather than a failure, and the caller
// says so.
func (r *Runner) Launch(ctx context.Context, spec Spec, opts LaunchOptions) ([]LaunchResult, error) {
	if r.Repo == "" {
		return nil, fmt.Errorf("fleet needs a git repository: run it from inside one")
	}
	tasks, err := r.selectTasks(ctx, spec, opts)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	if err := r.checkCapacity(ctx, spec, len(tasks)); err != nil {
		return nil, err
	}

	// The branch the fleet is expected to land on, resolved once and stamped on
	// every container. Read here rather than at land time because this is the
	// moment it is still true: the user launched a fleet from a checkout that was
	// on some branch, and that is what the work is for. Empty (a detached HEAD)
	// records nothing rather than guessing, and land falls back to its old
	// behavior for those.
	base := worktree.HeadBranch(r.Repo)

	results := make([]LaunchResult, 0, len(tasks))
	for i, task := range tasks {
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
		results = append(results, r.launchOne(ctx, spec, opts, task, base, opts.Build && i == 0))
	}
	return results, nil
}

// launchOne resolves a task's worktree and starts its container.
func (r *Runner) launchOne(ctx context.Context, spec Spec, lo LaunchOptions, task Task, base string, forceBuild bool) LaunchResult {
	res := LaunchResult{Branch: task.Branch}

	// Looked up per task, not once for the fleet: `agent:` on a task overrides the
	// file's, which is what lets one fleet compare two agents on two branches.
	agent, ok := agents.Lookup(spec.AgentFor(task))
	if !ok {
		// Validate has already rejected this; a Spec built in code has not been
		// through it, and starting a container for an agent that does not exist
		// would fail deep inside the container instead.
		res.Err = fmt.Errorf("unknown agent %q for branch %q", spec.AgentFor(task), task.Branch)
		return res
	}
	res.Agent = agent.Name

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

	// A finished container still holds the branch's container name, and docker
	// refuses to create a second one with a name in use. Left to the engine that
	// surfaces as `Conflict. The container name "/sandbox-<repo>-<branch>" is
	// already in use by container "<64 hex chars>"` — which names neither the
	// branch nor the fleet nor the command that fixes it, and reads like a bug in
	// docker rather than a decision this tool made. The name is deliberate: it is
	// what enforces one agent per branch. So the refusal is worth saying in the
	// tool's own words, and pointing at the logs first, since reaping is what
	// discards them.
	stale, err := r.exitedFor(ctx, task.Branch)
	if err != nil {
		res.Err = err
		return res
	}
	if stale != nil {
		// Except under --resume, where the caller has already said "retry this one"
		// and unfinished() selected this task *because* its last container exited
		// non-zero — a failed verify above all, which is the case resume documents
		// itself as existing for. Refusing here would leave --resume working only
		// for tasks whose containers had already been reaped, which is the opposite
		// of retrying the ones that failed. So the name is cleared rather than
		// reported.
		//
		// Only under --resume: an ordinary re-run has asked for nothing to be
		// discarded, and reaping is what destroys the logs. A backend that cannot
		// remove containers still refuses, because there is nothing else it can do.
		if wouldRefuseName(lo, r.Controller) {
			res.Err = fmt.Errorf("%q already has a finished container (%s, exit %d) holding its name; "+
				"read it with `sandbox-cli fleet logs %s`, then `sandbox-cli fleet clean %s` to run again",
				task.Branch, stale.Name, stale.ExitCode, task.Branch, task.Branch)
			return res
		}
		if err := r.Controller.Remove(ctx, stale.ID); err != nil {
			res.Err = fmt.Errorf("removing the finished container holding %q's name (%s): %w",
				task.Branch, stale.Name, err)
			return res
		}
		// Said out loud because it is not recoverable: the logs of the run being
		// retried are gone, and this is the only moment anyone could have read them.
		r.logf("%s: reaped its finished container (%s, exit %d) to retry; those logs are gone",
			task.Branch, stale.Name, stale.ExitCode)
	} else {
		// Nothing of the fleet's holds the name, which is not the same as the name
		// being free — see nameHolder. Without this the one case the user is least
		// able to guess at (their own interactive session, on a branch they also put
		// in the fleet file) is the one that still surfaces as docker's raw conflict,
		// which is the hole in the promise the refusal above exists to make.
		held, err := r.nameHolder(ctx, task.Branch)
		if err != nil {
			res.Err = err
			return res
		}
		if held != nil {
			res.Err = interactiveNameRefusal(task.Branch, held)
			return res
		}
	}

	info, err := worktree.Resolve(r.Repo, task.Branch)
	if err != nil {
		res.Err = err
		return res
	}
	res.WorktreePath = info.Path

	opts, err := r.options(spec, lo, agent, task, info.Path, base)
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
	// The agent is named because a fleet may now mix them, and "which agent is on
	// this branch" is then not something the file answers at a glance.
	//
	// No id here, deliberately. This line used to open with one, but Session.Start
	// returns the container *name* rather than an id, and every fleet container in
	// a repository is named sandbox-<repo>-<branch> — so truncating it to docker's
	// 12 characters printed the identical "sandbox-sand" for every task, which
	// identifies nothing and reads like it should. The branch is the fleet's
	// handle everywhere else (logs, stop, land all take one), and `fleet status`
	// prints the real ids from the inspector for the commands that want one.
	r.logf("started %s on %s (%s worktree %s)", agent.Name, task.Branch, verb, info.Path)
	return res
}

// options turns a task into the sandbox options for its container. Everything
// that defines the boundary comes from the same place an interactive run gets it;
// fleet only adds the branch, the labels and Detach.
func (r *Runner) options(spec Spec, lo LaunchOptions, agent agents.Descriptor, task Task, worktreePath, base string) (sandbox.Options, error) {
	lim := spec.LimitsFor(task)
	opts := sandbox.Options{
		Project: worktreePath,
		Detach:  true,
		RepoID:  r.RepoID,
		Branch:  task.Branch,
		Agent:   agent.Name,
		Base:    base,
		Fleet:   true,
		Verify:  task.Verify,
		Command: withVerify(agent.Autonomous(task.Prompt, task.Args), task.Verify),

		EnvAllow: agent.EnvAllow,
		// The descriptor's own container settings — an agent told that it is in a
		// container with no keyring, say. The interactive wrapper applies these
		// too; a fleet that dropped them would fail in the one place nobody is
		// watching. Copied rather than aliased, since Options.Env is appended to
		// downstream.
		Env:         append([]string(nil), agent.Env...),
		Memory:      lim.Memory,
		CPUs:        lim.CPUs,
		Allow:       lim.Allow,
		Cache:       spec.Defaults.Cache,
		GitIdentity: spec.Defaults.Git,

		// A fleet task always runs in a linked worktree, so the parent repo's .git
		// must come along or the agent can edit files it can never commit. Anything
		// the caller added — today only `--share`'s handoff directory — goes on top,
		// and every task gets it: a channel one agent can write and another cannot
		// read is not a channel.
		ExtraMounts: append(sandbox.LinkedWorktreeMounts(worktreePath), lo.ExtraMounts...),
	}

	// Persist the agent's login, exactly as the interactive wrapper does — gate
	// included. Without the mount a detached agent starts, finds itself logged
	// out, and dies unattended; without the *gate* it gets that HOME even under a
	// profile whose whole point is that it does not exist.
	//
	// The default auth path is not an API key but an OAuth refresh token sitting
	// in that directory, readable by the agent. prod turns persist_auth off so
	// there is nothing there to steal, and BuildSpec mounts AuthPersistDir
	// whenever it is non-empty — it does not re-check the config. So the check
	// belongs here, on every path that builds Options, and ValidateProfile cannot
	// stand in for it: that validates the resolved Config, and this would be a
	// leak in the Options.
	if r.Session.Cfg.PersistAuthEnabled() {
		if dir := config.AgentStateDir(agent.PersistDir); dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return sandbox.Options{}, fmt.Errorf("creating auth persist dir %s: %w", dir, err)
			}
			opts.AuthPersistDir = dir
		}
	}
	return opts, nil
}

// runningFor returns the running container working branch, or nil.
func (r *Runner) runningFor(ctx context.Context, branch string) (*runtime.ContainerInfo, error) {
	infos, err := r.branchContainers(ctx, branch)
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

// exitedFor returns the most recent finished container for branch, or nil.
//
// Containers arrive newest first, so the first match is the one whose name is in
// the way.
func (r *Runner) exitedFor(ctx context.Context, branch string) (*runtime.ContainerInfo, error) {
	infos, err := r.branchContainers(ctx, branch)
	if err != nil {
		return nil, err
	}
	for i := range infos {
		if !infos[i].Running() {
			return &infos[i], nil
		}
	}
	return nil, nil
}

// nameHolder returns a container of this repository that holds branch's
// container name but is *not* the fleet's, or nil.
//
// Docker's name namespace does not know about labels. An interactive
// `sandbox-cli <agent> --detach` on this branch is named sandbox-<repo>-<branch>
// exactly as a fleet agent is, so it blocks the launch just the same — while
// being invisible to every lookup above, all of which filter on sandbox.fleet
// and must keep doing so, since that label is what stops `fleet stop --all` and
// `fleet clean` reaching someone's live session.
//
// So this is for the refusal message and nothing else. It reports; it never
// reaps, not even under --resume: a session someone started interactively is not
// the fleet's to discard, and the whole point of the label is that fleet commands
// act only on what the fleet started.
func (r *Runner) nameHolder(ctx context.Context, branch string) (*runtime.ContainerInfo, error) {
	infos, err := r.Inspector.Containers(ctx, map[string]string{
		sandbox.LabelCLI:    "1",
		sandbox.LabelRepo:   r.RepoID,
		sandbox.LabelBranch: branch,
	})
	if err != nil {
		return nil, err
	}
	for i := range infos {
		if infos[i].Labels[sandbox.LabelFleet] == "" {
			return &infos[i], nil
		}
	}
	return nil, nil
}

// interactiveNameRefusal explains a name held by a session the fleet did not
// start, and names the command that clears it — which is not a fleet command.
func interactiveNameRefusal(branch string, held *runtime.ContainerInfo) error {
	if held.Running() {
		return fmt.Errorf("%q's container name is held by a running interactive session (%s), not a fleet agent; "+
			"see it with `sandbox-cli list`, then stop it with `sandbox-cli kill %s`",
			branch, held.Name, held.Name)
	}
	return fmt.Errorf("%q's container name is held by a finished interactive session (%s), not a fleet agent; "+
		"read it with `sandbox-cli logs %s`, then reap it with `sandbox-cli clean`",
		branch, held.Name, held.Name)
}

// branchContainers lists this fleet's containers for one branch. Filtered on
// sandbox.fleet like every other lookup here, so an interactive `--detach`
// session on the same branch is not mistaken for a fleet agent.
func (r *Runner) branchContainers(ctx context.Context, branch string) ([]runtime.ContainerInfo, error) {
	return r.Inspector.Containers(ctx, map[string]string{
		sandbox.LabelCLI:    "1",
		sandbox.LabelFleet:  "1",
		sandbox.LabelRepo:   r.RepoID,
		sandbox.LabelBranch: branch,
	})
}

// waitForSlot blocks until fewer than max of this fleet's containers are
// running.
//
// Counted through r.containers, which filters on sandbox.fleet. Counting every
// container of the repository instead — which this did — makes one open
// `sandbox-cli claude --detach` session occupy a slot it never frees, so a
// `max_parallel: 1` fleet waits behind somebody's interactive work forever. The
// same label rule the rest of the package already followed: fleet commands reach
// only what the fleet started.
func (r *Runner) waitForSlot(ctx context.Context, max int) error {
	announced := false
	for {
		infos, err := r.containers(ctx, "")
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
