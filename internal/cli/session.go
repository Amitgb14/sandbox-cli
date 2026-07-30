package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Sessions: what is running right now, and the four things you do with one.
//
// A *session* is a container sandbox-cli started — an ordinary `run`, an agent
// wrapper, a `--detach`, or a fleet task. Two of those outlive the process that
// started them, which is the reason this exists at all: the daemon owns the
// container, not the `docker run` client, so a `kill -9` on sandbox-cli leaves
// the agent running and still writing to /workspace, and `--detach` means to.
// "What is running right now" is therefore a question only the daemon can
// answer, and it answers it by label.
//
// The rule every command here shares: **a reference is resolved against our own
// containers and never handed to the engine to resolve.** `sandbox-cli kill
// postgres` must not reach a container this tool did not start, and matching a
// listing filtered by sandbox.cli is what guarantees that — passing the string
// through to `docker kill` would not, and would fail toward more reach.

// sessionRuntime is the slice of the container backend these commands need.
// An interface so the listing, the resolver and the verbs can be exercised
// without a daemon; the docker backend satisfies all three parts.
type sessionRuntime interface {
	runtime.Inspector
	runtime.Controller
	runtime.Attacher
}

// sessionEngine wires the backend the session commands talk to, and returns the
// engine's name so the messages can say which binary was looked for.
//
// The PATH check is here rather than left to the first exec because "docker: no
// such file or directory" is not an answer anyone can act on, and the machines
// that hit it are exactly the ones where the alternative (`--engine podman`)
// most needs saying.
func sessionEngine(cfgPath, engineFlag string) (sessionRuntime, string, error) {
	name := engineBin(cfgPath, engineFlag)
	if _, err := exec.LookPath(name); err != nil {
		return nil, name, fmt.Errorf("%s is not on your PATH, so there is nothing to ask about sandboxes\n"+
			"  install it, or select the engine you do have with --engine podman", name)
	}
	return runtime.NewEngine(name), name, nil
}

// sandboxSessions lists this tool's containers, newest first.
//
// Without all, containers that have finished are dropped — but "finished" is
// spelled as *not* exited or dead rather than as "is running", so a container
// that is created, restarting or paused still lists. Those states are all
// somebody's live run in an odd moment, and a listing that hides a container is
// worse than one that shows a state you have to read.
//
// That makes this deliberately *not* the inverse of `clean`'s sessionFinished,
// which also reaps `created`. The two questions are asymmetric on purpose:
// showing something costs a line, and removing it costs whatever it was. A
// `created` husk from a failed detached start is worth surfacing — it is how you
// learn the start failed — and is still safe to reap, because it never ran.
func sandboxSessions(ctx context.Context, rt sessionRuntime, engine string, all bool) ([]runtime.ContainerInfo, error) {
	infos, err := rt.Containers(ctx, map[string]string{sandbox.LabelCLI: "1"})
	if err != nil {
		return nil, fmt.Errorf("%w\n  is the %s daemon running? `sandbox-cli doctor` will say", err, engine)
	}
	if all {
		return infos, nil
	}
	live := make([]runtime.ContainerInfo, 0, len(infos))
	for _, c := range infos {
		if c.State == "exited" || c.State == "dead" {
			continue
		}
		live = append(live, c)
	}
	return live, nil
}

// resolveSession finds the one session a reference names.
//
// Three ways to name one, because three are what people actually have in front
// of them: the id from `sandbox-cli list`, the container name, or the branch —
// which is how a detached or fleet run is addressed everywhere else in this
// tool. Exact matches are considered before prefixes so a full id is never
// out-voted by something it happens to be a prefix of.
//
// Ambiguity refuses and lists the candidates. Stopping the wrong agent is not
// undone by running the command again, so a guess here is not worth the
// keystrokes it saves.
//
// The single exception is liveness, and it applies to *any* ambiguous tier —
// prefix collisions as much as a branch with a history — because the reasoning
// is about what a name means rather than about which field matched: a reference
// to work in progress cannot reasonably mean the container that finished
// yesterday while one is still going. It is also the narrowest possible
// widening, since the three verbs this feeds either act on a running session or
// decline to (`attach` refuses a finished one, `stopSessions` skips it).
func resolveSession(infos []runtime.ContainerInfo, ref string) (runtime.ContainerInfo, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return runtime.ContainerInfo{}, fmt.Errorf("name a session; `sandbox-cli list` shows what is running")
	}
	var exact, prefix []runtime.ContainerInfo
	for _, c := range infos {
		switch {
		case c.ID == ref || shortID(c.ID) == ref || c.Name == ref || c.Labels[sandbox.LabelBranch] == ref:
			exact = append(exact, c)
		case strings.HasPrefix(c.ID, ref) || strings.HasPrefix(c.Name, ref):
			prefix = append(prefix, c)
		}
	}
	for _, tier := range [][]runtime.ContainerInfo{exact, prefix} {
		switch len(tier) {
		case 0:
			continue
		case 1:
			return tier[0], nil
		}
		if live := runningOnly(tier); len(live) == 1 {
			return live[0], nil
		}
		return runtime.ContainerInfo{}, ambiguousSession(ref, tier)
	}
	return runtime.ContainerInfo{}, fmt.Errorf("no sandbox session matches %q\n"+
		"  `sandbox-cli list --all` shows every session, finished ones included", ref)
}

func runningOnly(infos []runtime.ContainerInfo) []runtime.ContainerInfo {
	var out []runtime.ContainerInfo
	for _, c := range infos {
		if c.Running() {
			out = append(out, c)
		}
	}
	return out
}

func ambiguousSession(ref string, matches []runtime.ContainerInfo) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d sessions; name one by id:", ref, len(matches))
	for _, c := range matches {
		fmt.Fprintf(&b, "\n  %s  %s  (%s)", shortID(c.ID), termsafe.Clean(c.Name), sessionStatus(c))
	}
	return fmt.Errorf("%s", b.String())
}

// theOnlyRunningSession is the one place a missing argument is not ambiguity:
// with exactly one sandbox running, "the session" has a single meaning.
//
// Offered to logs and attach, and deliberately not to kill. Reading is
// recoverable and stopping is not, so a destructive command does not get to
// infer its target — the cost of inferring wrongly is somebody's unfinished
// work, and the saving is one argument.
func theOnlyRunningSession(infos []runtime.ContainerInfo) (runtime.ContainerInfo, error) {
	live := runningOnly(infos)
	switch len(live) {
	case 1:
		return live[0], nil
	case 0:
		return runtime.ContainerInfo{}, fmt.Errorf("no sandbox is running\n" +
			"  `sandbox-cli list --all` includes sessions that have finished")
	default:
		return runtime.ContainerInfo{}, ambiguousSession("(no session given)", live)
	}
}

// pickSession is the argument handling shared by logs and attach.
func pickSession(infos []runtime.ContainerInfo, args []string) (runtime.ContainerInfo, error) {
	if len(args) == 0 {
		return theOnlyRunningSession(infos)
	}
	return resolveSession(infos, args[0])
}

func newListCmd() *cobra.Command {
	var all, quiet bool
	var engineFlag, cfgPath string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ps"},
		Short:   "List sandbox sessions running on this machine",
		Long: "One line per container sandbox-cli started, newest first.\n\n" +
			"This includes runs nothing is attached to any more: the daemon owns the\n" +
			"container, so a killed sandbox-cli leaves its agent working in your project\n" +
			"with no terminal on it, and `--detach` does the same on purpose. --all also\n" +
			"shows sessions that have finished — detached and fleet containers are kept\n" +
			"after they exit so their logs and exit code survive, and `sandbox-cli clean`\n" +
			"is what reaps them.\n\n" +
			"The ID column is what `attach`, `logs` and `kill` accept; so are the name and\n" +
			"the branch.",
		Example: "  sandbox-cli list\n" +
			"  sandbox-cli list --all\n" +
			"  sandbox-cli logs $(sandbox-cli list --quiet | head -1) --follow",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, engine, err := sessionEngine(cfgPath, engineFlag)
			if err != nil {
				return err
			}
			rows, err := sandboxSessions(cmd.Context(), rt, engine, all)
			if err != nil {
				return err
			}
			if quiet {
				for _, c := range rows {
					fmt.Println(shortID(c.ID))
				}
				return nil
			}
			return renderSessions(os.Stdout, rows, all, time.Now())
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include sessions that have finished")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only session ids, one per line")
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	return cmd
}

// renderSessions draws the table. Split out from the command so its shape is
// testable without a daemon — the columns are the tool's public answer to "what
// is running", and they are easy to break by accident.
func renderSessions(w io.Writer, rows []runtime.ContainerInfo, all bool, now time.Time) error {
	if len(rows) == 0 {
		if all {
			fmt.Fprintln(w, "no sandbox sessions; start one with `sandbox-cli claude` (or any agent)")
		} else {
			fmt.Fprintln(w, "no sandbox sessions running; `sandbox-cli list --all` includes finished ones")
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tAGENT\tBRANCH\tSTATUS\tELAPSED")
	anyRunning := false
	for _, c := range rows {
		anyRunning = anyRunning || c.Running()
		// Cleaned, not trusted. A label is a branch name and an agent name, which
		// means it is text from the repository — and this table goes to a terminal,
		// where an ESC is an instruction rather than a character. Clean also
		// collapses newlines and tabs, so no label can forge a row or a column in a
		// table whose separator is a tab.
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			shortID(c.ID), termsafe.Clean(c.Name),
			dash(termsafe.Clean(c.Labels[sandbox.LabelAgent])),
			dash(termsafe.Clean(c.Labels[sandbox.LabelBranch])),
			sessionStatus(c), sessionElapsed(c, now))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// A hint, not a second table, and only where a person is reading it: piping
	// the listing into another command should get the listing and nothing else.
	// The terminal test is on the writer actually being written to, so a caller
	// redirecting the table somewhere never gets advice mixed into it.
	if f, ok := w.(*os.File); anyRunning && ok && isTerminalStats(f) {
		fmt.Fprintln(w, "\nwatch one with `sandbox-cli logs <id> --follow`, or `sandbox-cli attach <id>`")
	}
	return nil
}

func newLogsCmd() *cobra.Command {
	var follow bool
	var engineFlag, cfgPath string
	cmd := &cobra.Command{
		Use:   "logs [SESSION]",
		Short: "Show what a sandbox session has written",
		Long: "Prints a session's output. Finished sessions work too, for as long as their\n" +
			"container is kept — which is the reason detached and fleet runs are not\n" +
			"removed when they exit.\n\n" +
			"SESSION is an id, a container name or a branch, as shown by `sandbox-cli\n" +
			"list`. With exactly one sandbox running you can leave it out.",
		Example: "  sandbox-cli logs\n" +
			"  sandbox-cli logs feature-a --follow\n" +
			"  sandbox-cli logs a1b2c3d4e5f6",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, engine, err := sessionEngine(cfgPath, engineFlag)
			if err != nil {
				return err
			}
			// --all: logs are most wanted for the run that just failed, and that run
			// has by definition exited.
			infos, err := sandboxSessions(cmd.Context(), rt, engine, true)
			if err != nil {
				return err
			}
			c, err := pickSession(infos, args)
			if err != nil {
				return err
			}
			return rt.Logs(cmd.Context(), c.ID, follow, os.Stdout, os.Stderr)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream new output until the session ends (Ctrl-C to stop reading)")
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	return cmd
}

func newAttachCmd() *cobra.Command {
	var engineFlag, cfgPath string
	cmd := &cobra.Command{
		Use:   "attach [SESSION]",
		Short: "Connect your terminal to a running sandbox session",
		Long: "Puts your terminal on a session that is already running — the one you started\n" +
			"in another window, or the one a killed sandbox-cli left behind.\n\n" +
			"Ctrl-C detaches and does not stop the agent. Attaching is a way to look, and\n" +
			"looking must not be able to end someone's run; `sandbox-cli kill` is how a\n" +
			"session is stopped, and it is a separate word on purpose.\n\n" +
			"A session started with --detach has no keyboard — the container was created\n" +
			"without stdin, deliberately, because nothing was attached to it — so\n" +
			"attaching to one shows its output but cannot type at it. Use `logs --follow`\n" +
			"if that is all you wanted.",
		Example: "  sandbox-cli attach\n" +
			"  sandbox-cli attach feature-a",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, engine, err := sessionEngine(cfgPath, engineFlag)
			if err != nil {
				return err
			}
			infos, err := sandboxSessions(cmd.Context(), rt, engine, true)
			if err != nil {
				return err
			}
			c, err := pickSession(infos, args)
			if err != nil {
				return err
			}
			if !c.Running() {
				return fmt.Errorf("%s is %s, so there is nothing to attach to\n"+
					"  read what it did with `sandbox-cli logs %s`",
					termsafe.Clean(c.Name), sessionStatus(c), shortID(c.ID))
			}
			for _, line := range attachNotes(c) {
				fmt.Fprintln(os.Stderr, "sandbox-cli: "+line)
			}
			return rt.Attach(cmd.Context(), c.ID, os.Stdin, os.Stdout, os.Stderr)
		},
	}
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	return cmd
}

// attachNotes is what to say before handing the terminal over.
//
// Both lines exist because the alternative is someone discovering the answer the
// expensive way: typing into a container that is not listening, or pressing
// Ctrl-C expecting to stop watching and finding they stopped the work.
func attachNotes(c runtime.ContainerInfo) []string {
	notes := []string{fmt.Sprintf("attached to %s — Ctrl-C detaches, the agent keeps running", termsafe.Clean(c.Name))}
	if !c.OpenStdin {
		notes = append(notes, "this session was started detached, so it has no keyboard: "+
			"you will see its output but cannot type at it")
	}
	return notes
}

func newKillCmd() *cobra.Command {
	var all, force bool
	var engineFlag, cfgPath string
	cmd := &cobra.Command{
		Use:     "kill SESSION...",
		Aliases: []string{"stop"},
		Short:   "Stop running sandbox sessions",
		Long: "Asks the agent to exit, and kills it if it does not. That order matters: an\n" +
			"agent gets the grace period to finish writing the file it was writing, and\n" +
			"--force is how you say you would rather it stopped now than finished that.\n\n" +
			"The container is kept afterwards, along with its logs and exit code;\n" +
			"`sandbox-cli clean` reaps it. Whatever the agent already wrote to your\n" +
			"project is on disk either way — /workspace is a bind mount, not a copy.\n\n" +
			"SESSION is an id, a container name or a branch, as shown by `sandbox-cli\n" +
			"list`. It cannot be left out: reading the wrong session's logs costs a\n" +
			"second, stopping the wrong agent costs its work.",
		Example: "  sandbox-cli kill feature-a\n" +
			"  sandbox-cli kill a1b2c3d4e5f6 sandbox-myrepo-feature-b\n" +
			"  sandbox-cli kill --all",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return fmt.Errorf("--all stops every running sandbox, so it takes no session names")
			}
			if !all && len(args) == 0 {
				return fmt.Errorf("name a session to stop, or pass --all\n" +
					"  `sandbox-cli list` shows what is running")
			}
			rt, engine, err := sessionEngine(cfgPath, engineFlag)
			if err != nil {
				return err
			}
			infos, err := sandboxSessions(cmd.Context(), rt, engine, true)
			if err != nil {
				return err
			}
			targets, err := killTargets(infos, args, all)
			if err != nil {
				return err
			}
			return stopSessions(cmd.Context(), rt, targets, force, os.Stdout)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "stop every running sandbox on this machine")
	cmd.Flags().BoolVar(&force, "force", false, "kill immediately (SIGKILL), with no chance to finish the current write")
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	return cmd
}

// killTargets resolves what to stop, refusing the whole command if any one
// reference does not resolve.
//
// All-or-nothing on purpose: a partial kill from a typo'd list leaves the user
// unsure which half ran, and re-running the corrected command would then stop
// things twice. Duplicates collapse, so naming a session by both its id and its
// branch stops it once.
func killTargets(infos []runtime.ContainerInfo, refs []string, all bool) ([]runtime.ContainerInfo, error) {
	if all {
		return runningOnly(infos), nil
	}
	seen := map[string]bool{}
	var out []runtime.ContainerInfo
	for _, ref := range refs {
		c, err := resolveSession(infos, ref)
		if err != nil {
			return nil, err
		}
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out, nil
}

// stopSessions signals every target, and reports on every target.
//
// One failure does not end the batch. Returning at the first error would leave
// the rest neither attempted nor mentioned, so a `--all` that stumbled on one
// container would tell the user about that one and leave them guessing at the
// state of the others — which is the "unsure which half ran" problem
// killTargets' all-or-nothing resolution exists to prevent, reintroduced one
// stage later. Every session gets its line, and the failures are joined at the
// end so the exit code still says something went wrong.
func stopSessions(ctx context.Context, rt sessionRuntime, targets []runtime.ContainerInfo, force bool, out io.Writer) error {
	if len(targets) == 0 {
		fmt.Fprintln(out, "no running sandbox sessions")
		return nil
	}
	stopped := 0
	var failed []error
	for _, c := range targets {
		if !c.Running() {
			// Not an error: the user asked for a state this session is already in.
			fmt.Fprintf(out, "%s had already %s\n", termsafe.Clean(c.Name), sessionStatus(c))
			continue
		}
		verb, act := "stopped", rt.Stop
		if force {
			verb, act = "killed", rt.Kill
		}
		if err := act(ctx, c.ID); err != nil {
			fmt.Fprintf(out, "%s could not be stopped\n", termsafe.Clean(c.Name))
			failed = append(failed, err)
			continue
		}
		fmt.Fprintf(out, "%s %s\n", verb, termsafe.Clean(c.Name))
		stopped++
	}
	if stopped > 0 {
		fmt.Fprintln(out, "logs and exit codes are kept; `sandbox-cli clean` removes them")
	}
	return errors.Join(failed...)
}

// shortID is the 12-character form docker itself displays, and the one every
// command here accepts back.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// sessionStatus says how a session is, and how it ended when it has.
//
// The exit code is part of the status rather than a column of its own: "exited"
// alone is the one thing nobody ever wants to know on its own.
func sessionStatus(c runtime.ContainerInfo) string {
	if c.Running() {
		return "running"
	}
	if c.State == "exited" {
		return fmt.Sprintf("exited (%d)", c.ExitCode)
	}
	if c.State == "" {
		return "?"
	}
	return c.State
}

// sessionElapsed is how long a session has been running, or how long it ran.
//
// Named for the column, and the column is named ELAPSED rather than UPTIME
// because half the rows are finished ones, for which "uptime" reads as a claim
// that they are still up. `fleet status` has carried the same value under the
// same name since it shipped, so this is the tool agreeing with itself rather
// than a new word.
func sessionElapsed(c runtime.ContainerInfo, now time.Time) string {
	if c.StartedAt.IsZero() {
		return "-"
	}
	end := c.FinishedAt
	if c.Running() || end.IsZero() || end.Before(c.StartedAt) {
		end = now
	}
	return humanDuration(end.Sub(c.StartedAt))
}

// humanDuration renders a duration for a table: two units at most, and never
// Go's "1h0m0s".
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// addSessionFlags registers the two flags every session command shares. They
// exist on each command rather than on the root because these are the questions
// "which daemon" and "whose configuration", and a supervision command that
// cannot see the engine its runs are on is worse than absent — it answers.
func addSessionFlags(cmd *cobra.Command, engineFlag, cfgPath *string) {
	cmd.Flags().StringVar(engineFlag, "engine", "", "container engine: docker (default) or podman")
	cmd.Flags().StringVarP(cfgPath, "config", "c", "", "explicit config file path")
}
