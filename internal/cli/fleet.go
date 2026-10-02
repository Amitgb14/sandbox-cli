package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/fleet"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

func newFleetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "Run one agent per branch, each in its own sandbox, from a fleet.yaml",
	}
	cmd.AddCommand(newFleetRunCmd(), newFleetStatusCmd(), newFleetLandCmd())
	return cmd
}

func repoHere() (string, error) {
	wd, _ := os.Getwd()
	repo := workspace.RepoRoot(wd)
	if repo == "" {
		return "", fmt.Errorf("not in a git repository")
	}
	return repo, nil
}

func newFleetRunCmd() *cobra.Command {
	var file, ctxFlag, profile string
	var keep bool
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "run -f fleet.yaml",
		Short: "Run every task, max_parallel at a time, and bring each one's work back to refs/sandbox/fleet/<branch>",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := fleet.Load(file)
			if err != nil {
				return err
			}
			repo, err := repoHere()
			if err != nil {
				return err
			}
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			cfg, err := loadConfig(repo, "", profile)
			if err != nil {
				return err
			}
			r := &fleet.Runner{Client: c, Repo: repo, Keep: keep, Out: cmd.OutOrStdout(),
				PersistLogins: cfg.PersistAuthEnabled(), CheckpointEvery: every}
			st, err := r.Run(cmd.Context(), spec)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nlogs: %s\nland: sandbox-cli agent fleet land --all\n", fleetLogDir(st))
			if n := len(st.Unfinished()); n > 0 {
				return fmt.Errorf("%d of %d tasks did not verify", n, len(st.Tasks))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "fleet.yaml", "the fleet file")
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	cmd.Flags().StringVar(&profile, "profile", "", "dev or prod (prod: no persisted logins)")
	cmd.Flags().BoolVar(&keep, "keep", false, "keep each task's sandbox when it finishes")
	cmd.Flags().DurationVar(&every, "checkpoint-every", 5*time.Minute, "fetch each task's working tree to refs/sandbox/checkpoints/<sandbox> this often while it runs (0: never)")
	return cmd
}

func fleetLogDir(st *fleet.State) string {
	return filepath.Join(filepath.Dir(fleet.StatePath(st.Repo)), "logs")
}

func newFleetStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the last fleet run in this repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, err := repoHere()
			if err != nil {
				return err
			}
			st, err := fleet.LoadState(repo)
			if err != nil {
				return fmt.Errorf("no fleet run recorded for this repository")
			}
			names := make([]string, 0, len(st.Tasks))
			for b := range st.Tasks {
				names = append(names, b)
			}
			sort.Strings(names)
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "BRANCH\tAGENT\tSTATE\tEXIT\tREF\n")
			for _, b := range names {
				t := st.Tasks[b]
				// Where the work is: what came back, or else the last
				// checkpoint of a task that never got that far.
				ref := t.Ref
				if ref == "" && t.Checkpoint != "" {
					ref = t.Checkpoint + " (checkpoint)"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", termsafe.Clean(b), t.Agent, t.State, t.ExitCode, ref)
			}
			return tw.Flush()
		},
	}
}

func newFleetLandCmd() *cobra.Command {
	var all, unverified bool
	var onto string
	cmd := &cobra.Command{
		Use:   "land [BRANCH...] | --all",
		Short: "Merge a task's work (refs/sandbox/fleet/<branch>) into the checked-out branch",
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) > 0) {
				return fmt.Errorf("name branches to land, or --all")
			}
			repo, err := repoHere()
			if err != nil {
				return err
			}
			st, err := fleet.LoadState(repo)
			if err != nil {
				return fmt.Errorf("no fleet run recorded for this repository")
			}
			opts := fleet.LandOptions{Unverified: unverified, Onto: onto}
			out := cmd.OutOrStdout()
			if all {
				landed, skipped, err := fleet.LandAll(st, opts)
				for _, b := range landed {
					fmt.Fprintf(out, "landed  %s\n", termsafe.Clean(b))
				}
				for _, s := range skipped {
					fmt.Fprintf(out, "skipped %s: %s\n", termsafe.Clean(s.Branch), s.Reason)
				}
				return err
			}
			for _, b := range args {
				if err := fleet.Land(st, b, opts); err != nil {
					return err
				}
				fmt.Fprintf(out, "landed  %s\n", termsafe.Clean(b))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "land every task that can be")
	cmd.Flags().BoolVar(&unverified, "unverified", false, "land work that failed or whose verify rejected it")
	cmd.Flags().StringVar(&onto, "onto", "", "land into this branch rather than the one the fleet started from")
	return cmd
}
