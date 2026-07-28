package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/fleet"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// defaultFleetFile is looked up in the current directory when -f is omitted.
const defaultFleetFile = "fleet.yaml"

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
		file       string
		configPath string
		dryRun     bool
		build      bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Launch every task in a fleet file",
		Long: "Reads a fleet file and starts one detached agent per task, each on its own\n" +
			"branch. A task whose branch already has a running agent is refused: two\n" +
			"agents in one worktree overwrite each other's work.\n\n" +
			"Example fleet.yaml:\n\n" +
			"  agent: claude\n" +
			"  max_parallel: 3\n" +
			"  defaults:\n" +
			"    memory: 4g\n" +
			"    cpus: \"2\"\n" +
			"  tasks:\n" +
			"    - branch: feature-a\n" +
			"      prompt: implement the login form\n" +
			"    - branch: feature-b\n" +
			"      prompt: add rate limiting\n\n" +
			"With max_parallel set below the task count this command stays attached,\n" +
			"starting the remaining tasks as earlier agents exit. Otherwise it returns as\n" +
			"soon as the containers are up.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			spec, err := fleet.Load(file)
			if err != nil {
				return err
			}
			r, err := newFleetRunner(configPath)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if dryRun {
				return printFleetPlan(ctx, r, spec)
			}

			results, err := r.Launch(ctx, spec, build)
			printLaunchResults(results)
			if err != nil {
				return err
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
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "explicit sandbox config file path")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what each task would do and exit")
	cmd.Flags().BoolVar(&build, "build", false, "force rebuild of the base image before launching")
	return cmd
}

func newFleetStatusCmd() *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show every branch's agent, and what it produced",
		Long: "One line per branch: whether its agent is running, how long it ran, how many\n" +
			"files it left uncommitted, and how many commits it is ahead of the base\n" +
			"branch (what there is to land).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newFleetRunner("")
			if err != nil {
				return err
			}
			if base == "" {
				base = worktree.Branch(r.Repo)
			}
			rows, err := r.Status(cmd.Context(), base)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				fmt.Println("no fleet branches (run `sandbox-cli fleet run` to start one)")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "BRANCH\tSTATE\tELAPSED\tDIRTY\tAHEAD")
			for _, s := range rows {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\n",
					s.Branch, fleetState(s), fleetElapsed(s), s.Dirty, s.Ahead)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "branch to count commits against (default: the branch you have checked out)")
	return cmd
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
			r, err := newFleetRunner("")
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
			r, err := newFleetRunner("")
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
	var force bool
	cmd := &cobra.Command{
		Use:   "land BRANCH",
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
			"them with `sandbox-cli fleet clean BRANCH --worktrees`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newFleetRunner("")
			if err != nil {
				return err
			}
			res, err := r.Land(cmd.Context(), args[0], fleet.LandOptions{
				Message: message,
				Onto:    onto,
				Force:   force,
			})
			if err != nil {
				return err
			}
			if res.Committed {
				fmt.Printf("committed uncommitted work in %s\n", res.Branch)
			}
			fmt.Printf("merged %s into %s\n", res.Branch, res.Base)
			// Say which kind of merge this was. "passed its verify" and "nobody
			// checked" must never look the same on the way past.
			switch {
			case res.Forced:
				fmt.Printf("warning: %s failed its verify and was landed with --force\n", res.Branch)
			case res.Verified:
				fmt.Printf("%s passed its verify\n", res.Branch)
			default:
				fmt.Printf("note: %s was landed unverified (no verify command recorded for this run)\n", res.Branch)
			}
			fmt.Printf("clean up with: sandbox-cli fleet clean %s --worktrees\n", res.Branch)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message for uncommitted worktree changes (default: an auto-generated one)")
	cmd.Flags().StringVar(&onto, "onto", "", "land onto this branch deliberately, overriding the branch recorded at launch; it must be the one checked out")
	cmd.Flags().BoolVar(&force, "force", false, "land even though the run failed its verify command")
	return cmd
}

func newFleetCleanCmd() *cobra.Command {
	var worktrees bool
	cmd := &cobra.Command{
		Use:   "clean [BRANCH]",
		Short: "Remove exited fleet containers",
		Long: "Fleet containers outlive their agents so their logs and exit codes survive;\n" +
			"this removes the ones that have finished. Running agents are never touched.\n\n" +
			"With --worktrees it also removes the branches' checkouts — but only the clean\n" +
			"ones. A worktree with uncommitted work is kept and reported, because that work\n" +
			"exists nowhere else.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			branch := ""
			if len(args) == 1 {
				branch = args[0]
			}
			r, err := newFleetRunner("")
			if err != nil {
				return err
			}
			res, err := r.Clean(cmd.Context(), branch, worktrees)
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
	return cmd
}

// newFleetRunner wires a fleet Runner to the docker backend and the repository
// the command was invoked in.
func newFleetRunner(configPath string) (*fleet.Runner, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	// The same layered config an ordinary run resolves, profile included: a fleet
	// container must be confined exactly as its interactive twin is, and the way
	// to guarantee that is to build it from the same configuration rather than
	// from a second, simpler one.
	cfg, err := config.LoadProfile(wd, configPath, "")
	if err != nil {
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
func printFleetPlan(ctx context.Context, r *fleet.Runner, spec fleet.Spec) error {
	plans, err := r.Plan(ctx, spec)
	if err != nil {
		return err
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
		fmt.Printf("command:  %s\n", strings.Join(p.Command, " "))
		limits := "unlimited"
		if p.Memory != "" || p.CPUs != "" {
			limits = fmt.Sprintf("memory=%s cpus=%s", orNone(p.Memory), orNone(p.CPUs))
		}
		fmt.Printf("limits:   %s\n", limits)
		if len(p.Allow) > 0 {
			fmt.Printf("egress:   baseline + %s\n", strings.Join(p.Allow, ", "))
		}
		if p.AlreadyRunning {
			fmt.Printf("SKIPPED:  an agent is already running on this branch (%s)\n", p.RunningInName)
		}
	}
	return nil
}

func printLaunchResults(results []fleet.LaunchResult) {
	for _, res := range results {
		if res.Err != nil {
			fmt.Fprintf(os.Stderr, "sandbox-cli: %s: %v\n", res.Branch, res.Err)
		}
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
