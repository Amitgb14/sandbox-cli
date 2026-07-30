package studioapi

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/fleet"
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
		runs = append(runs, toRun(c))
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
	writeJSON(w, http.StatusOK, toRun(c))
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

	name, err := s.Session.Start(r.Context(), opts, false)
	if err != nil {
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
	writeJSON(w, http.StatusCreated, toRun(run))
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
		writeJSON(w, http.StatusOK, toRun(c))
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
		writeJSON(w, http.StatusOK, toRun(updated))
		return
	}
	writeJSON(w, http.StatusOK, toRun(c))
}
