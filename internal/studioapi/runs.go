package studioapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/fleet"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// handleListRuns is GET /runs. Query parameters: all=1 includes finished runs
// (default: live only), repo=/branch=/agent= filter by label, fleet=1 shows only
// fleet-launched runs.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := map[string]string{}
	if v := q.Get("repo"); v != "" {
		filter[sandbox.LabelRepo] = v
	}
	if v := q.Get("branch"); v != "" {
		filter[sandbox.LabelBranch] = v
	}
	if v := q.Get("agent"); v != "" {
		filter[sandbox.LabelAgent] = v
	}
	if q.Has("fleet") {
		filter[sandbox.LabelFleet] = "1"
	}
	infos, err := s.listRuns(r.Context(), q.Has("all"), filter)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	runs := make([]Run, 0, len(infos))
	for _, c := range infos {
		runs = append(runs, toRun(c, s.Engine))
	}
	writeJSON(w, http.StatusOK, RunsResponse{Runs: runs})
}

// handleGetRun is GET /runs/{id}.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, toRun(c, s.Engine))
}

// handleCreateRun is POST /runs. It always launches detached — see
// RunCreateRequest's doc comment for why an HTTP request/response cycle has no
// foreground mode to offer.
func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var req RunCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Agent == "" && len(req.Command) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("set agent (with prompt) or command"))
		return
	}
	if req.Agent != "" && len(req.Command) > 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("agent and command are mutually exclusive"))
		return
	}
	if req.Project != "" && req.Worktree != "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("project and worktree are mutually exclusive"))
		return
	}

	opts, err := s.buildRunOptions(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// A before-image, taken immediately before the container starts, so this
	// run's changes can later be told apart from whatever was already
	// uncommitted in the workspace. Best-effort by design: no snapshot means the
	// diff falls back to "what is uncommitted here" and says so, which is the
	// behaviour that existed before — a run must never fail because its safety
	// net could not be strung.
	opts.Baseline = baselineFor(opts.Project, opts.Agent)

	name, err := s.Session.Start(r.Context(), opts, false)
	if err != nil {
		if msg, held := s.nameHeldBy(r.Context(), opts); held {
			writeError(w, http.StatusConflict, fmt.Errorf("%s", msg))
			return
		}
		writeError(w, http.StatusBadGateway, err)
		return
	}
	run, err := s.resolveRun(r.Context(), name)
	if err != nil {
		// The container was launched — that already happened and is not undone by
		// a listing race — but is not showing up in `docker ps` yet. Report the
		// name rather than fail a launch that succeeded.
		writeJSON(w, http.StatusCreated, Run{Name: name})
		return
	}
	writeJSON(w, http.StatusCreated, toRun(run, s.Engine))
}

// buildRunOptions turns a request into sandbox.Options, following the same
// resolution fleet.Runner.options does for a task: a worktree is resolved (and
// created if needed), the agent descriptor supplies its env allowlist and
// autonomous argv, and the login persistence gate is re-checked here because it
// is a property of every path that builds Options, not of the config alone.
func (s *Server) buildRunOptions(req RunCreateRequest) (sandbox.Options, error) {
	project := s.Project
	branch := req.Branch
	var extraMounts []string

	switch {
	case req.Worktree != "":
		info, err := worktree.Resolve(s.Project, req.Worktree)
		if err != nil {
			return sandbox.Options{}, err
		}
		project = info.Path
		if branch == "" {
			branch = req.Worktree
		}
		extraMounts = sandbox.LinkedWorktreeMounts(info.Path)
	case req.Project != "":
		project = req.Project
	}
	if branch == "" {
		branch = worktree.Branch(project)
	}

	opts := sandbox.Options{
		Project:     project,
		Detach:      true,
		RepoID:      s.RepoID,
		Branch:      branch,
		Base:        req.Base,
		Verify:      req.Verify,
		Image:       req.Image,
		Memory:      req.Memory,
		CPUs:        req.CPUs,
		Allow:       req.Allow,
		ExtraMounts: extraMounts,
	}
	for k, v := range req.Env {
		if config.IsReservedEnv(k) {
			return sandbox.Options{}, fmt.Errorf("env %q: %s", k, config.ReservedEnvReason())
		}
		opts.Env = append(opts.Env, k+"="+v)
	}

	if req.Agent != "" {
		agent, ok := agents.Lookup(req.Agent)
		if !ok {
			return sandbox.Options{}, fmt.Errorf("unknown agent %q (known: %s)", req.Agent, strings.Join(agents.Names(), ", "))
		}
		opts.Agent = agent.Name
		opts.EnvAllow = agent.EnvAllow
		opts.Env = append(opts.Env, agent.Env...)
		opts.Prompt = req.Prompt
		opts.Command = fleet.WithVerify(agent.Autonomous(req.Prompt, nil), req.Verify)

		// Same gate fleet.Runner.options applies, and for the same reason: the
		// default auth path is an OAuth refresh token sitting in this directory,
		// readable by the agent, and prod turns persist_auth off so there is
		// nothing there to steal. BuildSpec mounts AuthPersistDir whenever it is
		// non-empty without re-checking the config, so the check belongs on every
		// caller that builds Options — this one included.
		if s.Session.Cfg.PersistAuthEnabled() {
			if dir := config.AgentStateDir(agent.PersistDir); dir != "" {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					return sandbox.Options{}, fmt.Errorf("creating auth persist dir %s: %w", dir, err)
				}
				opts.AuthPersistDir = dir
			}
		}
	} else {
		opts.Command = fleet.WithVerify(req.Command, req.Verify)
	}

	return opts, nil
}

// handleStopRun is POST /runs/{id}/stop.
func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	var body RunStopRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !c.Running() {
		writeJSON(w, http.StatusOK, toRun(c, s.Engine))
		return
	}
	act := s.RT.Stop
	if body.Force {
		act = s.RT.Kill
	}
	if err := act(r.Context(), c.ID); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if updated, err := s.resolveRun(r.Context(), c.ID); err == nil {
		writeJSON(w, http.StatusOK, toRun(updated, s.Engine))
		return
	}
	writeJSON(w, http.StatusOK, toRun(c, s.Engine))
}

// nameHeldBy explains a failed launch when the branch's container name is
// already taken, and reports whether that is what happened.
//
// Called *after* Start fails, never before it. The name is the enforcement:
// docker refuses a duplicate atomically, and sandbox.containerName documents why
// that matters — a check-then-launch has a window in which a second launch
// passes the same check, and two agents in one checkout is silent data loss. So
// this listing only ever explains a refusal that already happened; it never
// decides whether to launch.
//
// Without it the caller got docker's own words, forwarded as a 502: `Conflict.
// The container name "/sandbox-<repo>-<branch>" is already in use by container
// "<64 hex chars>"`. That names neither the branch nor the run nor anything a
// client can act on, and 502 says the daemon misbehaved when in fact it did
// exactly its job.
func (s *Server) nameHeldBy(ctx context.Context, opts sandbox.Options) (string, bool) {
	if opts.Branch == "" || opts.RepoID == "" {
		// A run with no branch gets a timestamped name, which cannot collide.
		return "", false
	}
	infos, err := s.RT.Containers(ctx, map[string]string{
		sandbox.LabelCLI:    "1",
		sandbox.LabelRepo:   opts.RepoID,
		sandbox.LabelBranch: opts.Branch,
	})
	if err != nil || len(infos) == 0 {
		return "", false
	}
	// Newest first, and a live agent outranks a finished one: if both exist, the
	// one the caller needs to know about is the one still writing.
	c := infos[0]
	for i := range infos {
		if infos[i].Running() {
			c = infos[i]
			break
		}
	}
	if c.Running() {
		return fmt.Sprintf(
			"an agent is already running on %q (run %s); stop it before starting another — "+
				"two agents in one checkout overwrite each other's work",
			opts.Branch, shortID(c.ID)), true
	}
	// Names this API's own operation, not the CLI's. The caller is an HTTP
	// client that has just been refused; telling it to go and run a terminal
	// command is an instruction it cannot follow.
	return fmt.Sprintf(
		"a finished run (%s, exit %d) still holds %q's container name; "+
			"read it with GET /v1/runs/%s/logs, then DELETE /v1/runs/%s to run again",
		shortID(c.ID), c.ExitCode, opts.Branch, shortID(c.ID), shortID(c.ID)), true
}

// handleDeleteRun is DELETE /v1/runs/{id}: reap a finished run's container.
//
// The API could create runs and not remove them, which left a client stuck the
// moment a branch's container name was taken — the launch refusal could only be
// acted on by leaving Studio for a terminal. A control plane that can start
// something it cannot clean up is a control plane you have to work around.
//
// A running container is refused rather than reaped. `stop` and `remove` are
// different acts and the difference is an agent's unsaved work, so this makes
// you say which you meant — the same reason `kill` is a separate word from
// `stop` in the CLI.
//
// What this discards is the container: its logs and its exit code, which for a
// detached run are the whole record that it happened. The *work* is untouched —
// it is in the workspace, which is a bind mount and outlives every container
// that ever wrote to it.
func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if c.Running() {
		writeError(w, http.StatusConflict, fmt.Errorf(
			"run %s is still running; stop it first (POST /v1/runs/%s/stop) — "+
				"removing a live container discards whatever its agent had not written yet",
			shortID(c.ID), shortID(c.ID)))
		return
	}
	if err := s.RT.Remove(r.Context(), c.ID); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// baselineFor records the workspace as it stands, and returns the commit.
//
// Through rescue's own Begin/Start/Stop rather than a second snapshot
// implementation: that path takes one snapshot before its ticker ever fires,
// writes through a private GIT_INDEX_FILE so the user's index, HEAD, branches
// and working tree are untouched, and reads content through a scratch git
// directory so the repository's own config cannot define a filter driver. None
// of that is worth reproducing approximately.
//
// Stop is immediate and deliberate. This wants a before-image, not a running
// safety net — the API server is not the process supervising this container, and
// leaving a session open would report every run as crashed to `sandbox-cli
// recover`, which reads an unclosed manifest as exactly that.
func baselineFor(workspace, agent string) string {
	if workspace == "" {
		return ""
	}
	snap := rescue.Begin(workspace, agent, baselineInterval, baselineRetention)
	if snap == nil {
		return "" // not a repository, or snapshots switched off
	}
	snap.Start()
	snap.Stop("baseline", nil)
	return snap.LastCommit()
}

const (
	// Long enough that the ticker never fires: this run wants the one snapshot
	// loop() takes before it starts waiting, and nothing after it.
	baselineInterval = time.Hour
	// The retention rescue itself uses, since these age out through the same
	// pruning as any other snapshot.
	baselineRetention = 14 * 24 * time.Hour
)
