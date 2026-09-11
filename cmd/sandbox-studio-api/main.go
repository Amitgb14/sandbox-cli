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
	"github.com/Amitgb14/sandbox-cli/internal/qrterm"
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

// pairingBlock is what -pair and -print-pairing print: anything worth knowing
// before scanning, the code, the link, and the sentence that has to follow a
// secret onto a screen. Built before the server starts, so a link that cannot
// work is refused while nothing has happened yet; printed once it is listening,
// so a daemon that never came up has not published its token on the way down.
func pairingBlock(o studioapi.PairingOptions, colour bool) (string, error) {
	p, err := studioapi.ResolvePairing(o)
	if err != nil {
		return "", err
	}
	code, err := qrterm.Render(p.Link, colour)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("sandbox-studio-api: pairing link for Sandbox Studio — scan it with the app\n")
	for _, n := range p.Notes {
		fmt.Fprintf(&b, "  note: %s\n", n)
	}
	b.WriteString(code)
	b.WriteString(p.Link + "\n")
	b.WriteString("Treat this like a password: anyone with it can drive your agents.\n")
	return b.String(), nil
}

// isTerminal reports whether f is a terminal rather than a file or a pipe, which
// decides whether the QR code is painted: escape codes fix its polarity on a
// light theme and are noise in a log.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// shortHostname is the default label a paired phone shows: the machine's name
// without its domain, since "amits-mbp" is what somebody recognises and
// "amits-mbp.corp.example.com" is what gets truncated.
func shortHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	name, _, _ := strings.Cut(h, ".")
	return name
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
		probeIn        time.Duration
		pair           bool
		printPairing   bool
		pairURL        string
		pairName       string
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
	flag.DurationVar(&probeIn, "probe-interval", 5*time.Minute,
		"how often to record whether each agent's provider is answering, for the uptime strip on Studio's Routing screen; 0 turns it off")
	flag.Var(&origins, "cors-origin",
		"origin allowed to drive this control plane cross-origin (repeatable); default: none, so a web page cannot reach it at all")
	flag.Var(&hosts, "allow-host",
		"additional Host header value to answer to, beyond the loopback names always accepted (repeatable); needed when reaching a rebound -addr by name")
	// Pairing is opt-in, and never the default: the block carries the bearer
	// token, and this process's stderr is routinely somebody else's file — docker
	// logs, a launchd log, a CI transcript — where a token printed on every start
	// would sit for as long as the log is kept.
	flag.BoolVar(&pair, "pair", false,
		"print a QR code and link for pairing Sandbox Studio for iOS, once the server is listening; it contains the token, so it is printed only when asked")
	flag.BoolVar(&printPairing, "print-pairing", false,
		"print the pairing QR code and link and exit without serving, for a daemon already running with the same -token, -addr or -pair-url, and -allow-host")
	flag.StringVar(&pairURL, "pair-url", "",
		"address the phone should dial, e.g. http://192.168.1.20:8787 (default: derived from -addr; a loopback -addr is refused, since a phone cannot reach it)")
	flag.StringVar(&pairName, "pair-name", "",
		"label a paired phone shows for this daemon (default: this machine's short hostname)")
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

	if pairName == "" {
		pairName = shortHostname()
	}
	pairing := studioapi.PairingOptions{
		URL:          pairURL,
		Addr:         addr,
		Token:        token,
		Name:         pairName,
		AllowedHosts: hosts,
		OutboundIP:   studioapi.OutboundIP,
	}
	if printPairing {
		// Before the config is loaded, because nothing here needs it: this
		// prints facts about flags and exits, and a project that fails to load
		// should not stand between somebody and the link for a daemon that is
		// already running fine.
		block, err := pairingBlock(pairing, isTerminal(os.Stderr))
		if err != nil {
			return fmt.Errorf("not printing a pairing link: %w", err)
		}
		fmt.Fprint(os.Stderr, block)
		return nil
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
	if !studioapi.IsLoopbackHost(addr) && token == "" {
		fmt.Fprintf(os.Stderr,
			"sandbox-studio-api: refusing to listen on %s without -token.\n"+
				"  This process can start containers with the docker socket, so an unauthenticated\n"+
				"  routable port is root on this machine for anyone who can reach it.\n"+
				"  Set -token (or $SANDBOX_STUDIO_TOKEN), or bind a loopback address and reach it\n"+
				"  through an SSH tunnel: ssh -N -L 8787:127.0.0.1:8787 you@%s\n", addr, hostOf(addr))
		os.Exit(2)
	}

	// Resolved here, with the other refusals and before anything is served: a
	// -pair that cannot produce a working link is somebody standing at a
	// terminal waiting for a code, and a daemon that starts anyway leaves them
	// scrolling for a QR code that is not there.
	var pairingText string
	if pair {
		pairing.Serving = true
		pairingText, err = pairingBlock(pairing, isTerminal(os.Stderr))
		if err != nil {
			return fmt.Errorf("-pair: not printing a pairing link: %w", err)
		}
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

	// Listen before announcing anything. "listening on" used to be logged ahead
	// of a bind that could still fail, and the pairing block must not be: a port
	// already in use would otherwise print a token for a server that never
	// answered.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
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
		if !studioapi.IsLoopbackHost(addr) {
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
		// Said out loud because it is the one thing this daemon does on a timer
		// without being asked: outbound requests to vendor endpoints whether or
		// not anybody launches anything. They carry no credentials and record
		// nothing about the user — see internal/studioapi/probelog.go — but a
		// process making network calls on its own should say so at startup.
		srv.StartProbing(ctx, probeIn)
		if probeIn > 0 {
			log.Printf("sandbox-studio-api: recording provider uptime every %s — one credential-free "+
				"HEAD request per provider (-probe-interval 0 turns it off)", probeIn)
		} else {
			log.Printf("sandbox-studio-api: provider uptime is not being recorded (-probe-interval 0); " +
				"the strip on the Routing screen will show only history already on disk")
		}
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
		if pairingText != "" {
			// Plain Fprint, not log: a timestamp prefix on each line would
			// break the code into something a camera cannot read.
			fmt.Fprint(os.Stderr, pairingText)
		}
		errCh <- httpSrv.Serve(ln)
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
