package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// newRecoverCmd is the way back from a sandbox that died badly: it finds the
// work, and repairs the repository that can no longer show it.
func newRecoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Find and restore work from a sandbox run that crashed",
		Long: "While a sandbox runs, sandbox-cli snapshots the workspace into your\n" +
			"repository's own object store under refs/sandbox/snapshots/. Those snapshots\n" +
			"are what these commands hand back when a run dies: committed work the agent\n" +
			"later threw away, and uncommitted work that existed nowhere else.\n\n" +
			"With no subcommand it reports what is wrong with the repository here and\n" +
			"what there is to recover.",
		Example: "  sandbox-cli recover\n" +
			"  sandbox-cli recover list\n" +
			"  sandbox-cli recover restore 20260724-224601\n" +
			"  sandbox-cli recover repair",
		RunE: func(cmd *cobra.Command, args []string) error { return runRecoverStatus() },
	}
	cmd.AddCommand(
		newRecoverListCmd(),
		newRecoverShowCmd(),
		newRecoverRestoreCmd(),
		newRecoverRepairCmd(),
		newRecoverPruneCmd(),
	)
	return cmd
}

// runRecoverStatus is the "something went wrong, what now?" entry point, so it
// leads with the diagnosis and follows with the snapshots.
func runRecoverStatus() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	findings, derr := rescue.Diagnose(wd)
	switch {
	case derr != nil:
		// Diagnose only fails when there is no repository here to speak of, in
		// which case listing its snapshots would fail for the same reason and say
		// so twice.
		return derr
	case len(findings) == 0:
		fmt.Println("repository: healthy")
	default:
		fmt.Printf("repository: %d problem(s) found\n\n", len(findings))
		printFindings(findings, "")
		fmt.Println()
	}

	snaps, err := rescue.List(wd, false)
	if err != nil {
		// A broken repository is exactly when the snapshots matter most, so a
		// failure to list them is worth reporting rather than swallowing.
		return err
	}
	return printSnapshots(snaps, false)
}

// printFindings renders the diagnosis. branchOverride is what the user passed as
// --branch, so a finding that only needed a branch name reports the fix it is
// about to apply rather than still advising the user to supply one — printing
// "re-run with --branch NAME" during a run that already has it reads as a
// failure when the repair is in fact about to happen.
func printFindings(findings []rescue.Finding, branchOverride string) {
	for i, f := range findings {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("  \033[1m%s\033[0m\n", f.Summary)
		fmt.Printf("    %s\n", f.Detail)
		if fix, ok := f.RepairableWith(branchOverride); ok {
			fmt.Printf("    fix: %s  (sandbox-cli recover repair)\n", fix)
			continue
		}
		if f.Advice != "" {
			fmt.Printf("    do:  %s\n", f.Advice)
		}
	}
}

func newRecoverListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recoverable snapshots for this repository",
		Long: "Lists every sandbox run recorded for this repository, newest first, with\n" +
			"what it left behind. A run marked \"crashed\" is one nothing closed — the\n" +
			"container, the daemon, or sandbox-cli itself went away.\n\n" +
			"Works from your normal checkout even when the run happened in a --worktree\n" +
			"sandbox: worktrees share the repository the snapshots live in.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			snaps, err := rescue.List(wd, all)
			if err != nil {
				return err
			}
			return printSnapshots(snaps, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "list snapshots for every repository, not just this one")
	return cmd
}

func printSnapshots(snaps []rescue.Snapshot, all bool) error {
	if len(snaps) == 0 {
		fmt.Println("no snapshots recorded")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	// AGENT is here because a snapshot's real companion after a crash is the
	// conversation, and the manifest already knows which agent ran. Without it the
	// listing describes only the half that usually survives.
	header := "SESSION\tBRANCH\tAGENT\tWHEN\tSNAPSHOTS\tSTATE"
	if all {
		header = "SESSION\tREPO\tBRANCH\tAGENT\tWHEN\tSNAPSHOTS\tSTATE"
	}
	fmt.Fprintln(tw, header)
	withAgent := false
	for _, s := range snaps {
		branch := s.Branch
		if branch == "" {
			branch = "-"
		}
		agent := s.Agent
		if agent == "" {
			agent = "-" // a plain `run`: there was no conversation to lose
		} else {
			withAgent = true
		}
		state := s.Status()
		if s.Commit != "" && !s.Reachable {
			state += " (objects gone)"
		}
		if all {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", s.ID, s.Repo, branch, agent, humanAge(s.Activity()), s.Snapshots, state)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", s.ID, branch, agent, humanAge(s.Activity()), s.Snapshots, state)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if withAgent {
		// Not a per-row lookup: resolving each run's transcript means probing the
		// store and walking it once per row, which is too much work for a listing.
		// The pointer is enough, and `restore` does the resolution for the one
		// session the user picks.
		fmt.Println("\na run with an agent also left a conversation; `sandbox-cli recover restore ID` names it")
	}
	return nil
}

// humanAge renders "how long ago" at the resolution someone hunting for lost
// work actually cares about.
func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func newRecoverShowCmd() *cobra.Command {
	var patch bool
	cmd := &cobra.Command{
		Use:   "show ID",
		Short: "Show what a snapshot contains",
		Long: "Shows the snapshot for a session so you can see what is in it before\n" +
			"restoring. ID may be any unambiguous prefix of the session id from\n" +
			"`sandbox-cli recover list`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			err = rescue.Show(wd, args[0], patch)
			// git streams its own output, including its diagnostics, so a non-zero
			// exit only needs mirroring — printing it again would say it twice.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
				return nil
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&patch, "patch", "p", false, "include the full diff, not just the file stats")
	return cmd
}

func newRecoverRestoreCmd() *cobra.Command {
	var (
		branch       string
		patch        bool
		out          string
		intoWorktree bool
	)
	cmd := &cobra.Command{
		Use:   "restore ID",
		Short: "Put a snapshot back, on a branch by default",
		Long: "Restores the work from a snapshot. By default it creates a branch pointing\n" +
			"at the snapshot and changes nothing else — your working tree, your current\n" +
			"branch and every other branch are left exactly as they are. After a crash\n" +
			"the files on disk may themselves be the newest copy of your work, so\n" +
			"nothing here overwrites them unless you ask.\n\n" +
			"  sandbox-cli recover restore 20260724-224601                 # onto a branch\n" +
			"  sandbox-cli recover restore 20260724-224601 --patch -o w.patch\n" +
			"  sandbox-cli recover restore 20260724-224601 --into-worktree # files back in place",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if patch && intoWorktree {
				return fmt.Errorf("--patch and --into-worktree do different things; pick one")
			}
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			opts := rescue.RestoreOptions{Branch: branch, Out: out}
			switch {
			case patch:
				opts.Mode = rescue.RestorePatch
			case intoWorktree:
				opts.Mode = rescue.RestoreWorktree
			}
			res, err := rescue.Restore(wd, args[0], opts)
			if err != nil {
				return err
			}
			reportRestore(res, opts.Mode)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&branch, "branch", "", "name for the restored branch (default: sandbox-recover/<branch>-<session>)")
	f.BoolVar(&patch, "patch", false, "write the work out as a patch instead of creating a branch")
	f.StringVarP(&out, "output", "o", "", "with --patch, the file to write (default: stdout)")
	f.BoolVar(&intoWorktree, "into-worktree", false, "restore the files into the current working tree (refused unless it is clean)")
	return cmd
}

// reportRestore prints the next command, because "restored" is not the thing the
// user wanted — seeing their work again is.
func reportRestore(res rescue.RestoreResult, mode rescue.RestoreMode) {
	was := res.Snapshot.Branch
	if was == "" {
		was = "a detached HEAD"
	} else {
		was = "branch " + was
	}
	switch mode {
	case rescue.RestorePatch:
		if res.Patch != "-" {
			fmt.Fprintf(os.Stderr, "sandbox-cli: wrote %s (work from %s)\n", res.Patch, was)
			fmt.Fprintf(os.Stderr, "  Apply with: git apply %s\n", res.Patch)
		}
	case rescue.RestoreWorktree:
		fmt.Fprintf(os.Stderr, "sandbox-cli: restored the snapshot from %s into your working tree\n", was)
		fmt.Fprintf(os.Stderr, "  Review with: git status\n")
		fmt.Fprintf(os.Stderr, "  Keep it with: git commit\n")
	default:
		fmt.Println(res.Branch)
		fmt.Fprintf(os.Stderr, "sandbox-cli: created branch %q from %s", res.Branch, was)
		if res.Files > 0 {
			fmt.Fprintf(os.Stderr, " (%d file(s) changed)", res.Files)
		}
		fmt.Fprintln(os.Stderr)
		if res.MatchesWorkingTree {
			// Said before the git commands, because it changes what they are for.
			// The files were never lost — /workspace is a bind mount — so pointing
			// at the branch first invites a hunt through a diff that is empty.
			fmt.Fprintf(os.Stderr, "  Your working tree already matches this snapshot: nothing was missing.\n")
		}
		fmt.Fprintf(os.Stderr, "  Look at it:  git diff HEAD %s\n", res.Branch)
		fmt.Fprintf(os.Stderr, "  Work on it:  git switch %s\n", res.Branch)
	}
	reportConversation(res.Snapshot.Session)
}

func newRecoverRepairCmd() *cobra.Command {
	var (
		yes    bool
		branch string
	)
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Fix a repository a crashed sandbox left broken",
		Long: "Repairs the damage a killed sandbox leaves behind — most importantly a git\n" +
			"worktree whose administrative directory in the parent repository was pruned\n" +
			"away, which makes every git command fail with \"not a git repository\" even\n" +
			"though all your files are still there.\n\n" +
			"Nothing here moves a branch or discards a change: it rebuilds the metadata\n" +
			"git needs and then rebuilds the index from HEAD. Interrupted merges and\n" +
			"rebases are reported, never touched — only you know whether to finish or\n" +
			"abort one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			findings, err := rescue.Diagnose(wd)
			if err != nil {
				return err
			}
			if len(findings) == 0 {
				fmt.Println("nothing to repair")
				return nil
			}
			printFindings(findings, branch)

			// One reader for the whole loop: a fresh bufio.Reader per question
			// would discard anything already buffered past the first line.
			stdin := bufio.NewReader(os.Stdin)
			repaired := 0
			for _, f := range findings {
				fix, ok := f.RepairableWith(branch)
				if !ok {
					continue
				}
				if !yes && !confirm(stdin, fmt.Sprintf("\nApply: %s?", fix)) {
					fmt.Fprintln(os.Stderr, "sandbox-cli: skipped")
					continue
				}
				if err := rescue.Repair(f, branch); err != nil {
					return err
				}
				repaired++
			}
			if repaired == 0 {
				fmt.Println("\nnothing was repaired")
				return nil
			}
			fmt.Printf("\nrepaired %d problem(s); check with: git status\n", repaired)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "apply every repairable finding without asking")
	cmd.Flags().StringVar(&branch, "branch", "", "branch the broken worktree was on, when sandbox-cli has no record of it")
	return cmd
}

// confirm asks before writing into the user's repository. A repair is a small
// write, but it is a write to the thing that is already broken, so it is worth
// one question. Anything other than a terminal answers no.
func confirm(in *bufio.Reader, prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	line, err := in.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func newRecoverPruneCmd() *cobra.Command {
	var (
		olderThan  time.Duration
		superseded bool
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete snapshots that are no longer worth keeping",
		Long: "Deletes snapshot refs and their session records, freeing the objects they\n" +
			"were pinning. Two things qualify: sessions past the retention window, and\n" +
			"sessions whose work you have since committed for real.\n\n" +
			"\"Committed for real\" means a commit on a branch holds exactly the snapshot's\n" +
			"tree. Commit half of what the agent left and the snapshot stays, because it\n" +
			"is still the only copy of the other half.\n\n" +
			"Both run automatically at the start of every sandbox run in the repository;\n" +
			"this is the manual form.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := rescue.MainRepoRoot(wd)
			if err != nil {
				return err
			}
			expired, err := rescue.Prune(root, olderThan)
			if err != nil {
				return err
			}
			var done int
			if superseded {
				if done, err = rescue.PruneSuperseded(root); err != nil {
					return err
				}
			}
			if expired+done == 0 {
				fmt.Println("nothing to prune")
				return nil
			}
			fmt.Printf("pruned %d session(s): %d expired, %d already committed\n", expired+done, expired, done)
			return nil
		},
	}
	cmd.Flags().DurationVar(&olderThan, "older-than", 14*24*time.Hour, "delete sessions with no activity for longer than this")
	cmd.Flags().BoolVar(&superseded, "superseded", true, "also delete sessions whose work is already committed in this repository")
	return cmd
}
