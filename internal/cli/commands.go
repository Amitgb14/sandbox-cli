package cli

import (
	"context"
	"errors"
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
		Long: "Creates a sandbox, runs COMMAND in the sandbox user's home directory on a\n" +
			"terminal (or streamed, when stdin is not one), and removes the sandbox. A\n" +
			"sandbox needs no repository: clone one inside it if the command wants code.",
		Example: "  sandbox-cli run -- uname -a\n  sandbox-cli run -- sh -c 'git clone https://github.com/you/app && cd app && make test'\n  sandbox-cli run --network none -- make\n  sandbox-cli run --detach -- ./long-job.sh",
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
	var labels []string
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
			list, err := c.Sandboxes(cmd.Context(), labels...)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tSTATE\tIMAGE\tNETWORK\tCREATED\tEXPIRES\tLABELS")
			for _, s := range list {
				expires := "-"
				if s.ExpiresAt != nil && s.State != api.StateTerminated {
					expires = s.ExpiresAt.Local().Format(time.DateTime)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, termsafe.Clean(s.Name), s.State,
					termsafe.Clean(s.Image), s.Network.Mode, s.CreatedAt.Local().Format(time.DateTime), expires, formatLabels(s.Labels))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "only sandboxes with this label, key=value (repeatable)")
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
		Short: "Attach your terminal to a sandbox's process; closing the terminal, or Ctrl-C without one, detaches and leaves it running",
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
			code, err := attach(cmd.Context(), c, args[0], p, info.Tty && isTerminal(os.Stdin), false)
			if errors.Is(err, errDetached) {
				fmt.Fprintf(os.Stderr, "sandbox-cli: detached; %s keeps running (sandbox-cli attach %s · sandbox-cli kill %s)\n",
					termsafe.Clean(args[0]), termsafe.Clean(args[0]), termsafe.Clean(args[0]))
				return nil
			}
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
		Short: "Terminate sandboxes, discarding everything in them",
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
			if caps.Has(api.CapMemorySnapshot) || caps.Has(api.CapDiskSnapshot) {
				fmt.Fprintf(out, "schedules: a snapshot every %v or longer, at most %d kept\n",
					time.Duration(caps.Limits.MinSnapshotEverySecs)*time.Second, caps.Limits.MaxSnapshotKeep)
			}
			if caps.Has(api.CapGPU) {
				fmt.Fprintf(out, "gpus:      %s\n", gpuLine(cmd.Context(), c, caps.Limits.MaxGPUs))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	return cmd
}

// gpuLine is doctor's GPU line: the most one sandbox may have and, from a
// sandboxd (a gateway serves no node status), how many are free now.
func gpuLine(ctx context.Context, c *api.Client, max int) string {
	line := fmt.Sprintf("up to %d per sandbox (--gpus)", max)
	if st, err := c.Node(ctx); err == nil && st.Capacity.GPUs > 0 {
		line += fmt.Sprintf("; %d of %d free on this node", st.Free.GPUs, st.Capacity.GPUs)
	}
	return line
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
		newSnapshotScheduleCmd(),
		newSnapshotLsCmd(),
		newSnapshotRmCmd(),
	}
}

// newSnapshotLsCmd lists the snapshots this context's key may see, newest
// first. Sandboxes already started from one do not need it; a snapshot is
// only what a new fork would start from.
func newSnapshotLsCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:     "snapshot-ls",
		Aliases: []string{"snapshots"},
		Short:   "List snapshots, newest first",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			list, err := c.Snapshots(cmd.Context())
			if err != nil {
				return err
			}
			sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSANDBOX\tIMAGE\tKIND\tSIZE\tTAKEN BY\tCREATED")
			for _, s := range list {
				by := "request"
				if s.Scheduled {
					by = "schedule"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", termsafe.Clean(s.ID), termsafe.Clean(s.Sandbox), termsafe.Clean(s.Image),
					termsafe.Clean(s.Kind), snapshotSize(s.Bytes), by, s.CreatedAt.Local().Format(time.DateTime))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	return cmd
}

// newSnapshotRmCmd deletes snapshots by id. Each is tried, so one that is
// already gone does not keep the rest; the command fails if any was not
// deleted. Sandboxes started from a snapshot keep running without it.
func newSnapshotRmCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:     "snapshot-rm SNAPSHOT...",
		Short:   "Delete snapshots; sandboxes already started from them keep running",
		Example: "  sandbox-cli snapshot-rm snp_0f80da1e250655e9\n  sandbox-cli snapshot-ls",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			var failed error
			for _, id := range args {
				if err := c.DeleteSnapshot(cmd.Context(), id); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "sandbox-cli: %s: %v\n", termsafe.Clean(id), err)
					failed = errors.New("not every snapshot was deleted")
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", termsafe.Clean(id))
			}
			return failed
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	return cmd
}

// snapshotSize is a snapshot's size, rounded: its bytes are not a round
// number as a sandbox's memory is, and one under a MiB still says it is there.
func snapshotSize(b int64) string {
	switch {
	case b <= 0:
		return "-"
	case b < 1<<20:
		return "<1 MiB"
	case b < 1<<30:
		return fmt.Sprintf("%d MiB", b>>20)
	}
	return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
}

// newSnapshotScheduleCmd sets a running sandbox's snapshot schedule: one every
// --every while it runs, the newest --keep kept, taken by the server whether
// or not anything is watching.
func newSnapshotScheduleCmd() *cobra.Command {
	var ctxFlag string
	var every time.Duration
	var keep int
	var off bool
	cmd := &cobra.Command{
		Use:   "snapshot-schedule SANDBOX",
		Short: "Snapshot a running sandbox on a schedule, keeping the newest few",
		Example: "  sandbox-cli snapshot-schedule demo --every 30m --keep 3\n" +
			"  sandbox-cli snapshot-schedule demo --off",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if off == (every != 0) {
				return errors.New("give --every (and --keep), or --off")
			}
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			sb, err := c.SetSnapshotSchedule(cmd.Context(), args[0], api.SnapshotSchedule{EverySecs: int(every / time.Second), Keep: keep})
			if err != nil {
				return err
			}
			if sb.SnapshotEverySecs == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: no scheduled snapshots\n", sb.ID)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: a snapshot every %v, the newest %d kept\n", sb.ID, time.Duration(sb.SnapshotEverySecs)*time.Second, sb.SnapshotKeep)
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&every, "every", 0, "how often, e.g. 30m (the server sets the shortest allowed)")
	cmd.Flags().IntVar(&keep, "keep", 0, "how many to keep, newest first (default 1)")
	cmd.Flags().BoolVar(&off, "off", false, "stop the schedule; snapshots already taken stay")
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	return cmd
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
