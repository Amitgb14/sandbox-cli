package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/detect"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/session"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// The session server's commands: `serve`, and the two read-only views of what it
// knows.
//
// Phase 1 of the control-plane track, and deliberately read-only: nothing here
// starts a container. A pane is still created by `claude --detach` and friends, and
// `serve` catalogs what it finds — which is what makes this safe to land before
// the spawn path moves, and what makes the catalog comparable with
// `sandbox-cli list` rather than a second source of truth for the same question.

func newServeCmd() *cobra.Command {
	var engineFlag, cfgPath string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the session server for this repository",
		Long: "Catalogs the sandboxes belonging to this repository — which worktrees exist,\n" +
			"which containers are in them, and how each is doing — and answers questions\n" +
			"about them over a unix socket in the session directory.\n\n" +
			"Foreground, and one per repository: the socket path is derived from the\n" +
			"repository's identity, so two shells in one checkout find the same daemon and\n" +
			"two different checkouts cannot collide.\n\n" +
			"It starts nothing. Containers are still created by the agent wrappers, and\n" +
			"stopping the server leaves every one of them running — the daemon owns them,\n" +
			"which is the same reason `sandbox-cli list` survives a killed CLI.\n\n" +
			"The socket is 0600 inside a 0700 directory and carries no token: anyone who\n" +
			"can open it can already run the container engine as you, so a token would be\n" +
			"a second secret protecting nothing.",
		Example: "  sandbox-cli serve\n" +
			"  sandbox-cli serve status\n" +
			"  sandbox-cli pane list",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, engine, err := sessionEngine(cfgPath, engineFlag)
			if err != nil {
				return err
			}
			profile, err := servingProfile(cfgPath)
			if err != nil {
				return err
			}
			srv, err := openSession(cfgPath, engine, profile)
			if err != nil {
				return err
			}
			// Only the daemon looks for conversations. It is the one caller that
			// refreshes repeatedly, so the per-pane file reads are amortised — and a
			// one-shot `pane list` reading every agent's transcript store to print a
			// column would pay that cost on every invocation to answer a question
			// nobody asked it.
			srv.Transcripts = session.ReadTranscripts

			// Asked here as well as inside Serve, and the duplication is for the
			// *order* of the output rather than for the check. Serve's is the real
			// guard — it is what a library caller gets, and it is the one close enough
			// to the bind to matter — but by the time it refuses, this command has
			// already printed a banner saying which socket it is on. Refusing before
			// the banner is the difference between one sentence and a start, a stop
			// and an error.
			if _, alive := servePID(srv.Dir()); alive {
				return fmt.Errorf("a session server is already running for %s\n"+
					"  socket %s is answering; stop it with `sandbox-cli serve stop`",
					srv.Root(), session.SockPath(srv.Dir()))
			}

			// Catalog once before listening, so the first client gets an answer
			// rather than a daemon that has not looked yet. A failure here is
			// reported and not fatal: a daemon that refuses to start because the
			// engine is down is one that cannot be running when it comes back.
			ctx := cmd.Context()
			if err := srv.Adopt(ctx, rt); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "sandbox-cli: %v\n  serving anyway; the catalog will fill in when %s answers\n", err, engine)
			} else if err := srv.Save("serve"); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "sandbox-cli: the catalog could not be written: %v\n", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "sandbox-cli serve\n  repository %s\n  socket     %s\n  panes      %d\n",
				srv.Root(), session.SockPath(srv.Dir()), len(srv.Snapshot().Panes))

			// SIGINT and SIGTERM stop the server and leave every container alone.
			// Notified here rather than left to cobra so the message below is
			// printed: "did stopping the daemon kill my agent" is the first thing
			// anybody wonders, and the answer has to be where they will see it.
			ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()
			if err := srv.Serve(ctx, rt); err != nil {
				// Only on a clean stop. Printing it unconditionally meant a *failure*
				// to serve also reported "stopped; containers are untouched", which is
				// true and reads as though the thing had run.
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "stopped; containers are untouched\n")
			return nil
		},
	}
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	cmd.AddCommand(newServeStatusCmd(), newServeStopCmd())
	return cmd
}

func newServeStatusCmd() *cobra.Command {
	var engineFlag, cfgPath string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Whether a session server is running for this repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := sessionDir(cfgPath)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "session  %s\n", dir)
			fmt.Fprintf(out, "socket   %s\n", session.SockPath(dir))

			if err := session.CheckSockPath(dir); err != nil {
				// Said before "not running", because starting one would not help:
				// this daemon could never bind.
				fmt.Fprintf(out, "state    cannot run here\n\n%v\n", err)
				return nil
			}

			pid, alive := servePID(dir)
			switch {
			case alive && pid > 0:
				fmt.Fprintf(out, "state    running (pid %d)\n", pid)
			case alive:
				// Answering, with no readable pid file. "pid 0" would be a lie in the
				// shape of a fact — and 0 is the value that made `serve stop` signal
				// the caller's own process group.
				fmt.Fprintf(out, "state    running (pid unknown — %s is missing or unreadable)\n", session.PIDPath(dir))
			case pid > 0:
				// The pid file outlives a daemon that was killed rather than
				// stopped, which is exactly why the process is checked rather than
				// the file's existence.
				fmt.Fprintf(out, "state    not running (stale pid %d)\n", pid)
			default:
				fmt.Fprintf(out, "state    not running\n")
			}

			if snap, err := readCatalog(dir); err == nil {
				fmt.Fprintf(out, "panes    %d catalogued\n", len(snap.Panes))
			}
			return nil
		},
	}
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	return cmd
}

func newServeStopCmd() *cobra.Command {
	var engineFlag, cfgPath string
	var killPanes bool
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop this repository's session server, leaving its containers running",
		Long: "Signals the daemon to exit. Every container keeps running: the engine owns\n" +
			"them, and the catalog is rebuilt from the engine's labels the next time a\n" +
			"server starts — so stopping the server loses nothing but the socket.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if killPanes {
				// Named in the plan for this track and deliberately not implemented
				// here: phase 1 starts and stops nothing, so a flag that stopped
				// containers would be the one place this command reached past what
				// the phase owns. `sandbox-cli kill` is the verb that exists.
				return errors.New("--kill-panes is not implemented: this server starts and stops no containers yet\n" +
					"  use `sandbox-cli kill <ref>` for a container, or `sandbox-cli clean` to reap finished ones")
			}
			dir, err := sessionDir(cfgPath)
			if err != nil {
				return err
			}
			pid, alive := servePID(dir)
			if !alive {
				return fmt.Errorf("no session server is running for %s", dir)
			}
			if pid <= 0 {
				// Refused rather than approximated, because the approximation is
				// dangerous in a way that is easy to miss: `servePID` returns 0 when the
				// pid file is missing or unreadable, and `kill(0, SIGTERM)` does not mean
				// "no process" — it signals **every process in the caller's process
				// group**. `serve stop` would have terminated the user's shell job and
				// its siblings while leaving the daemon running.
				return fmt.Errorf("a session server is answering on %s but its pid is unknown\n"+
					"  %s is missing or unreadable, so there is no process to signal\n"+
					"  find it with `lsof %s` and stop it by hand",
					session.SockPath(dir), session.PIDPath(dir), session.SockPath(dir))
			}
			if err := stopServe(pid); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "asked pid %d to stop; containers are untouched\n", pid)
			return nil
		},
	}
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	cmd.Flags().BoolVar(&killPanes, "kill-panes", false, "not implemented; see `sandbox-cli kill`")
	return cmd
}

// newPaneCmd is the read side of the catalog.
func newPaneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pane",
		Short: "Panes — the sandboxes the session server knows about",
		Long: "A pane is one sandbox: a container, the worktree it is working in, and how it\n" +
			"is doing. `sandbox-cli list` shows the same containers as a flat table; this\n" +
			"shows them as the session server has them grouped, with the pane ids the\n" +
			"protocol uses.\n\n" +
			"Phase 1 of the session-server track is read-only, so `pane spawn` does not\n" +
			"exist yet: panes are created by the agent wrappers and catalogued here.",
	}
	cmd.AddCommand(newPaneListCmd(), newPaneSpawnCmd(), newPaneKillCmd(), newPaneWaitCmd())
	return cmd
}

// newPaneSpawnCmd is the same launch every wrapper makes, named as a pane.
//
// It exists for two reasons and neither is convenience. The first is that the
// protocol's `pane.spawn` has to have a CLI twin, so the claim "a pane spawned by
// the server is the same container as the equivalent CLI flags" is a comparison
// somebody can run rather than a promise. The second is that the comparison is a
// *test* — `TestPaneSpawnArgvMatchesTheRunPath` — and a test needs both sides to be
// reachable from one process.
//
// Built on exactly the same `runFlags` the `run` command uses, so the sandbox flags
// behave identically and there is no second flag surface to keep in step.
func newPaneSpawnCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "spawn [flags] -- <command>",
		Short: "Start a pane: a detached sandbox recorded in the catalog",
		Long: "The same container `run --detach` starts, with a pane id and a row in the\n" +
			"catalog. Every sandbox flag means what it means elsewhere.\n\n" +
			"`--dry-run` prints the engine command and starts nothing — and is the thing\n" +
			"worth comparing: a pane and the equivalent `run --detach` must produce the\n" +
			"same argv, because both go through runtime.BuildArgs and nothing else does.",
		Example: "  sandbox-cli pane spawn --worktree feat -- npm test\n" +
			"  sandbox-cli pane spawn --dry-run --worktree feat -- npm test",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Detached by definition: a pane is a container somebody looks at later,
			// and the HTTP-shaped half of this protocol has nowhere to hold a pty.
			rf.detach = true
			guest := guestArgs(cmd, args)
			if len(guest) == 0 {
				return fmt.Errorf("no command given; usage: sandbox-cli pane spawn [flags] -- <command> [args...]")
			}
			return execute(rf, guest)
		},
	}
	addRunFlags(cmd, rf)
	return cmd
}

func newPaneKillCmd() *cobra.Command {
	var engineFlag, cfgPath string
	var force bool
	cmd := &cobra.Command{
		Use:   "kill <ref>",
		Short: "Stop a pane",
		Long: "Resolves a pane id, a container name, a short container id, or a branch when\n" +
			"that is unambiguous — and refuses when it is not, because stopping the wrong\n" +
			"agent costs its work.\n\n" +
			"The same verb as `sandbox-cli kill`, which also accepts pane ids. This one\n" +
			"exists so `pane` is a complete noun rather than a half of one.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, engine, err := sessionEngine(cfgPath, engineFlag)
			if err != nil {
				return err
			}
			// The same three functions `sandbox-cli kill` uses, rather than a second
			// resolver: one listing filtered by sandbox.cli, one all-or-nothing
			// resolution, one stop. A second copy is how two commands that name the
			// same container come to disagree about which one that is.
			infos, err := sandboxSessions(cmd.Context(), rt, engine, true)
			if err != nil {
				return err
			}
			targets, err := killTargets(infos, args, false)
			if err != nil {
				return err
			}
			return stopSessions(cmd.Context(), rt, targets, force, cmd.OutOrStdout())
		},
	}
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	cmd.Flags().BoolVar(&force, "force", false, "SIGKILL instead of asking the guest to exit")
	return cmd
}

func newPaneListCmd() *cobra.Command {
	var engineFlag, cfgPath string
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List this repository's panes",
		Long: "Reads the session server when one is running, and the engine directly when one\n" +
			"is not — so this answers the same question either way, and says which source\n" +
			"it used when the answer could differ.\n\n" +
			"Not autostarted. A read-only listing that forks a long-lived daemon is a\n" +
			"surprise, and `serve` is a foreground process in this phase, so there is\n" +
			"nothing to fork it into.",
		Example: "  sandbox-cli pane list\n" +
			"  sandbox-cli pane list --all --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			panes, live, err := panesFor(cmd.Context(), cfgPath, engineFlag, protocol.PaneListParams{All: all})
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), protocol.PaneListResult{Panes: panes})
			}
			printPanes(cmd, panes, live, all)
			return nil
		},
	}
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	cmd.Flags().BoolVar(&all, "all", false, "include panes whose container has exited")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the protocol's own shape")
	return cmd
}

func newPaneWaitCmd() *cobra.Command {
	var engineFlag, cfgPath string
	var states []string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait <ref>",
		Short: "Block until a pane reaches one of the given states",
		Long: "Waits for a pane to reach any of --state, and exits non-zero if the wait\n" +
			"expires. What it is for: starting several agents and being told when one of\n" +
			"them needs you, instead of watching a listing.\n\n" +
			"A timeout is not a failure of the pane — it means the pane was in some other\n" +
			"state when the clock ran out, and the message says which. A script that treats\n" +
			"one as \"the agent failed\" will stop work that was merely slow.\n\n" +
			"Needs a running session server: the wait is the daemon's poll loop, and there\n" +
			"is nothing for this command to block on without one.",
		Example: "  sandbox-cli pane wait p_3f21 --state blocked --state done\n" +
			"  sandbox-cli pane wait feat --state done --timeout 10m",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(states) == 0 {
				return fmt.Errorf("say what to wait for with --state (%s, %s, %s, %s, %s)\n"+
					"  a wait for nothing is a sleep",
					protocol.StateWorking, protocol.StateBlocked, protocol.StateIdle,
					protocol.StateDone, protocol.StateFailed)
			}
			want := make([]protocol.PaneState, 0, len(states))
			for _, st := range states {
				ps := protocol.PaneState(strings.TrimSpace(st))
				if !protocol.KnownPaneState(ps) {
					return fmt.Errorf("unknown state %q (known: %s)", st,
						strings.Join(protocol.PaneStateNames(), ", "))
				}
				want = append(want, ps)
			}

			dir, err := sessionDir(cfgPath)
			if err != nil {
				return err
			}
			if _, alive := servePID(dir); !alive {
				// Said rather than worked around. The other pane commands fall back to
				// reading the engine, and a wait cannot: blocking is the daemon's poll
				// loop, and reimplementing it here would be a second one to keep in step.
				return fmt.Errorf("no session server is running for %s\n"+
					"  `pane wait` blocks in the daemon's own poll loop, so start one with `sandbox-cli serve`",
					repoRootForMessage(dir))
			}

			var res protocol.PaneWaitResult
			// The deadline follows the wait, plus slack for the round trip. A constant
			// here would cut off a legitimate wait and report it as a transport error.
			err = session.CallWithin(session.SockPath(dir), protocol.OpPaneWait,
				protocol.PaneWaitParams{
					Pane:      args[0],
					States:    want,
					TimeoutMS: int(timeout / time.Millisecond),
				}, &res, timeout+30*time.Second)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n",
				res.Pane.ID, res.State, termsafe.Clean(res.Pane.ContainerName))
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", detect.Describe(res.State))
			return nil
		},
	}
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	cmd.Flags().StringArrayVar(&states, "state", nil,
		"a state to wait for; repeatable, and any one of them ends the wait")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "give up after this long")
	return cmd
}

// repoRootForMessage names the repository in an error, preferring the repository to
// the session directory.
//
// The catalog is the better source when there is one — it records the root a daemon
// was opened on — but a session that has never run has no catalog, and falling back
// to the session *directory* made the message name a path under
// ~/.config/sandbox/sessions, which is not a thing the reader typed or cares about.
// The working directory is what they are standing in.
func repoRootForMessage(dir string) string {
	if snap, err := readCatalog(dir); err == nil && snap.Root != "" {
		return snap.Root
	}
	if wd, err := os.Getwd(); err == nil {
		if root, err := worktree.RepoRoot(wd); err == nil {
			return root
		}
		return wd
	}
	return dir
}

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "The session server's catalog for this repository",
	}
	cmd.AddCommand(newSessionSnapshotCmd())
	return cmd
}

func newSessionSnapshotCmd() *cobra.Command {
	var engineFlag, cfgPath string
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Print the whole catalog as JSON",
		Long: "The `session.snapshot` op's own output: workspaces, the worktrees in them, and\n" +
			"the panes. JSON only, because this is the protocol's shape rather than a view\n" +
			"of it — `pane list` is the view.\n\n" +
			"Not to be confused with `sandbox-cli recover`, which is the other thing called\n" +
			"a snapshot here and the more valuable one: that is workspace *files*, kept in\n" +
			"git under refs/sandbox/snapshots/. This is layout. Restoring a layout starts\n" +
			"no agent, and restoring files changes no layout.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			snap, _, err := snapshotFor(cmd.Context(), cfgPath, engineFlag)
			if err != nil {
				return err
			}
			return writeJSON(cmd.OutOrStdout(), snap)
		},
	}
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	return cmd
}

// snapshotFor answers from the daemon when there is one and from the engine when
// there is not, reporting which.
//
// Both paths, rather than requiring the daemon, because the catalog is derived:
// everything phase 1 reports comes from container labels plus git, so a client can
// build it itself. What the daemon adds is *persistence* — the panes whose
// containers are already gone — which is exactly the difference the caller is told
// about rather than left to discover.
func snapshotFor(ctx context.Context, cfgPath, engineFlag string) (protocol.Session, bool, error) {
	dir, err := sessionDir(cfgPath)
	if err != nil {
		return protocol.Session{}, false, err
	}
	if _, alive := servePID(dir); alive {
		var snap protocol.Session
		if err := session.Call(session.SockPath(dir), protocol.OpSnapshot, struct{}{}, &snap); err == nil {
			return snap, true, nil
		}
		// A daemon that is running and not answering is worth a word, and then the
		// direct path: the answer matters more than which door it came through.
		fmt.Fprintf(os.Stderr, "sandbox-cli: the session server is not answering; reading the engine directly\n")
	}

	rt, engine, err := sessionEngine(cfgPath, engineFlag)
	if err != nil {
		return protocol.Session{}, false, err
	}
	// No profile: a listing reads the catalog and records nothing, so it has no
	// business loading — or failing on — the project config.
	srv, err := openSession(cfgPath, engine, "")
	if err != nil {
		return protocol.Session{}, false, err
	}
	if err := srv.Adopt(ctx, rt); err != nil {
		return protocol.Session{}, false, err
	}
	return srv.Snapshot(), false, nil
}

func panesFor(ctx context.Context, cfgPath, engineFlag string, p protocol.PaneListParams) ([]protocol.Pane, bool, error) {
	dir, err := sessionDir(cfgPath)
	if err != nil {
		return nil, false, err
	}
	if _, alive := servePID(dir); alive {
		var res protocol.PaneListResult
		if err := session.Call(session.SockPath(dir), protocol.OpPaneList, p, &res); err == nil {
			return res.Panes, true, nil
		}
		fmt.Fprintf(os.Stderr, "sandbox-cli: the session server is not answering; reading the engine directly\n")
	}
	snap, _, err := snapshotFor(ctx, cfgPath, engineFlag)
	if err != nil {
		return nil, false, err
	}
	// Filtered by the *same* function the daemon applies, rather than by a second
	// copy of the rule. The first version honoured only `All` and silently dropped
	// the workspace and worktree filters, so the two sources answered different
	// questions — invisibly, because no CLI caller sets them yet, which is exactly
	// how it would have stayed wrong until one did.
	return session.FilterPanes(snap, p), false, nil
}

// printPanes renders the table. Values that came off a container label go through
// termsafe.Clean, for the reason `list` already does: a label is text from the
// repository, and a tab-separated table should not be forgeable by a branch name.
func printPanes(cmd *cobra.Command, panes []protocol.Pane, live, all bool) {
	out := cmd.OutOrStdout()
	if len(panes) == 0 {
		if all {
			fmt.Fprintln(out, "No panes for this repository.")
		} else {
			fmt.Fprintln(out, "No panes running. --all includes the ones that have finished.")
		}
		return
	}

	// Whether to mark the legacy rows, by the same rule `showRuntimeColumn` uses
	// for the RUNTIME column: say it where rows *differ*, and not at all where
	// every row is the same.
	//
	// It matters here because until the spawn path moves, **every** container
	// predates the pane label — so marking each row would put the word on every
	// line of every listing, which is how a distinction that will matter later
	// becomes something people learn to skip. Where the listing is uniform the
	// footer says it once.
	var legacy, modern int
	for _, p := range panes {
		if p.Legacy {
			legacy++
		} else {
			modern++
		}
	}
	markLegacy := legacy > 0 && modern > 0

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PANE\tWORKTREE\tKIND\tAGENT\tSTATE\tCONTAINER")
	for _, p := range panes {
		state := string(p.State)
		if p.ExitCode != nil {
			state += " (" + strconv.Itoa(*p.ExitCode) + ")"
		}
		name := p.ContainerName
		if markLegacy && p.Legacy {
			// This pane's id is its container name, and the protocol will not mutate
			// it by a pane id it does not have.
			name += " (pre-session)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			termsafe.Clean(p.ID),
			termsafe.Clean(strings.TrimPrefix(p.WorktreeID, "wt_")),
			termsafe.Clean(string(p.Kind)),
			termsafe.Clean(orDash(p.Agent)),
			state,
			termsafe.Clean(name))
	}
	w.Flush()

	if legacy > 0 && modern == 0 {
		fmt.Fprintf(out, "\nEvery pane here predates the session server, so each one's id is its\n"+
			"container name rather than a pane id. That is expected until the spawn\n"+
			"path moves: nothing has stamped a pane label yet.\n")
	}

	if !live {
		// The one difference between the two sources, stated rather than left to be
		// noticed: without a daemon there is nothing holding the panes whose
		// containers have already been reaped.
		fmt.Fprintln(out, "\nRead from the engine — no session server is running, so panes whose\n"+
			"containers are already gone are not listed. `sandbox-cli serve` keeps them.")
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func writeJSON(w interface{ Write([]byte) (int, error) }, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// openSession opens the catalog for the current directory's repository.
//
// The profile is a parameter rather than resolved here, and that split is the
// whole point. A profile is *stamped into the catalog* as a fact about the run, so
// a repository whose config will not load must not be recorded as `dev` — wrong in
// the one direction that matters, and written down rather than merely assumed. But
// that reasoning applies to the command that writes the catalog, not to the ones
// that read it: an empty profile means "leave whatever the file already says",
// which is exactly right for a listing.
//
// Resolving it here for every caller made `pane list` **fail** in a repository
// whose `.sandbox.yaml` is refused — a read-only listing, refusing because of a
// config it has no use for, while `sandbox-cli list` answered the same question
// about the same containers without complaint. Strictness in the wrong place reads
// as a broken command.
func openSession(cfgPath, engine, profile string) (*session.Server, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return session.Open(wd, profile, engine)
}

// servingProfile resolves the profile `serve` records in the catalog, and refuses
// rather than guessing. This is the caller finding 8 was about: it writes.
func servingProfile(cfgPath string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cfg, err := config.LoadProfile(wd, cfgPath, "")
	if err != nil {
		return "", fmt.Errorf("%w\n  the session server records which profile is in force, so it will not start with an unreadable config", err)
	}
	if cfg.Profile == "" {
		return config.ProfileDev, nil
	}
	return cfg.Profile, nil
}

func sessionDir(cfgPath string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir, err := session.DirFor(wd)
	if err != nil {
		return "", fmt.Errorf("a session is scoped to a repository: %w", err)
	}
	return dir, nil
}

// readCatalog reads session.json without opening a session, so `serve status` can
// report a pane count without creating the directory it is reporting on.
func readCatalog(dir string) (protocol.Session, error) {
	b, err := os.ReadFile(session.StatePath(dir))
	if err != nil {
		return protocol.Session{}, err
	}
	var snap protocol.Session
	err = json.Unmarshal(b, &snap)
	return snap, err
}

// servePID reports the pid a daemon wrote and whether one is actually answering.
//
// Liveness is **the socket**, not the pid file and not the process. A daemon
// killed rather than stopped leaves the pid file behind, and a pid that exists is
// not yet a daemon that has bound — there is a window between writing the pid and
// listening, and another on the way out. Only a dial proves there is something to
// talk to, and talking to it is the only thing a caller wants the answer for.
//
// The pid is read anyway, because "not running (stale pid 4711)" is a more useful
// sentence than "not running" when somebody is looking for the process that is
// holding a socket open.
func servePID(dir string) (int, bool) {
	var pid int
	if b, err := os.ReadFile(session.PIDPath(dir)); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
			pid = n
		}
	}
	conn, err := net.DialTimeout("unix", session.SockPath(dir), 500*time.Millisecond)
	if err != nil {
		return pid, false
	}
	conn.Close()
	return pid, true
}
