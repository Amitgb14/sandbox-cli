package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agentstate"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// newAgentStateCmd answers "which of my agents is waiting for me": every agent
// sandbox on the context, or the ones named, with what each agent is doing.
func newAgentStateCmd() *cobra.Command {
	var ctxFlag string
	var why bool
	cmd := &cobra.Command{
		Use:   "state [SANDBOX...]",
		Short: "Say what each agent is doing: working, blocked (waiting for you), idle, done or failed",
		Long: "Reports each agent sandbox's state from evidence, never from the agent's wording:\n" +
			"whether its process has exited, who spoke last in its conversation, how long\n" +
			"ago, and whether it has a terminal somebody can answer at. A quiet agent with a\n" +
			"terminal is blocked — waiting for you; without one it is idle. Where the\n" +
			"evidence runs out (no conversation yet, or an agent whose format is not read)\n" +
			"the answer is unknown. --why says what each state means.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			reports, err := agentReports(cmd.Context(), c, args)
			if err != nil {
				return err
			}
			printAgentStates(cmd.OutOrStdout(), reports, why)
			return nil
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	cmd.Flags().BoolVar(&why, "why", false, "say why each state was reported")
	return cmd
}

// agentReports looks at the named sandboxes, or every one an agent run started.
// Names are resolved by the server against its own listing (rule 9).
func agentReports(ctx context.Context, c *api.Client, refs []string) ([]agentstate.Report, error) {
	var sbs []api.Sandbox
	if len(refs) == 0 {
		all, err := c.Sandboxes(ctx)
		if err != nil {
			return nil, err
		}
		for _, sb := range all {
			if sb.Labels[agentstate.AgentLabel] != "" && sb.State != api.StateTerminated {
				sbs = append(sbs, sb)
			}
		}
	}
	for _, ref := range refs {
		sb, err := c.Sandbox(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ref, err)
		}
		sbs = append(sbs, sb)
	}
	now := time.Now()
	out := make([]agentstate.Report, 0, len(sbs))
	for _, sb := range sbs {
		out = append(out, agentstate.Look(ctx, c, sb, now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sandbox < out[j].Sandbox })
	return out, nil
}

func printAgentStates(w io.Writer, reports []agentstate.Report, why bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	head := "SANDBOX\tNAME\tAGENT\tSTATE"
	if why {
		head += "\tWHY"
	}
	fmt.Fprintln(tw, head)
	for _, r := range reports {
		agent := r.Agent
		if agent == "" {
			agent = "-"
		}
		line := fmt.Sprintf("%s\t%s\t%s\t%s", r.Sandbox, termsafe.Clean(r.Name), termsafe.Clean(agent), r.State)
		if why {
			line += "\t" + agentstate.Describe(r.State)
		}
		fmt.Fprintln(tw, line)
	}
	tw.Flush()
}

// newAgentWaitCmd blocks until an agent reaches one of the named states, so a
// script can start several agents and come back when one needs it.
func newAgentWaitCmd() *cobra.Command {
	var ctxFlag string
	var states []string
	var timeout, every time.Duration
	cmd := &cobra.Command{
		Use:   "wait SANDBOX --state STATE [--state STATE...]",
		Short: "Wait until an agent is in one of the given states (blocked, idle, done, failed, …)",
		Long: "Polls the agent's state until it is one of --state, then prints it and exits 0.\n" +
			"A timeout exits 2 and says which state the agent was actually in: it is not a\n" +
			"failure of the agent, and a script that treats it as one will stop work that was\n" +
			"merely slow. A sandbox that is gone ends the wait with an error, unless stopped\n" +
			"was one of the states asked for.",
		Example: "  sandbox-cli agent wait fix-auth --state blocked --state done --state failed --timeout 30m",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			want := map[agentstate.State]bool{}
			for _, s := range states {
				st := agentstate.State(s)
				if agentstate.Describe(st) == agentstate.Describe(agentstate.Unknown) && st != agentstate.Unknown {
					return fmt.Errorf("--state %q: one of working, blocked, idle, done, failed, suspended, stopped, unknown", s)
				}
				want[st] = true
			}
			if len(want) == 0 {
				return fmt.Errorf("name at least one --state to wait for")
			}
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			r, err := waitForState(cmd.Context(), c, args[0], want, timeout, every)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), r.State)
			return nil
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	cmd.Flags().StringArrayVar(&states, "state", nil, "a state to wait for (repeatable)")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long (0: never)")
	cmd.Flags().DurationVar(&every, "every", 2*time.Second, "how often to look")
	return cmd
}

// errWaitTimedOut is a wait that ran out of time, which exits 2: the agent did
// nothing wrong.
type errWaitTimedOut struct{ last agentstate.State }

func (e errWaitTimedOut) Error() string {
	return fmt.Sprintf("timed out; the agent is %s (%s)", e.last, agentstate.Describe(e.last))
}

func (errWaitTimedOut) ExitCode() int { return 2 }

func waitForState(ctx context.Context, c *api.Client, ref string, want map[agentstate.State]bool, timeout, every time.Duration) (agentstate.Report, error) {
	if every < 200*time.Millisecond {
		every = 200 * time.Millisecond
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	last := agentstate.Unknown
	for {
		sb, err := c.Sandbox(ctx, ref)
		switch {
		case err == nil:
			r := agentstate.Look(ctx, c, sb, time.Now())
			last = r.State
			if want[r.State] {
				return r, nil
			}
			if r.State == agentstate.Stopped {
				return r, fmt.Errorf("%s is gone; it will not reach %s", termsafe.Clean(ref), wantList(want))
			}
		case api.IsCode(err, api.CodeNotFound):
			if want[agentstate.Stopped] {
				return agentstate.Report{Sandbox: ref, State: agentstate.Stopped}, nil
			}
			return agentstate.Report{}, fmt.Errorf("%s is gone; it will not reach %s", termsafe.Clean(ref), wantList(want))
		case ctx.Err() == nil:
			return agentstate.Report{}, err
		}
		select {
		case <-ctx.Done():
			if timeout > 0 && ctx.Err() == context.DeadlineExceeded {
				return agentstate.Report{Sandbox: ref, State: last}, errWaitTimedOut{last}
			}
			return agentstate.Report{}, ctx.Err()
		case <-time.After(every):
		}
	}
}

func wantList(want map[agentstate.State]bool) string {
	var s []string
	for st := range want {
		s = append(s, string(st))
	}
	sort.Strings(s)
	return strings.Join(s, " or ")
}
