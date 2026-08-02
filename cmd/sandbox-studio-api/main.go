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
	"strings"
	"syscall"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
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
		addr    string
		project string
		cfgPath string
		profile string
		token   string
		origins corsOrigins
	)
	flag.StringVar(&addr, "addr", "127.0.0.1:4319",
		"address to listen on — loopback by default; see docs/studio-api/README.md before binding this to a network interface")
	flag.StringVar(&project, "project", "",
		"host directory this server manages (default: current directory)")
	flag.StringVar(&cfgPath, "config", "", "explicit sandbox config file path")
	flag.StringVar(&profile, "profile", "", "security profile: dev (default) or prod")
	flag.StringVar(&token, "token", os.Getenv("SANDBOX_STUDIO_TOKEN"),
		"bearer token required on every request but /health (default: $SANDBOX_STUDIO_TOKEN, none if unset)")
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
