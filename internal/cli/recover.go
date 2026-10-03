package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// recover answers "where is my work?" after something ended a run badly. In
// beta.15 that meant a host repository broken by a container writing into it;
// here the host repository is never written by a guest, so the question is
// only which of three places the work still is:
//
//   - in a sandbox that is still alive — the CLI that ran it was killed, the
//     terminal closed, the laptop slept — where bring-back still works, until
//     the sandbox's idle timeout ends it;
//   - in a checkpoint fetched during the run, when the sandbox itself is gone
//     (a sandboxd restart or a host reboot ends every VM with its disk);
//   - nowhere, which is said plainly rather than left to be discovered.
//
// It reads the session records the CLI writes and asks each one's own sandboxd
// about its sandbox; it never writes to the repository.
func newRecoverCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Find work that was never brought back: in a live sandbox, in a checkpoint, or lost",
		Long: "Lists runs in this repository whose work was never brought back, and says where\n" +
			"it still is. A sandbox that outlived the CLI that started it can still be\n" +
			"brought back. One that is gone may have left a checkpoint: while a run is\n" +
			"attached, its working tree is fetched to refs/sandbox/checkpoints/<id> every few\n" +
			"minutes (--checkpoint-every).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo := ""
			if !all {
				r, err := repoHere()
				if err != nil {
					return fmt.Errorf("%w (or --all for every repository)", err)
				}
				repo = r
			}
			return listRecoverable(cmd.Context(), cmd.OutOrStdout(), repo, time.Now())
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every repository, not just this one")
	cmd.AddCommand(&cobra.Command{
		Use:   "forget SANDBOX...",
		Short: "Drop the record of a run; refs it fetched stay in the repository",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range args {
				if _, err := workspace.LoadSession(id); err != nil {
					return fmt.Errorf("no record of %s", id)
				}
				if err := workspace.Forget(id); err != nil {
					return err
				}
			}
			return nil
		},
	})
	return cmd
}

// Where a session's sandbox stands, as far as its sandboxd says.
const (
	whereGone        = "gone"
	whereUnreachable = "unreachable"
)

func sandboxState(ctx context.Context, clients map[string]*api.Client, s workspace.Session) string {
	c, ok := clients[s.Context]
	if !ok {
		var err error
		c, _, err = newClient(s.Context)
		if err != nil {
			c = nil
		}
		clients[s.Context] = c
	}
	if c == nil {
		return whereUnreachable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	sb, err := c.Sandbox(ctx, s.Sandbox)
	switch {
	case api.IsCode(err, api.CodeNotFound):
		return whereGone
	case err != nil:
		return whereUnreachable
	case sb.State == api.StateTerminated:
		return whereGone
	}
	return sb.State
}

func listRecoverable(ctx context.Context, out io.Writer, repo string, now time.Time) error {
	sessions, err := workspace.Sessions()
	if err != nil {
		return err
	}
	clients := map[string]*api.Client{}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	rows := 0
	var next []string
	for _, s := range sessions {
		if repo != "" && s.Repo != repo {
			continue
		}
		state := sandboxState(ctx, clients, s)
		if s.Done {
			// Brought back, and nothing left behind to bring back again: the
			// record has done its job.
			if state == whereGone {
				_ = workspace.Forget(s.Sandbox)
			}
			continue
		}
		if rows == 0 {
			fmt.Fprintln(tw, "SANDBOX\tSTARTED\tSANDBOX STATE\tCHECKPOINT\tREPOSITORY")
		}
		rows++
		ckpt := "-"
		if s.Checkpoint != "" {
			ckpt = humanAge(now, s.CheckpointAt)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Sandbox, humanAge(now, s.Started), state, ckpt, termsafe.Clean(s.Repo))
		next = append(next, recoverHint(s, state))
	}
	if rows == 0 {
		fmt.Fprintln(out, "nothing to recover: every recorded run brought its work back")
		return nil
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(out)
	for _, n := range next {
		fmt.Fprintln(out, n)
	}
	return nil
}

func recoverHint(s workspace.Session, state string) string {
	id := s.Sandbox
	switch state {
	case api.StateRunning, api.StatePending:
		return fmt.Sprintf("%s: alive — sandbox-cli bring-back %s", id, id)
	case api.StateSuspended:
		return fmt.Sprintf("%s: suspended — sandbox-cli resume %s && sandbox-cli bring-back %s", id, id, id)
	case whereUnreachable:
		return fmt.Sprintf("%s: its sandboxd (context %s) did not answer; try again when it is up", id, termsafe.Clean(s.Context))
	}
	if s.Checkpoint != "" {
		return fmt.Sprintf("%s: the sandbox is gone; its last checkpoint is %s\n  review: git log -p HEAD..%s · merge: git merge %s · done: sandbox-cli recover forget %s",
			id, s.Checkpoint, s.Checkpoint, s.Checkpoint, id)
	}
	return fmt.Sprintf("%s: the sandbox is gone and no checkpoint was taken; the work is lost (sandbox-cli recover forget %s)", id, id)
}

// humanAge renders "how long ago" at the resolution someone hunting for lost
// work actually cares about.
func humanAge(now, t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := now.Sub(t)
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
