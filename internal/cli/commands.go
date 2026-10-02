package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
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
			if len(rf.fallback) > 0 {
				return fmt.Errorf("--fallback is for agents (sandbox-cli agent claude --fallback codex …): run has no agent to fall back from")
			}
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

// newAgentCmd is a wrapper: `sandbox-cli agent claude [sandbox flags] [--] [claude args]`.
// Leading recognised sandbox flags are consumed; everything else, from the first
// argument that is not one (or after "--"), is the agent's — so agent flags never
// collide with the CLI's.
func newAgentCmd(d agents.Descriptor) *cobra.Command {
	cmd := &cobra.Command{
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
			code, err := routedRun(cmd.Context(), rf, d, agentArgs)
			if err != nil {
				return err
			}
			if code != 0 {
				return exitError{code}
			}
			return nil
		},
	}
	// cobra answers --help itself, even with flag parsing off, and this command
	// declares no flags of its own — so without this the sandbox flags a
	// wrapper accepts were listed nowhere.
	cmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		rf := &runFlags{}
		fc := &cobra.Command{}
		rf.register(fc)
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n\nUsage: sandbox-cli agent %s [sandbox-flags] [--] [%s-args...]\n\n"+
			"Leading sandbox flags are consumed; everything after them, or after --, goes to %s.\n\nSandbox flags:\n%s",
			cmd.Short, d.Name, d.Name, d.Name, fc.Flags().FlagUsages())
	})
	return cmd
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
			s, err := workspace.LoadSession(args[0])
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
			ref, err := workspace.BringBack(cmd.Context(), c, s, name)
			if err != nil {
				return err
			}
			s.MarkBroughtBack(ref)
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

func newSuspendCmds() []*cobra.Command {
	var ctxFlag string
	mk := func(use, short string, do func(*api.Client, *cobra.Command, string) error) *cobra.Command {
		cmd := &cobra.Command{Use: use + " SANDBOX", Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, _, err := newClient(ctxFlag)
				if err != nil {
					return err
				}
				return do(c, cmd, args[0])
			}}
		cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
		return cmd
	}
	return []*cobra.Command{
		mk("suspend", "Stop a sandbox, keeping its memory, processes and disk", func(c *api.Client, cmd *cobra.Command, ref string) error {
			_, err := c.Suspend(cmd.Context(), ref)
			return err
		}),
		mk("resume", "Bring a suspended sandbox back as it was", func(c *api.Client, cmd *cobra.Command, ref string) error {
			_, err := c.Resume(cmd.Context(), ref)
			return err
		}),
		mk("snapshot", "Capture a sandbox; start forks of it with run --from-snapshot", func(c *api.Client, cmd *cobra.Command, ref string) error {
			s, err := c.CreateSnapshot(cmd.Context(), ref)
			if err == nil {
				fmt.Fprintln(cmd.OutOrStdout(), s.ID)
			}
			return err
		}),
	}
}

// newTunnelCmd forwards a local port to a port on a sandbox's loopback. It
// listens on 127.0.0.1 only: a tunnel is for you, not for your network.
func newTunnelCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:     "tunnel SANDBOX [LOCAL:]PORT",
		Short:   "Forward a local port to a port inside a sandbox",
		Example: "  sandbox-cli tunnel sbx_… 3000          # localhost:3000 -> the sandbox's 3000\n  sandbox-cli tunnel sbx_… 8080:3000",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			local, remote, ok := strings.Cut(args[1], ":")
			if !ok {
				remote = local
			}
			var lp, rp int
			if _, err := fmt.Sscan(local, &lp); err != nil {
				return fmt.Errorf("local port %q", local)
			}
			if _, err := fmt.Sscan(remote, &rp); err != nil {
				return fmt.Errorf("port %q", remote)
			}
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", lp))
			if err != nil {
				return err
			}
			defer l.Close()
			fmt.Fprintf(os.Stderr, "sandbox-cli: 127.0.0.1:%d -> %s:%d (Ctrl-C to stop)\n", lp, args[0], rp)
			go func() { <-cmd.Context().Done(); l.Close() }()
			for {
				local, err := l.Accept()
				if err != nil {
					return nil
				}
				go func() {
					defer local.Close()
					remote, err := c.Tunnel(cmd.Context(), args[0], rp)
					if err != nil {
						fmt.Fprintf(os.Stderr, "sandbox-cli: tunnel: %v\n", err)
						return
					}
					defer remote.Close()
					go func() {
						_, _ = io.Copy(remote, local)
						if cw, ok := remote.(interface{ CloseWrite() error }); ok {
							_ = cw.CloseWrite()
						}
					}()
					_, _ = io.Copy(local, remote)
				}()
			}
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	return cmd
}
