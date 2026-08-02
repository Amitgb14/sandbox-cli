package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/fleet"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// defaultFleetFile is looked up in the current directory when -f is omitted.
const defaultFleetFile = "fleet.yaml"

// Config selection for the whole command group, registered once on the parent as
// persistent flags rather than per subcommand.
//
// --profile is here for the same reason `run` has one: prod is the profile an
// unattended fleet most wants — nobody is watching, so a control that quietly
// could not be satisfied is exactly the failure it exists to prevent — and
// without a flag the only way to ask for it would be a config file. A supervision
// command that can only be told about a security posture in writing is one that
// will be run without it.
var (
	fleetCfgPath string
	fleetProfile string
)

// newFleetCmd is the multi-agent command group: launch several agents from one
// task file, watch them, and land what they produced.
func newFleetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "Run and supervise several agents at once",
		Long: "A fleet runs one agent per git branch, each in its own worktree and its own\n" +
			"detached container, from a single task file.\n\n" +
			"Fleet agents run autonomously by construction: nothing is attached to a\n" +
			"background container, so an agent that stopped to ask permission would hang\n" +
			"forever. `fleet run` therefore starts each agent in its non-interactive mode\n" +
			"with approvals skipped — the container, not the prompt, is the boundary.\n\n" +
			"Unlike every other sandbox-cli container, fleet containers are NOT removed\n" +
			"when they exit: their logs and exit codes are the only record a background\n" +
			"run leaves. Reap them with `sandbox-cli fleet clean`.",
		Example: "  sandbox-cli fleet run -f fleet.yaml\n" +
			"  sandbox-cli fleet status\n" +
			"  sandbox-cli fleet logs feature-a --follow\n" +
			"  sandbox-cli fleet land feature-a\n" +
			"  sandbox-cli fleet clean",
	}
	cmd.PersistentFlags().StringVarP(&fleetCfgPath, "config", "c", "", "explicit sandbox config file path")
	cmd.PersistentFlags().StringVar(&fleetProfile, "profile", "", "security profile for every task: dev or prod")
	cmd.AddCommand(
		newFleetRunCmd(),
		newFleetStatusCmd(),
		newFleetLogsCmd(),
		newFleetStopCmd(),
		newFleetLandCmd(),
		newFleetCleanCmd(),
	)
	return cmd
}

func newFleetRunCmd() *cobra.Command {
	var (
		file      string
		dryRun    bool
		build     bool
		only      []string
		resume    bool
		share     bool
		shareName string
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Launch every task in a fleet file",
		Long: "Reads a fleet file and starts one detached agent per task, each on its own\n" +
			"branch. A task whose branch already has a running agent is refused: two\n" +
			"agents in one worktree overwrite each other's work.\n\n" +
			"Example fleet.yaml:\n\n" +
			"  agent: claude          # the default for tasks that name no agent\n" +
			"  max_parallel: 3\n" +
			"  defaults:\n" +
			"    memory: 4g\n" +
			"    cpus: \"2\"\n" +
			"  tasks:\n" +
			"    - branch: feature-a\n" +
			"      prompt: implement the login form\n" +
			"      verify: go build ./... && go test ./...\n" +
			"    - branch: feature-b\n" +
			"      agent: codex       # a different agent for this branch\n" +
			"      memory: 8g         # and its own limits\n" +
			"      prompt: add rate limiting\n\n" +
			"A task's `verify` runs inside its container after the agent, and its exit\n" +
			"code decides whether the work is done — `fleet land` refuses a branch that\n" +
			"failed it. A task without one still runs, but lands reported as unverified.\n\n" +
			"With max_parallel set below the task count this command stays attached,\n" +
			"starting the remaining tasks as earlier agents exit. Otherwise it returns as\n" +
			"soon as the containers are up. Ctrl-C there stops the scheduling, not the\n" +
			"agents already running — `--resume` picks the rest up.\n\n" +
			"A fully commented example is in docs/examples/fleet.yaml; the walkthrough is\n" +
			"docs/GUIDE.md, \"Running agents in parallel\".",
		Example: "  sandbox-cli fleet run\n" +
			"  sandbox-cli fleet run --dry-run\n" +
			"  sandbox-cli fleet run --only feature-a\n" +
			"  sandbox-cli fleet run --resume",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := fleet.Load(file)
			if err != nil {
				return err
			}
			r, err := newFleetRunner(fleetCfgPath, fleetProfile)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			opts := fleet.LaunchOptions{Only: only, Resume: resume, Build: build}
			// --share is a command-line flag and deliberately not a fleet.yaml key.
			// A cross-project directory is exactly the reach the sandbox otherwise
			// refuses, so switching it on stays an act you can see in your shell
			// history rather than a line in a file that gets copied between repos.
			if shareName != "" && !share {
				return fmt.Errorf("--share-name %q needs --share as well: a namespace is a way of sharing, not an alternative to it", shareName)
			}
			if share {
				mount, err := shareMount(shareName)
				if err != nil {
					return err
				}
				opts.ExtraMounts = append(opts.ExtraMounts, mount)
			}
			if dryRun {
				return printFleetPlan(ctx, r, spec, opts)
			}

			results, err := r.Launch(ctx, spec, opts)
			printLaunchResults(results)
			if err != nil {
				return err
			}
			if len(results) == 0 {
				// Only reachable through --resume: everything the file asks for is
				// already running or already done. Saying so is the answer to the
				// question that was asked, and silence would read as a failure.
				fmt.Println("nothing to start; `sandbox-cli fleet status` shows where the fleet is")
				return nil
			}
			for _, res := range results {
				if res.Err != nil {
					// One bad task must not read as a clean fleet launch.
					exitCode = 1
					break
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", defaultFleetFile, "fleet task file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what each task would do and exit")
	cmd.Flags().BoolVar(&build, "build", false, "force rebuild of the base image before launching")
	cmd.Flags().StringSliceVar(&only, "only", nil, "start only these branches from the file (repeatable, or comma-separated)")
	cmd.Flags().BoolVar(&resume, "resume", false, "skip tasks already running or already finished successfully")
	cmd.Flags().BoolVar(&share, "share", false, "mount the shared handoff directory at /shared in every task, so one agent can leave a file for another")
	cmd.Flags().StringVar(&shareName, "share-name", "", "namespace the shared directory (requires --share)")
	return cmd
}

// statusWatchInterval is how often --watch redraws. Agent runs last minutes, so
// two seconds is already far finer than the thing being watched changes — it is
// the interval people type into `watch -n2`, which is what this replaces.
const statusWatchInterval = 2 * time.Second

func newFleetStatusCmd() *cobra.Command {
	var base string
	var watch bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show every branch's agent, and what it produced",
		Long: "One line per branch: which agent is on it, whether that agent is running, how\n" +
			"long it ran, how many files it left uncommitted, and how many commits it is\n" +
			"ahead of the base branch (what there is to land).\n\n" +
			"The ID column is the same one `sandbox-cli list` prints, and the same one\n" +
			"`logs`, `attach` and `kill` accept — a fleet agent is a session like any\n" +
			"other, and this table is the branch-shaped view of it.\n\n" +
			"--watch redraws every couple of seconds until you stop it.",
		Example: "  sandbox-cli fleet status\n" +
			"  sandbox-cli fleet status --watch",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newFleetRunner(fleetCfgPath, fleetProfile)
			if err != nil {
				return err
			}
			if base == "" {
				base = worktree.Branch(r.Repo)
			}
			if !watch {
				return printFleetStatus(cmd.Context(), r, base, os.Stdout)
			}
			return watchFleetStatus(cmd.Context(), r, base, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "branch to count commits against (default: the branch you have checked out)")
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "redraw every two seconds until interrupted")
	return cmd
}

// printFleetStatus draws the table once.
func printFleetStatus(ctx context.Context, r *fleet.Runner, base string, w io.Writer) error {
	rows, err := r.Status(ctx, base)
	if err != nil {
		return err
	}
	return renderFleetStatus(w, rows)
}

// renderFleetStatus is split out from the command so the columns — the tool's
// branch-shaped answer to "where is my fleet" — are testable without a daemon.
func renderFleetStatus(w io.Writer, rows []fleet.Status) error {
	if len(rows) == 0 {
		fmt.Fprintln(w, "no fleet branches (run `sandbox-cli fleet run` to start one)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tBRANCH\tAGENT\tSTATE\tVERIFY\tELAPSED\tDIRTY\tAHEAD")
	for _, s := range rows {
		// Branch and agent are label values, which is text from the repository
		// arriving in a tab-separated table on a terminal. Cleaned for the same
		// reason `list` cleans them: a branch name must not be able to forge a row.
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\n",
			fleetSessionID(s), termsafe.Clean(s.Branch), dash(termsafe.Clean(s.Agent)),
			fleetState(s), dash(string(s.Verify)), fleetElapsed(s), s.Dirty, s.Ahead)
	}
	return tw.Flush()
}

// watchFleetStatus redraws until the context is cancelled.
//
// It clears the screen and reprints rather than rewriting rows in place: the
// table changes shape as branches appear and disappear, and a partial redraw
// that got that wrong would show a mixture of two moments, which is worse than
// a flicker. Ctrl-C cancels the context, which ends the loop — and, as
// everywhere else in this tool, stopping the *watching* stops nothing else.
func watchFleetStatus(ctx context.Context, r *fleet.Runner, base string, w io.Writer) error {
	ticker := time.NewTicker(statusWatchInterval)
	defer ticker.Stop()
	for {
		fmt.Fprint(w, "\033[H\033[2J")
		if err := printFleetStatus(ctx, r, base, w); err != nil {
			return err
		}
		fmt.Fprintf(w, "\nwatching every %s — Ctrl-C to stop; the agents keep running\n", statusWatchInterval)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// fleetSessionID is the branch's current container in the form `list` prints, so
// one id works in both tables. A branch with no container has none to give.
func fleetSessionID(s fleet.Status) string {
	if s.Container == nil {
		return "—"
	}
	return shortID(s.Container.ID)
}

func newFleetLogsCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs BRANCH",
		Short: "Show the output of BRANCH's agent",
		Long: "Prints what the agent on BRANCH has written. The container is kept after it\n" +
			"exits, so this works for finished runs too — until `fleet clean` reaps it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newFleetRunner(fleetCfgPath, fleetProfile)
			if err != nil {
				return err
			}
			return r.Logs(cmd.Context(), args[0], follow, os.Stdout, os.Stderr)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream new output until the agent exits")
	return cmd
}

func newFleetStopCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "stop [BRANCH]",
		Short: "Stop a running agent (or all of them with --all)",
		Long: "Signals the agent to exit, killing it after docker's grace period. The\n" +
			"container and its logs are kept, and whatever the agent already wrote to the\n" +
			"worktree stays on disk — check it with `sandbox-cli fleet status`.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			branch := ""
			if len(args) == 1 {
				branch = args[0]
			}
			if branch == "" && !all {
				return fmt.Errorf("name a branch, or pass --all to stop every running agent")
			}
			r, err := newFleetRunner(fleetCfgPath, fleetProfile)
			if err != nil {
				return err
			}
			stopped, err := r.Stop(cmd.Context(), branch)
			for _, b := range stopped {
				fmt.Printf("stopped %s\n", b)
			}
			if err != nil {
				return err
			}
			if len(stopped) == 0 {
				fmt.Println("no running agents")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "stop every running agent in this repository")
	return cmd
}

func newFleetLandCmd() *cobra.Command {
	var message, onto string
	var force, all bool
	cmd := &cobra.Command{
		Use:   "land [BRANCH]",
		Short: "Merge a finished branch into your current branch",
		Long: "Commits whatever the agent left in BRANCH's worktree, then merges BRANCH\n" +
			"(--no-ff) into the branch you have checked out. It refuses while the agent is\n" +
			"still running, refuses if your checkout has uncommitted changes, and on a\n" +
			"merge conflict stops with git's message, leaving the merge for you to finish.\n\n" +
			"It also refuses if your checkout has moved since the agent was launched: each\n" +
			"container records the branch its work was meant for, and landing into a branch\n" +
			"nobody chose is the one mistake here that needs a rewrite to undo. Check out\n" +
			"the recorded branch, or say --onto BRANCH to land where you are on purpose.\n\n" +
			"And if the task declared a `verify:` command, the run must have passed it.\n" +
			"A task with no verify still lands, but is reported as unverified rather than\n" +
			"passed — nothing has said the work is right, only that the agent stopped.\n" +
			"--force lands work whose verify failed.\n\n" +
			"After a successful land the worktree and container are left in place; remove\n" +
			"them with `sandbox-cli fleet clean BRANCH --worktrees`.\n\n" +
			"--all lands every branch of the fleet, oldest first. A branch that cannot be\n" +
			"landed — still running, failed its verify, nothing to merge — is skipped and\n" +
			"reported, and the rest carry on; anything wrong with the branch being merged\n" +
			"*into* stops there, because it will be just as wrong for the next one.",
		Example: "  sandbox-cli fleet land feature-a\n" +
			"  sandbox-cli fleet land --all",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case all && len(args) == 1:
				return fmt.Errorf("--all lands every branch, so it takes no branch name")
			case !all && len(args) == 0:
				return fmt.Errorf("name a branch to land, or pass --all\n" +
					"  `sandbox-cli fleet status` shows what there is")
			}
			r, err := newFleetRunner(fleetCfgPath, fleetProfile)
			if err != nil {
				return err
			}
			opts := fleet.LandOptions{Message: message, Onto: onto, Force: force}
			if all {
				return landAll(cmd.Context(), r, opts)
			}
			res, err := r.Land(cmd.Context(), args[0], opts)
			if err != nil {
				return err
			}
			printLanded(res)
			fmt.Printf("clean up with: sandbox-cli fleet clean %s --worktrees\n", res.Branch)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message for uncommitted worktree changes (default: an auto-generated one)")
	cmd.Flags().StringVar(&onto, "onto", "", "land onto this branch deliberately, overriding the branch recorded at launch; it must be the one checked out")
	cmd.Flags().BoolVar(&force, "force", false, "land even though the run failed its verify command")
	cmd.Flags().BoolVar(&all, "all", false, "land every branch of the fleet that can be landed, oldest first")
	return cmd
}

// landAll runs and reports a `fleet land --all`.
//
// What was landed is printed even when the run stopped, and before the error:
// some of those merges are already commits in the user's branch, and an error
// message that arrived alone would say the opposite of what happened.
func landAll(ctx context.Context, r *fleet.Runner, opts fleet.LandOptions) error {
	res, err := r.LandAll(ctx, opts)
	for _, one := range res.Landed {
		printLanded(one)
	}
	for _, s := range res.Skipped {
		fmt.Printf("skipped %s: %s\n", s.Branch, s.Reason)
	}
	if err != nil {
		return err
	}
	if len(res.Landed) == 0 {
		fmt.Println("nothing was landed")
		return nil
	}
	fmt.Println("clean up with: sandbox-cli fleet clean --worktrees")
	return nil
}

// printLanded says what one merge was. "Passed its verify" and "nobody checked"
// must never look the same on the way past — which matters more under --all,
// where the lines scroll.
func printLanded(res fleet.LandResult) {
	if res.Committed {
		fmt.Printf("committed uncommitted work in %s\n", res.Branch)
	}
	fmt.Printf("merged %s into %s\n", res.Branch, res.Base)
	switch {
	case res.ForcedUnchecked:
		fmt.Printf("warning: %s never reached its verify and was landed with --force; nothing checked this work\n", res.Branch)
	case res.Forced:
		fmt.Printf("warning: %s failed its verify and was landed with --force\n", res.Branch)
	case res.Verified:
		fmt.Printf("%s passed its verify\n", res.Branch)
	default:
		fmt.Printf("note: %s was landed unverified (no verify command recorded for this run)\n", res.Branch)
	}
}

func newFleetCleanCmd() *cobra.Command {
	var worktrees, force bool
	cmd := &cobra.Command{
		Use:   "clean [BRANCH]",
		Short: "Remove exited fleet containers",
		Long: "Fleet containers outlive their agents so their logs and exit codes survive;\n" +
			"this removes the ones that have finished. Running agents are never touched.\n\n" +
			"A branch that still has work to land is also kept: `fleet land` reads the\n" +
			"branch, its base and its verify result off the container, so reaping one is\n" +
			"how a finished, passing branch becomes mergeable only by hand. Land first, or\n" +
			"pass --force to reap it anyway.\n\n" +
			"With --worktrees it also removes the branches' checkouts — but only the clean\n" +
			"ones. A worktree with uncommitted work is kept and reported, because that work\n" +
			"exists nowhere else.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			branch := ""
			if len(args) == 1 {
				branch = args[0]
			}
			r, err := newFleetRunner(fleetCfgPath, fleetProfile)
			if err != nil {
				return err
			}
			res, err := r.Clean(cmd.Context(), branch, worktrees, force)
			if len(res.Containers) > 0 {
				fmt.Printf("removed containers for: %s\n", strings.Join(res.Containers, ", "))
			}
			if len(res.Worktrees) > 0 {
				fmt.Printf("removed worktrees for: %s\n", strings.Join(res.Worktrees, ", "))
			}
			for _, k := range res.Kept {
				fmt.Printf("kept %s\n", k)
			}
			if err != nil {
				return err
			}
			if len(res.Containers) == 0 && len(res.Worktrees) == 0 && len(res.Kept) == 0 {
				fmt.Println("nothing to clean")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&worktrees, "worktrees", false, "also remove the branches' worktrees, skipping any with uncommitted work")
	cmd.Flags().BoolVar(&force, "force", false, "reap containers for branches that still have work to land")
	return cmd
}

// newFleetRunner wires a fleet Runner to the docker backend and the repository
// the command was invoked in.
func newFleetRunner(configPath, profile string) (*fleet.Runner, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	// The same layered config an ordinary run resolves, profile included: a fleet
	// container must be confined exactly as its interactive twin is, and the way
	// to guarantee that is to build it from the same configuration rather than
	// from a second, simpler one.
	cfg, err := config.LoadProfile(wd, configPath, profile)
	if err != nil {
		return nil, err
	}
	// LoadProfile runs ValidateProfile but not this: without it a malformed user
	// config is caught by `run` and waved through by `fleet run`, which is the
	// wrong way round — a fleet launches N containers from it unattended.
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	sess := sandbox.New(cfg)
	// The lazy image builder, wired exactly as newSession does it — without this a
	// fleet against a machine that has never built the base image fails at the
	// first launch with nothing to build it.
	if d, ok := sess.Runtime.(*runtime.DockerCLI); ok {
		image.Register(d)
	}

	repo := worktree.MainRepo(wd)
	if repo == "" {
		return nil, fmt.Errorf("%q is not a git repository; a fleet works on branches", wd)
	}
	// Best-effort, like the run path: a repository that cannot produce an id can
	// still be worked in, it just cannot be addressed by label afterwards — so it
	// is worth an error here rather than silently launching containers no later
	// command can find.
	id, err := worktree.RepoID(repo)
	if err != nil {
		return nil, fmt.Errorf("identifying repository %s: %w", repo, err)
	}

	r := &fleet.Runner{Session: sess, Repo: repo, RepoID: id, Out: os.Stderr}
	// The docker backend implements both; a future backend that does not simply
	// cannot serve the commands that need them, and says so.
	if insp, ok := sess.Runtime.(runtime.Inspector); ok {
		r.Inspector = insp
	} else {
		return nil, fmt.Errorf("this runtime backend cannot list containers")
	}
	if ctl, ok := sess.Runtime.(runtime.Controller); ok {
		r.Controller = ctl
	}
	return r, nil
}

// printFleetPlan renders `fleet run --dry-run`.
// shellJoin renders an argv for reading, quoting only the arguments that need it
// so a prompt full of spaces stays one visible unit.
func shellJoin(argv []string) string {
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n\"'$`\\") {
			out = append(out, strconv.Quote(a))
			continue
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

func printFleetPlan(ctx context.Context, r *fleet.Runner, spec fleet.Spec, opts fleet.LaunchOptions) error {
	plans, err := r.Plan(ctx, spec, opts)
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		fmt.Println("nothing to start")
		return nil
	}
	for i, p := range plans {
		if i > 0 {
			fmt.Println()
		}
		verb := "create"
		if p.WorktreeExists {
			verb = "reuse"
		}
		fmt.Printf("branch:   %s\n", p.Branch)
		fmt.Printf("worktree: %s (%s)\n", p.WorktreePath, verb)
		fmt.Printf("agent:    %s\n", shellJoin(p.Command))
		if p.Verify != "" {
			fmt.Printf("verify:   %s\n", p.Verify)
		} else {
			fmt.Printf("verify:   (none — this task will land unverified)\n")
		}
		limits := "unlimited"
		if p.Memory != "" || p.CPUs != "" {
			limits = fmt.Sprintf("memory=%s cpus=%s", orNone(p.Memory), orNone(p.CPUs))
		}
		fmt.Printf("limits:   %s\n", limits)
		if len(p.Allow) > 0 {
			fmt.Printf("egress:   baseline + %s\n", strings.Join(p.Allow, ", "))
		}
		for _, m := range p.Mounts {
			fmt.Printf("mount:    %s\n", m)
		}
		if p.AlreadyRunning {
			fmt.Printf("SKIPPED:  an agent is already running on this branch (%s)\n", p.RunningInName)
		}
		if p.NameHeldBy != "" {
			// The two holders need different commands, and naming the wrong one turns
			// a refusal into a dead end: `fleet clean` filters on the fleet label, so
			// it will never clear an interactive session no matter how often it is run.
			if p.NameHeldByFleet {
				fmt.Printf("REFUSED:  a finished container still holds this branch's name (%s);\n"+
					"          `sandbox-cli fleet clean %s` to run it again\n", p.NameHeldBy, p.Branch)
			} else {
				fmt.Printf("REFUSED:  an interactive session holds this branch's name (%s), not a fleet agent;\n"+
					"          see it with `sandbox-cli list`, then `sandbox-cli kill %s` or `sandbox-cli clean`\n",
					p.NameHeldBy, p.NameHeldBy)
			}
		}
	}
	// A mixed fleet's login requirement, said once at the end where it can be
	// acted on. Each agent needs its own login (or its own key) before an
	// unattended run, and finding that out from a container that exited 1 four
	// minutes in is the expensive way to learn it.
	if names := spec.Agents(); len(names) > 1 {
		fmt.Printf("\nthis fleet mixes %s — each needs its own login before an unattended run\n",
			strings.Join(names, " and "))
	}
	return nil
}

// printLaunchResults reports the tasks that did not start. The successful ones
// have already announced themselves from the runner as they went, which is what
// you want while a fan-out is in progress.
//
// The agent is named because a fleet may mix them: "feature-b: …" leaves you
// looking up which agent that branch was running before you can read the error.
func printLaunchResults(results []fleet.LaunchResult) {
	for _, res := range results {
		if res.Err == nil {
			continue
		}
		who := res.Branch
		if res.Agent != "" {
			who = fmt.Sprintf("%s (%s)", res.Branch, res.Agent)
		}
		fmt.Fprintf(os.Stderr, "sandbox-cli: %s: %v\n", who, res.Err)
	}
}

// fleetState renders the state column, spelling out how an exited agent ended.
func fleetState(s fleet.Status) string {
	if s.Container == nil {
		return "—"
	}
	if s.Container.Running() {
		return "running"
	}
	if s.Container.State == "exited" {
		return fmt.Sprintf("exited %d", s.Container.ExitCode)
	}
	return s.Container.State
}

func fleetElapsed(s fleet.Status) string {
	if s.Elapsed <= 0 {
		return "—"
	}
	return s.Elapsed.Round(time.Second).String()
}

func orNone(v string) string {
	if v == "" {
		return "none"
	}
	return v
}
