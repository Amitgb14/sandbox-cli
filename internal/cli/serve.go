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
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/session"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
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
			srv, err := openSession(cfgPath, engine)
			if err != nil {
				return err
			}

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
	cmd.AddCommand(newPaneListCmd())
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
	srv, err := openSession(cfgPath, engine)
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
func openSession(cfgPath, engine string) (*session.Server, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	// The error is returned, not swallowed into a "dev" default. The profile is
	// stamped into the catalog as a fact about the run, and a repository whose
	// config trips ErrRestrictedProjectKeys — or fails to load for any other reason
	// — would otherwise be recorded as dev: wrong in the one direction that
	// matters, and recorded rather than merely assumed. Every other command fails
	// here too, so this also stops `serve` being the one that quietly does not.
	cfg, err := config.LoadProfile(wd, cfgPath, "")
	if err != nil {
		return nil, err
	}
	profile := cfg.Profile
	if profile == "" {
		profile = config.ProfileDev
	}
	return session.Open(wd, profile, engine)
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
