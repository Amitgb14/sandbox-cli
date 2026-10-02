package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// newWorktreeCmd manages the sandbox-owned git worktrees used by `--worktree`.
func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage the git worktrees used for parallel per-branch agents",
		Long: "sandbox-cli --worktree BRANCH runs a sandbox in a git worktree for BRANCH\n" +
			"(created if needed) so several agents can work in parallel, each on its own\n" +
			"branch. These subcommands list and remove those worktrees.",
	}
	cmd.AddCommand(
		newWorktreeListCmd(),
		newWorktreePathCmd(),
		newWorktreeGitCmd(),
		newWorktreeCommitCmd(),
		newWorktreeRemoveCmd(),
	)
	return cmd
}

// newWorktreeGitCmd runs git inside a worktree, addressed by branch name, so the
// sandbox-owned directory never has to be typed or cd'd into.
func newWorktreeGitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git BRANCH [git args...]",
		Short: "Run git inside BRANCH's worktree",
		Long: "Runs git in the worktree for BRANCH and prints its output, so you can work on\n" +
			"the agent's changes without cd-ing into the sandbox-owned directory:\n\n" +
			"  sandbox-cli worktree git feature-a status\n" +
			"  sandbox-cli worktree git feature-a diff\n" +
			"  sandbox-cli worktree git feature-a add -A\n\n" +
			"Everything after BRANCH is passed to git verbatim. Use `--` before any flag\n" +
			"git should receive that sandbox-cli would otherwise parse itself.",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
				return cmd.Help()
			}
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			gitArgs := args[1:]
			// Drop a leading `--` separator; it delimits sandbox-cli's args, not git's.
			if len(gitArgs) > 0 && gitArgs[0] == "--" {
				gitArgs = gitArgs[1:]
			}
			if len(gitArgs) == 0 {
				return fmt.Errorf("no git command given, e.g. sandbox-cli worktree git %s status", args[0])
			}
			return reportGit(worktree.Git(wd, args[0], gitArgs...))
		},
	}
	return cmd
}

// newWorktreeCommitCmd is the convenience wrapper for the common case: stage
// everything in the worktree and commit it, without leaving your own checkout.
func newWorktreeCommitCmd() *cobra.Command {
	var message string
	var all bool
	cmd := &cobra.Command{
		Use:   "commit BRANCH -m MESSAGE",
		Short: "Commit the work in BRANCH's worktree",
		Long: "Commits whatever the agent left in BRANCH's worktree, so uncommitted work\n" +
			"reaches your repository without cd-ing anywhere:\n\n" +
			"  sandbox-cli worktree commit feature-a -m \"implement A\"\n\n" +
			"Stages everything first (including untracked files) unless --no-all is given.\n" +
			"For anything more involved, use `sandbox-cli worktree git BRANCH ...`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if message == "" {
				return fmt.Errorf("a commit message is required: -m MESSAGE")
			}
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			branch := args[0]
			if all {
				if err := reportGit(worktree.Git(wd, branch, "add", "-A")); err != nil || exitCode != 0 {
					return err
				}
			}
			return reportGit(worktree.Git(wd, branch, "commit", "-m", message))
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "commit message (required)")
	cmd.Flags().BoolVar(&all, "all", true, "stage all changes, including untracked files, before committing")
	return cmd
}

func newWorktreePathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path BRANCH",
		Short: "Print the worktree directory for BRANCH (scriptable)",
		Long: "Prints the path sandbox-cli uses for BRANCH's worktree, so you never have to\n" +
			"type it by hand:\n\n" +
			"  cd \"$(sandbox-cli worktree path feature-a)\"\n\n" +
			"Usually you don't need this at all — the branch is in your repository, so\n" +
			"`git log`, `git diff` and `git checkout` work from your normal checkout. Reach\n" +
			"for the directory only to get at work the agent left uncommitted.\n\n" +
			"Exits non-zero if no worktree exists for BRANCH.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			path, exists, err := worktree.Path(wd, args[0])
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("no worktree for branch %q (would be %s)", args[0], path)
			}
			fmt.Println(path)
			return nil
		},
	}
}

func newWorktreeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List sandbox-managed worktrees for the current repository",
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			infos, err := worktree.List(wd)
			if err != nil {
				return err
			}
			if len(infos) == 0 {
				fmt.Println("no sandbox worktrees")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "BRANCH\tPATH")
			for _, wt := range infos {
				fmt.Fprintf(tw, "%s\t%s\n", wt.Branch, wt.Path)
			}
			return tw.Flush()
		},
	}
}

func newWorktreeRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "rm BRANCH",
		Short: "Remove the sandbox worktree for BRANCH",
		Long: "Removes the sandbox worktree directory for BRANCH. The branch and its\n" +
			"commits stay in your repository — only the checkout is deleted.\n\n" +
			"If the worktree has modified or untracked files, removal is refused: that\n" +
			"work exists nowhere else. Commit it (from inside the worktree, or from the\n" +
			"sandbox) or copy it out first. --force discards it permanently.",
		Aliases: []string{"remove"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			if err := worktree.Remove(wd, args[0], force); err != nil {
				return err
			}
			fmt.Printf("removed worktree for %q\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "discard uncommitted changes in the worktree and remove it anyway")
	return cmd
}

// reportGit translates a worktree.Git result into CLI behaviour: git's own
// failures only set the exit code (git already explained itself on stderr),
// while sandbox-cli's errors — an unknown branch, not a repository — are
// returned so they get printed.
func reportGit(err error) error {
	if err == nil {
		return nil
	}
	var gitErr *worktree.ErrGitFailed
	if errors.As(err, &gitErr) {
		// Mirror git's own status so scripts can distinguish its failure modes.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
		return nil
	}
	return err
}
