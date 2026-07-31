// Command sandbox-studio-api runs the local HTTP control plane for
// sandbox-cli — see internal/studioapi and docs/studio-api/README.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
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

// corsOrigins collects one or more repeated -cors-origin flags.
type corsOrigins []string

func (c *corsOrigins) String() string { return strings.Join(*c, ",") }
func (c *corsOrigins) Set(v string) error {
	*c = append(*c, v)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox-studio-api:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr          string
		project       string
		cfgPath       string
		profile       string
		token         string
		origins       corsOrigins
		historyDB     string
		historyRetain time.Duration
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
	flag.Var(&origins, "cors-origin",
		"origin allowed to read cross-origin responses (repeatable); default: none, so only same-origin callers can read a response")
	flag.Parse()

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
	srv.Token = token

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
		if token == "" {
			log.Printf("sandbox-studio-api: no -token set — every request but /health is unauthenticated")
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
