package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

func newRunCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "run [flags] -- <command> [args...]",
		Short: "Run any command inside the sandbox",
		Example: "  sandbox-cli run -- bash\n" +
			"  sandbox-cli run -- sh -c 'echo $HOME; ls /workspace'\n" +
			"  sandbox-cli run --dry-run -- npm test",
		RunE: func(cmd *cobra.Command, args []string) error {
			guest := guestArgs(cmd, args)
			if len(guest) == 0 {
				return fmt.Errorf("no command given; usage: sandbox-cli run -- <command> [args...]")
			}
			return execute(rf, guest)
		},
	}
	addRunFlags(cmd, rf)
	return cmd
}

// guestArgs returns the arguments after `--`. When `--` is absent, all positional
// args are treated as the guest command.
func guestArgs(cmd *cobra.Command, args []string) []string {
	if d := cmd.ArgsLenAtDash(); d >= 0 {
		return args[d:]
	}
	return args
}

// splitWrapperArgs divides an agent wrapper's args into sandbox's own flags and
// the arguments forwarded to the agent. It consumes a leading run of recognized
// sandbox long-flags (looked up on the command, so nothing is hardcoded); the
// first token that is not a known sandbox long-flag — a short flag, an unknown
// long flag, a positional, or an explicit `--` — ends the sandbox portion and
// everything from there is forwarded verbatim to the agent.
//
// This makes `sandbox-cli claude --no-persist-auth --dangerously-skip-permissions`
// work with no separator, while agent short flags like `-p` always pass through
// and never collide with sandbox's own short flags. An explicit `--` still forces
// the boundary (and is dropped from both sides).
//
// explicit reports whether the boundary came from a literal `--` rather than from
// the first unrecognized token. Everything after either boundary is forwarded, so
// the two are the same to the agent; the difference matters only to the handful of
// sandbox-cli subcommands a wrapper also answers (see wrapperSubcommand), where a
// typed `--` is how the user says "no, really, give this to the agent".
func splitWrapperArgs(cmd *cobra.Command, args []string) (sandboxFlags, guest []string, explicit bool) {
	fs := cmd.Flags()
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" { // explicit boundary; drop the separator
			return args[:i], args[i+1:], true
		}
		if !strings.HasPrefix(a, "--") {
			break // short flag or positional -> belongs to the agent
		}
		name := strings.TrimPrefix(a, "--")
		hasEq := false
		if idx := strings.IndexByte(name, '='); idx >= 0 {
			name, hasEq = name[:idx], true
		}
		fl := fs.Lookup(name)
		if fl == nil {
			break // unknown long flag -> belongs to the agent
		}
		i++
		// Consume the flag's value when it takes one and it wasn't --flag=value.
		takesValue := fl.NoOptDefVal == "" // bools have NoOptDefVal set
		if takesValue && !hasEq && i < len(args) {
			i++
		}
	}
	return args[:i], args[i:], false
}

// runWrapper implements the claude/codex subcommands. Flag parsing is disabled
// on these commands (so unknown agent flags are not rejected); sandbox flags are
// parsed manually from the leading sandbox-flag run. agentCmd is the container
// command prefix (e.g. ["codex"], or a bootstrap that execs claude), to which the
// guest args are appended. afterParse, if set, runs once sandbox flags are known
// (used to add flag-dependent mounts).
func runWrapper(cmd *cobra.Command, rf *runFlags, args []string, agentCmd, envAllow []string, afterParse func() error) error {
	// Explicit request for the wrapper's own help.
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		return cmd.Help()
	}
	sflags, guest, explicit := splitWrapperArgs(cmd, args)
	if err := cmd.Flags().Parse(sflags); err != nil {
		return err
	}
	// A sandbox-cli subcommand spelled under the agent's name (`sandbox-cli claude
	// context list`) is answered here, before anything is mounted or a container
	// is built: it is a question about the agent, not a run of it.
	if handled, err := wrapperSubcommand(cmd, rf, guest, explicit); handled {
		return err
	}
	if afterParse != nil {
		if err := afterParse(); err != nil {
			return err
		}
	}
	rf.envAllow = append(rf.envAllow, envAllow...)
	// An abbreviated session id from `context list` is expanded here, because the
	// agents require the full one — and a session belonging to another project is
	// refused here, because the container will not have its history mounted.
	guest, err := expandResumeID(cmd.Annotations[agentAnnotation], resumeProject(rf), guest)
	if err != nil {
		return err
	}
	full := append(append([]string{}, agentCmd...), guest...)
	return execute(rf, full)
}

// execute is the shared run path for run/claude/codex.
func execute(rf *runFlags, guest []string) error {
	sess, opts, err := newSession(rf)
	if err != nil {
		return err
	}
	opts.Command = guest

	if rf.dryRun {
		spec, err := sess.Prepare(opts)
		if err != nil {
			return err
		}
		fmt.Println(dockerCommandLine(spec))
		return nil
	}

	// A detached run gets no safety net: sandbox-cli exits as soon as the
	// container is up, so there is no host process left to take snapshots. The
	// container keeps running with the workspace mounted, and `recover repair`
	// still works afterwards — there is simply nothing periodic behind it.
	if rf.detach {
		return startDetached(rf, sess, opts)
	}

	// The crash safety net: started before the container and stopped after it, so
	// a run that dies anywhere in between still leaves the work recoverable.
	snap := beginRescue(rf, sess.Cfg, opts)
	stopWatching := watchSignals(snap, rf)
	defer stopWatching()

	code, err := sess.Run(context.Background(), opts, rf.build)
	if err != nil {
		snap.Stop(rescue.OutcomeFailed, nil)
		return err
	}
	snap.Stop(rescue.OutcomeClean, &code)
	warnDirtyWorktree(rf)
	if code != 0 {
		reportRescue(snap)
	}
	exitCode = code
	return nil
}

// beginRescue starts snapshotting the workspace for this run, or returns nil
// when there is nothing to protect or the user opted out. Every Snapshotter
// method tolerates a nil receiver, so the run path stays free of guards.
func beginRescue(rf *runFlags, cfg config.Config, opts sandbox.Options) *rescue.Snapshotter {
	if rf.noSnapshot || !cfg.Snapshot.IsEnabled() {
		return nil
	}
	interval := cfg.Snapshot.EveryDuration()
	if rf.snapshotInterval > 0 {
		interval = rf.snapshotInterval
	}
	ws := opts.Project
	if ws == "" {
		ws, _ = os.Getwd()
	}
	s := rescue.Begin(config.ExpandTilde(ws), rf.persistName, interval, cfg.Snapshot.RetentionDuration())
	s.Start()
	return s
}

// watchSignals gives sandbox-cli the one thing it never had: a chance to act on
// the way out. Until now SIGINT/SIGTERM killed the process with its default
// disposition, so no deferred function ran and the "you left work in the
// worktree" warning never fired in the very case that needed it.
//
// The container is deliberately *not* killed here. With a TTY docker holds the
// terminal in raw mode, so Ctrl-C is a keystroke for the agent rather than a
// signal; and when a signal does arrive it reached the whole foreground process
// group, container included. Taking the container down ourselves would only
// change how every agent behaves on Ctrl-C.
func watchSignals(snap *rescue.Snapshotter, rf *runFlags) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
			return
		case sig := <-ch:
			// Hand the signal back to the default handler first: a second Ctrl-C
			// during the final snapshot must still kill us on the spot.
			signal.Stop(ch)
			if snap != nil {
				fmt.Fprintf(os.Stderr, "\nsandbox-cli: interrupted — saving a final snapshot of your work…\n")
			}
			snap.Stop(rescue.OutcomeSignalled, nil)
			warnDirtyWorktree(rf)
			reportRescue(snap)
			os.Exit(signalExitCode(sig))
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// signalExitCode mirrors the shell convention of 128+signal, so callers see the
// same status they would have seen when the default handler killed us.
func signalExitCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok && s > 0 {
		return 128 + int(s)
	}
	return 130
}

// reportRescue points at the recovery command after a run that did not end
// cleanly — the moment the user is most likely to need it and least likely to
// remember it exists.
func reportRescue(snap *rescue.Snapshotter) {
	sess := snap.Session()
	if sess == nil || sess.Snapshots == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "sandbox-cli: your work is snapshotted — sandbox-cli recover restore %s\n", sess.ID)
}

// startDetached launches the container in the background and prints how to reach
// it again. Nothing waits on it — that is the point: one terminal can start
// several agents, and the exit code and output are recovered from docker later
// instead of from this process.
//
// The name goes to stdout on its own so the command is scriptable; everything
// else is stderr, like the rest of sandbox-cli's own commentary.
func startDetached(rf *runFlags, sess *sandbox.Session, opts sandbox.Options) error {
	name, err := sess.Start(context.Background(), opts, rf.build)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "sandbox-cli: started %s in the background\n", name)
	fmt.Fprintf(os.Stderr, "  logs:  docker logs -f %s\n", name)
	fmt.Fprintf(os.Stderr, "  stop:  docker stop %s\n", name)
	// Worth saying once, at the moment it can still be acted on: an agent left in
	// its interactive mode has no terminal in there and will sit until stopped.
	if rf.persistName != "" {
		fmt.Fprintf(os.Stderr, "  note:  nothing is attached — the agent must be in a mode that exits on its own\n")
	}
	fmt.Println(name)
	return nil
}

// warnDirtyWorktree points out work the agent left uncommitted in a --worktree
// run. It lives only in the worktree directory, and without a nudge here the
// user typically discovers it much later as a `worktree rm` refusal.
func warnDirtyWorktree(rf *runFlags) {
	if rf.worktree == "" {
		return
	}
	repoDir := rf.project
	if repoDir == "" {
		repoDir, _ = os.Getwd()
	}
	const show = 5
	files := worktree.Dirty(repoDir, rf.worktree, show+1)
	if len(files) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\nsandbox-cli: worktree %q has uncommitted changes:\n", rf.worktree)
	for i, f := range files {
		if i == show {
			fmt.Fprintf(os.Stderr, "  … and more\n")
			break
		}
		fmt.Fprintf(os.Stderr, "  %s\n", f)
	}
	fmt.Fprintf(os.Stderr, "  Commit with: sandbox-cli worktree commit %s -m \"...\"\n", rf.worktree)
}

// dockerCommandLine renders the docker invocation for --dry-run, quoting args
// that contain whitespace so the output is copy-pasteable.
func dockerCommandLine(spec runtime.RunSpec) string {
	var b strings.Builder
	b.WriteString("docker")
	for _, a := range runtime.BuildArgs(spec) {
		b.WriteByte(' ')
		b.WriteString(shellQuote(a))
	}
	return b.String()
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.ContainsAny(s, " \t\n'\"\\$") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}
