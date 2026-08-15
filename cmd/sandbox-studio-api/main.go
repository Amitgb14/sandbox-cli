// Command sandbox-studio-api runs the local HTTP control plane for
// sandbox-cli — see internal/studioapi and docs/studio-api/README.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/history"
	"github.com/Amitgb14/sandbox-cli/internal/studioapi"
)

// repeatedFlag collects one or more repetitions of the same string flag
// (-cors-origin, -allow-host).
type repeatedFlag []string

func (c *repeatedFlag) String() string { return strings.Join(*c, ",") }
func (c *repeatedFlag) Set(v string) error {
	*c = append(*c, v)
	return nil
}

// hostOf is the host half of a listen address, for a message that suggests a
// tunnel to it. Empty or wildcard addresses have no name worth printing, so they
// become a placeholder the reader substitutes.
func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return "your-host"
	}
	return host
}

// loopbackAddr reports whether a listen address keeps this server reachable only
// from this machine. An empty or wildcard host ("", "0.0.0.0", "::") does not.
func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox-studio-api:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr           string
		project        string
		cfgPath        string
		profile        string
		token          string
		origins        repeatedFlag
		hosts          repeatedFlag
		historyDB      string
		historyRetain  time.Duration
		usageRefreshIn time.Duration
	)
	flag.StringVar(&addr, "addr", "127.0.0.1:8787",
		"address to listen on — loopback by default; see docs/studio-api/README.md before binding this to a network interface")
	flag.StringVar(&project, "project", "",
		"host directory this server manages (default: current directory)")
	flag.StringVar(&cfgPath, "config", "", "explicit sandbox config file path")
	flag.StringVar(&profile, "profile", "", "security profile: dev (default) or prod")
	flag.StringVar(&token, "token", os.Getenv("SANDBOX_STUDIO_TOKEN"),
		"bearer token required on every request but /health (default: $SANDBOX_STUDIO_TOKEN, none if unset)")
	flag.StringVar(&historyDB, "history-db", "",
		"path to a SQLite index over the audit log; empty means scan the log, which is the default and always correct")
	flag.DurationVar(&historyRetain, "history-retain", 0,
		"drop indexed runs older than this on start (e.g. 2160h for 90 days); 0 keeps everything the log holds")
	flag.DurationVar(&usageRefreshIn, "usage-refresh-interval", 0,
		"how often to make the agent refresh the usage reading (e.g. 10m); off by default because each refresh spends a request from the window it measures, and current Claude Code may not record usage at all")
	flag.Var(&origins, "cors-origin",
		"origin allowed to drive this control plane cross-origin (repeatable); default: none, so a web page cannot reach it at all")
	flag.Var(&hosts, "allow-host",
		"additional Host header value to answer to, beyond the loopback names always accepted (repeatable); needed when reaching a rebound -addr by name")
	flag.Parse()

	// Validated here, before anything with a side effect. `openHistory` below
	// syncs the index and, with -history-retain, prunes rows out of it — so a
	// mistyped interval used to delete indexed history and *then* exit without
	// ever serving. A refusal that comes after the damage is not a refusal.
	//
	// A refusal rather than a clamp, and negatives are refused too: silently
	// substituting a different number for the one that was typed is how a setting
	// stops meaning what it says, and `-usage-refresh-interval -10m` reading as
	// "off" would swallow exactly the typo this check exists to catch. Zero is the
	// one way to say off. Claude Code will not refetch inside its own interval
	// regardless, so requests below the floor buy nothing at all.
	if usageRefreshIn != 0 && usageRefreshIn < time.Minute {
		return fmt.Errorf("-usage-refresh-interval %s is not usable: each refresh spends a request "+
			"from the window it measures, and the agent will not refetch more than once a minute "+
			"regardless. Use a minute or more, or 0 to turn it off", usageRefreshIn)
	}

	if project == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolving current directory: %w", err)
		}
		project = wd
	}

	cfg, err := config.LoadProfile(project, cfgPath, profile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	if err := config.ValidateProfile(cfg.Profile, cfg); err != nil {
		return fmt.Errorf("profile %q: %w", cfg.Profile, err)
	}

	srv, err := studioapi.New(cfg, project)
	if err != nil {
		return err
	}
	srv.CORSOrigins = origins
	// Off loopback with no token is refused rather than warned about.
	//
	// This process holds the docker socket, so anything that can reach the port
	// and be answered can start a container mounting `/` — root on this machine.
	// The token is the only thing standing between a routable port and that, and
	// a warning is not a control: the deployment it protects is the one nobody is
	// watching. Loopback keeps its old behaviour, where the operating system is
	// the boundary and an unauthenticated daemon is a reasonable default.
	if !loopbackAddr(addr) && token == "" {
		fmt.Fprintf(os.Stderr,
			"sandbox-studio-api: refusing to listen on %s without -token.\n"+
				"  This process can start containers with the docker socket, so an unauthenticated\n"+
				"  routable port is root on this machine for anyone who can reach it.\n"+
				"  Set -token (or $SANDBOX_STUDIO_TOKEN), or bind a loopback address and reach it\n"+
				"  through an SSH tunnel: ssh -N -L 8787:127.0.0.1:8787 you@%s\n", addr, hostOf(addr))
		os.Exit(2)
	}

	srv.Token = token
	srv.AllowedHosts = hosts

	// The index is optional and stays optional. Everything it answers, the log
	// answers too — more slowly, and always correctly — so a database that
	// cannot be opened is a warning and not a failed start. The record is the
	// file; this is a view of it.
	if historyDB != "" {
		h, err := openHistory(historyDB, historyRetain)
		if err != nil {
			log.Printf("sandbox-studio-api: history index unavailable, falling back to scanning the log: %v", err)
		} else {
			srv.History = h
			defer h.Close()
		}
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("sandbox-studio-api listening on %s (project %s, engine %s, profile %s)",
			addr, project, srv.Engine, cfg.Profile)
		if srv.RepoID == "" {
			// Said here rather than left to the first request. The server runs
			// without a repository on purpose — a run naming its own project still
			// works — but everything addressed by branch (the worktree listing, the
			// diffs, any run asking for a worktree) fails, one 500 at a time, with
			// nothing in the message naming the thing that was wrong at startup.
			//
			// The usual way to arrive here is compose: `${PWD}` is the directory you
			// launched from, while compose finds its file by walking up from there,
			// so launching in a subdirectory mounts and manages the subdirectory.
			log.Printf("sandbox-studio-api: %s is not a git repository — worktrees, diffs and any run asking for a branch "+
				"will fail; point -project at the repository root (with docker compose, set SANDBOX_PROJECT or launch from it)", project)
		}
		if token == "" {
			log.Printf("sandbox-studio-api: no -token set — every request but /health is unauthenticated")
		}
		if !loopbackAddr(addr) {
			// Said once, at the moment it becomes true, because the whole trust model
			// below this line assumes only this machine can open a connection.
			log.Printf("sandbox-studio-api: %s is not a loopback address — anything that can route to this "+
				"host can now ask it to start containers; there is no TLS here, so the token and "+
				"everything it protects cross the network in cleartext", addr)
		}
		// Stated rather than assumed, in both directions: a refresh costs a request
		// from the subscription it reports on, and a deployment that cannot refresh
		// at all should say so at startup instead of leaving someone waiting for a
		// number that will never move.
		switch {
		case usageRefreshIn <= 0:
			// Off is off; nothing to say.
		case srv.StartUsageRefresh(ctx, usageRefreshIn):
			log.Printf("sandbox-studio-api: refreshing the usage reading every %s — each one spends a "+
				"request from the window it measures (-usage-refresh-interval 0 turns it off)", usageRefreshIn)
		default:
			log.Printf("sandbox-studio-api: not refreshing usage — the agent that records these numbers " +
				"is not on this server's PATH. The figures are still read and shown; only advancing them " +
				"needs the agent, which is what running the API on your host gives it")
		}
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}

// openHistory opens the index, brings it up to date with the log, and applies
// retention.
//
// Synced here rather than per request: a rebuild reads the log, which is the
// very thing the index exists to stop doing on every query. New runs appended
// after this are picked up on the next start — the index is for history, and
// history is what has already happened.
func openHistory(path string, retain time.Duration) (*history.DB, error) {
	h, err := history.Open(path)
	if err != nil {
		return nil, err
	}
	dir := config.AuditDir()
	if dir == "" {
		return h, nil
	}
	if err := h.Sync(audit.Generations(filepath.Join(dir, "sessions.jsonl"))); err != nil {
		h.Close()
		return nil, fmt.Errorf("indexing the log: %w", err)
	}
	if retain > 0 {
		// Retention on the index only. The log keeps its own size-based rotation,
		// and the two are separate on purpose: bounding a view by age is a
		// preference, while deleting the record is not this flag's business.
		if n, err := h.Prune(time.Now().Add(-retain)); err == nil && n > 0 {
			log.Printf("sandbox-studio-api: dropped %d indexed runs older than %s (the log is untouched)", n, retain)
		}
	}
	return h, nil
}
