package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// exitError carries a guest exit code out of a command, so the CLI's own exit
// status mirrors the command it ran.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func newRunCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "run [flags] -- COMMAND [ARGS...]",
		Short: "Run a command in a new sandbox",
		Long: "Creates a sandbox, clones the current git repository into /workspace, runs\n" +
			"COMMAND on a terminal (or streamed, when stdin is not one), brings any new\n" +
			"commits back to refs/sandbox/<name>, and removes the sandbox.",
		Example: "  sandbox-cli run -- npm test\n  sandbox-cli run --network none -- make\n  sandbox-cli run --detach -- ./long-job.sh",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code, err := execute(cmd.Context(), rf, runSpec{argv: args})
			if err != nil {
				return err
			}
			if code != 0 {
				return exitError{code}
			}
			return nil
		},
	}
	rf.register(cmd)
	return cmd
}

// newAgentCmd is a wrapper: `sandbox-cli claude [sandbox flags] [--] [claude args]`.
// Leading recognised sandbox flags are consumed; everything else, from the first
// argument that is not one (or after "--"), is the agent's — so agent flags never
// collide with the CLI's.
func newAgentCmd(d agents.Descriptor) *cobra.Command {
	return &cobra.Command{
		Use:                d.Name + " [sandbox-flags] [--] [" + d.Name + "-args...]",
		Short:              "Run " + d.Name + " in a new sandbox",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sandboxArgs, agentArgs := splitWrapperArgs(args)
			rf := &runFlags{}
			fc := &cobra.Command{}
			rf.register(fc)
			if err := fc.ParseFlags(sandboxArgs); err != nil {
				return err
			}
			if help, _ := fc.Flags().GetBool("help"); help {
				fmt.Fprintf(cmd.OutOrStdout(), "Usage: sandbox-cli %s [sandbox-flags] [--] [%s-args...]\n\nSandbox flags:\n%s",
					d.Name, d.Name, fc.Flags().FlagUsages())
				return nil
			}
			argv := append(append([]string{}, d.Command...), agentArgs...)
			code, err := execute(cmd.Context(), rf, runSpec{argv: argv, agent: &d})
			if err != nil {
				return err
			}
			if code != 0 {
				return exitError{code}
			}
			return nil
		},
	}
}

func splitWrapperArgs(args []string) (sandbox, agent []string) {
	known := sandboxFlagNames()
	known["help"] = false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return sandbox, args[i+1:]
		}
		name, _, hasValue := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		takesValue, ok := known[name]
		if !strings.HasPrefix(a, "--") || !ok {
			return sandbox, args[i:]
		}
		sandbox = append(sandbox, a)
		if takesValue && !hasValue && i+1 < len(args) {
			i++
			sandbox = append(sandbox, args[i])
		}
	}
	return sandbox, nil
}

func newListCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List sandboxes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			list, err := c.Sandboxes(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tSTATE\tIMAGE\tNETWORK\tCREATED")
			for _, s := range list {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, termsafe.Clean(s.Name), s.State,
					termsafe.Clean(s.Image), s.Network.Mode, s.CreatedAt.Local().Format(time.DateTime))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	return cmd
}

// pickProcess is the process a session command means when no --pid is given:
// the first running one with a terminal, else the first running one, else the
// first.
func pickProcess(ctx context.Context, c *api.Client, ref string, pid int) (int, error) {
	if pid > 0 {
		return pid, nil
	}
	ps, err := c.Processes(ctx, ref)
	if err != nil {
		return 0, err
	}
	if len(ps) == 0 {
		return 0, fmt.Errorf("%s has no processes", ref)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].PID < ps[j].PID })
	for _, want := range []func(api.Process) bool{
		func(p api.Process) bool { return p.State == api.ProcessRunning && p.Tty },
		func(p api.Process) bool { return p.State == api.ProcessRunning },
		func(api.Process) bool { return true },
	} {
		for _, p := range ps {
			if want(p) {
				return p.PID, nil
			}
		}
	}
	return ps[0].PID, nil
}

func newAttachCmd() *cobra.Command {
	var ctxFlag string
	var pid int
	cmd := &cobra.Command{
		Use:   "attach SANDBOX",
		Short: "Attach your terminal to a sandbox's process; detaching (closing the terminal) leaves it running",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			p, err := pickProcess(cmd.Context(), c, args[0], pid)
			if err != nil {
				return err
			}
			info, err := c.Process(cmd.Context(), args[0], p)
			if err != nil {
				return err
			}
			code, err := attach(cmd.Context(), c, args[0], p, info.Tty && isTerminal(os.Stdin))
			if err != nil {
				return err
			}
			if code != 0 {
				return exitError{code}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	cmd.Flags().IntVar(&pid, "pid", 0, "process to attach to (default: the running one)")
	return cmd
}

func newLogsCmd() *cobra.Command {
	var ctxFlag string
	var pid int
	cmd := &cobra.Command{
		Use:   "logs SANDBOX",
		Short: "Print a sandbox process's output from the start, following it until it exits",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			p, err := pickProcess(cmd.Context(), c, args[0], pid)
			if err != nil {
				return err
			}
			return c.FollowOutput(cmd.Context(), args[0], p, func(ev api.OutputEvent) error {
				if ev.Stream == "stderr" {
					_, _ = os.Stderr.Write(ev.Data)
				} else {
					_, _ = os.Stdout.Write(ev.Data)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	cmd.Flags().IntVar(&pid, "pid", 0, "process (default: the running one)")
	return cmd
}

// kill names its target explicitly: stopping the wrong sandbox costs its work,
// so unlike logs and attach it never guesses.
func newKillCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:   "kill SANDBOX...",
		Short: "Terminate sandboxes, discarding anything not brought back",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			for _, ref := range args {
				if err := c.TerminateSandbox(cmd.Context(), ref); err != nil {
					return fmt.Errorf("%s: %w", ref, err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	return cmd
}

func newBringBackCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "bring-back SANDBOX",
		Short: "Fetch a sandbox's commits into refs/sandbox/<name> in the repository it was cloned from",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadSession(args[0])
			if err != nil {
				return fmt.Errorf("no record of a repository cloned into %s on this machine", args[0])
			}
			c, _, err := newClient(s.Context)
			if err != nil {
				return err
			}
			if name == "" {
				name = s.Sandbox
			}
			ref, err := bringBack(cmd.Context(), c, s, name)
			if err != nil {
				return err
			}
			if ref == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "no new commits")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", ref)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "ref name under refs/sandbox/ (default: the sandbox id)")
	return cmd
}

func newDoctorCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the current context's sandboxd and say what it offers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, name, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			caps, err := c.Capabilities(cmd.Context())
			if err != nil {
				return fmt.Errorf("context %s: sandboxd is not answering: %w", name, err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "context:   %s\nbackend:   %s\napi:       %s\n", name, caps.Backend, caps.APIVersion)
			fmt.Fprintf(out, "network:   default %s, ceiling %s\n", caps.Network.Default.Mode, caps.Network.Ceiling)
			var on []string
			for k, v := range caps.Capabilities {
				if v {
					on = append(on, k)
				}
			}
			sort.Strings(on)
			fmt.Fprintf(out, "can:       %s\n", strings.Join(on, ", "))
			fmt.Fprintf(out, "limits:    %v cpus, %d MiB memory, %d MiB disk\n", caps.Limits.MaxCPUs, caps.Limits.MaxMemoryMB, caps.Limits.MaxDiskMB)
			return nil
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	return cmd
}
