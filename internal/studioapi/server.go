// Package studioapi is the HTTP control plane for sandbox-cli: a local API
// server a frontend ("Sandbox Studio") talks to instead of shelling out to the
// CLI itself.
//
// It is deliberately thin. Every handler resolves to the same machinery the CLI
// commands use — sandbox.Session, runtime.Runtime/Inspector/Controller,
// internal/worktree, internal/agents, internal/rescue — so a run launched here
// is isolated exactly as one launched from a terminal, and this package adds no
// container logic of its own. See docs/studio-api/README.md for the contract
// and the trust model of running an HTTP server capable of starting containers.
package studioapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// engineRuntime is the slice of the container backend the API needs: the same
// three capabilities internal/cli's session commands are built on
// (runtime.Inspector/Controller, plus Runtime itself to launch new containers),
// so a run started here is found, read, and stopped through the identical
// mechanism as one started by hand.
type engineRuntime interface {
	runtime.Runtime
	runtime.Inspector
	runtime.Controller
}

// Server holds everything the HTTP handlers need. One Server manages one host
// project directory — the same "which project" question every sandbox-cli
// invocation answers via --project/cwd.
type Server struct {
	Session *sandbox.Session
	RT      engineRuntime
	Project string // resolved absolute host directory
	RepoID  string // worktree.RepoID(Project); "" outside a git repo
	Engine  string // "docker" | "podman"

	// CORSOrigins is the exact set of origins allowed to read cross-origin
	// responses (Studio's own frontend, typically a Next dev server on another
	// port). Empty means no CORS headers at all — combined with binding to
	// loopback by default, that is what stops an arbitrary web page's JavaScript
	// from driving this control plane; see docs/studio-api/README.md.
	CORSOrigins []string

	// Token, when non-empty, is required as `Authorization: Bearer <Token>` on
	// every request but /health. A second, cheap line of defense on top of
	// loopback binding and CORS: neither stops another local process (or
	// malicious browser extension) that can already reach 127.0.0.1.
	Token string

	// AllowedHosts are additional Host header values this server answers to,
	// beyond the loopback names it always accepts. Anything else is refused —
	// see hostAllowed for the DNS-rebinding attack that check exists to stop.
	AllowedHosts []string
}

// New builds a Server for the given resolved config and project directory.
//
// It does not touch docker: Available() is checked per-request by /health, and
// every other handler surfaces a normal error if the daemon is unreachable, the
// same way the CLI commands do rather than refusing to start.
func New(cfg config.Config, project string) (*Server, error) {
	sess := sandbox.New(cfg)
	rt, ok := sess.Runtime.(engineRuntime)
	if !ok {
		return nil, fmt.Errorf("engine backend %T does not support inspecting or controlling containers", sess.Runtime)
	}
	engine := cfg.Engine
	if engine == "" {
		engine = "docker"
	}
	// Best-effort: a project outside a git repository still runs, just with no
	// sandbox.repo label and so no repo-scoped filtering — the same as a plain
	// `sandbox-cli run` outside a repo.
	repoID, _ := worktree.RepoID(project)
	return &Server{
		Session: sess,
		RT:      rt,
		Project: project,
		RepoID:  repoID,
		Engine:  engine,
	}, nil
}

// Handler returns the routed, middleware-wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /agents", s.handleAgents)
	mux.HandleFunc("GET /runs", s.handleListRuns)
	mux.HandleFunc("POST /runs", s.handleCreateRun)
	mux.HandleFunc("GET /runs/{id}", s.handleGetRun)
	mux.HandleFunc("POST /runs/{id}/stop", s.handleStopRun)
	mux.HandleFunc("POST /runs/{id}/recover", s.handleRecoverRun)
	mux.HandleFunc("GET /runs/{id}/logs", s.handleRunLogs)
	mux.HandleFunc("GET /runs/{id}/metrics", s.handleRunMetrics)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("GET /worktrees", s.handleListWorktrees)
	mux.HandleFunc("POST /worktrees", s.handleCreateWorktree)
	// {branch...}, not {branch}: a branch name may contain slashes and usually
	// does (feat/studio-api), and a single-segment wildcard makes exactly those
	// branches unaddressable. The trailing-wildcard form matches the rest of the
	// path whole, and `GET /worktrees` still routes to its own pattern above.
	mux.HandleFunc("GET /worktrees/{branch...}", s.handleGetWorktree)
	mux.HandleFunc("DELETE /worktrees/{branch...}", s.handleDeleteWorktree)
	return s.withMiddleware(mux)
}

// withMiddleware applies panic recovery, the two request-origin checks
// (internal/studioapi/guard.go), CORS, the optional bearer token, and the body
// limit — in that order, and the order carries the reasoning:
//
//   - recovery outermost, so a panic anywhere below still answers with JSON
//     instead of a closed connection;
//   - Host before anything that trusts r.Host, since the same-origin escape in
//     originAllowed is only sound once Host is known to be one of ours;
//   - CORS headers before the OPTIONS short-circuit, so a preflight gets its
//     answer without needing a token — but *after* the Host check, so a rebound
//     name cannot preflight its way in either;
//   - Origin before the token, because an unlisted origin is refused whether or
//     not it guessed a token.
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeError(w, http.StatusInternalServerError, fmt.Errorf("internal error: %v", rec))
			}
		}()
		if !s.hostAllowed(r) {
			writeError(w, http.StatusForbidden, fmt.Errorf(
				"refusing a request for host %q: this control plane answers to loopback names only, "+
					"which is what stops a web page from reaching it by rebinding its own hostname to 127.0.0.1 "+
					"(pass -allow-host %s if you meant to reach it by that name)", r.Host, r.Host))
			return
		}
		s.applyCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !s.originAllowed(r) {
			writeError(w, http.StatusForbidden, fmt.Errorf(
				"origin %q is not allowed to drive this control plane (pass -cors-origin %s if it is yours)",
				r.Header.Get("Origin"), r.Header.Get("Origin")))
			return
		}
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid bearer token"))
			return
		}
		if err := checkContentType(r); err != nil {
			writeError(w, http.StatusUnsupportedMediaType, err)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || !containsString(s.CORSOrigins, origin) {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// dockerAvailable reports whether the configured engine can actually be asked
// anything right now, for /health.
func (s *Server) dockerAvailable(ctx context.Context) bool {
	return s.RT.Available(ctx) == nil
}
