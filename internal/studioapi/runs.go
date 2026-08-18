package studioapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/fleet"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
	"github.com/Amitgb14/sandbox-cli/internal/routing"
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
	if req.Project != "" && req.Repo != "" {
		// Two answers to "which repository", one a registered id and one a raw
		// path. Refusing beats picking, and picking the id would quietly ignore
		// the more specific of the two.
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"repo and project are mutually exclusive: repo names a registered repository, project names a host directory"))
		return
	}

	// Tighten-only, enforced here rather than trusted to a client.
	//
	// `allow` is the one network field a request may carry, and on a daemon
	// configured `mode: none` it is not a narrowing at all: BuildSpec reads a
	// non-empty Allow as switching the allowlist *on*, which then promotes the
	// container off `--network none` onto the sandbox bridge ("allowlist needs
	// networking"). So one domain in a request would hand a run networking the
	// daemon was configured not to give it — a request loosening the posture,
	// which is the thing the whole layering exists to prevent. The CLI's
	// `--allow` is a different act: it is typed by the person who owns the
	// machine.
	if len(req.Allow) > 0 && s.Session.Cfg.Network.Mode == "none" {
		writeError(w, http.StatusUnprocessableEntity, fmt.Errorf(
			"this daemon is configured to reach nothing (network mode \"none\"), and adding domains would "+
				"turn networking back on for this run — a request may narrow the egress posture, never widen it.\n"+
				"  Change the mode where the daemon reads its config, and restart it"))
		return
	}

	// Ports are checked here as well as inside BuildSpec, and the difference is
	// which answer the caller gets: BuildSpec's refusal arrives through
	// Session.Start, which this handler reports as a 502 — "the daemon is broken"
	// for what is a typo in a port. Asking the same normaliser first turns it back
	// into the 400 it is, with the message that function already writes.
	if _, err := sandbox.NormalizePublish(req.Publish); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	opts, err := s.buildRunOptions(r.Context(), req)
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

	// The workspace as it stands, for the *routing* question rather than the
	// diffing one: whether this run wrote anything at all decides whether a
	// failure may be retried with the next agent. A separate reading because it
	// is a separate comparison — Baseline is a snapshot commit, and what the
	// supervisor compares is the tree, both sides of it read the same way.
	before := s.sv().fingerprint(opts.Project)

	// StartRecorded rather than Start: this run is detached, so its line says
	// only that it launched — and the supervisor needs that same record to write
	// the partner line when the container ends. See supervisor.recordEnding.
	name, launched, err := s.Session.StartRecorded(r.Context(), opts, false)
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

	// Every run is watched, not only the ones with somewhere to fall through to.
	//
	// Two jobs, and the second is why the condition went away. Routing's retry
	// needs a chain; *recording what happened* needs nothing but a container that
	// will end. A detached run's audit line is written at launch — there is no
	// exit code to wait for — so without something watching, the log's answer to
	// "did it pass" was a placeholder 0 for every run Studio ever started.
	s.sv().supervise(&watch{
		container: run.ID,
		name:      name,
		req:       req,
		agent:     opts.Agent,
		remaining: remainingAfter(chainFor(req), opts.Agent),
		workspace: opts.Project,
		before:    before,
		routeID:   opts.RouteID,
		attempt:   opts.RouteAttempt,
		meta:      launched,
	})
	writeJSON(w, http.StatusCreated, toRun(run, s.Engine))
}

// buildRunOptions turns a request into sandbox.Options, following the same
// resolution fleet.Runner.options does for a task: a worktree is resolved (and
// created if needed), the agent descriptor supplies its env allowlist and
// autonomous argv, and the login persistence gate is re-checked here because it
// is a property of every path that builds Options, not of the config alone.
func (s *Server) buildRunOptions(ctx context.Context, req RunCreateRequest) (sandbox.Options, error) {
	// Which repository this run is about, before anything else is decided: a
	// worktree is resolved inside it, and with no worktree it *is* the workspace.
	// An unregistered id refuses here rather than silently falling back to the
	// daemon's own project, which would run the agent in a repository the request
	// did not name.
	sc, err := s.scopeFor(req.Repo)
	if err != nil {
		return sandbox.Options{}, err
	}
	project := sc.Project
	repoID := sc.RepoID
	branch := req.Branch
	var extraMounts []string

	switch {
	case req.Worktree != "":
		info, err := worktree.Resolve(sc.Project, req.Worktree)
		if err != nil {
			return sandbox.Options{}, err
		}
		project = info.Path
		if branch == "" {
			branch = req.Worktree
		}
		extraMounts = sandbox.LinkedWorktreeMounts(info.Path)
		// repoID stays the scope's: a linked worktree belongs to the same
		// repository, which is the whole point of addressing it by branch.
	case req.Project != "":
		project = req.Project
		// Recomputed, not inherited. The repo id is part of the container's
		// identity — it becomes the sandbox.repo label and, through
		// containerName, half the container's name — and every later command
		// reads those labels as fact: `list --repo`, `fleet land`, the
		// one-agent-per-branch guarantee docker's duplicate-name refusal
		// provides. Stamping this server's repo on a run mounting a different
		// checkout files it under a repository it never touched, and two
		// different repos' `main` would then collide on one container name.
		repoID, _ = worktree.RepoID(project)
	}
	if branch == "" {
		branch = worktree.Branch(project)
	}

	if req.Console && req.Verify != "" {
		// Verify's exit code is the container's, and that is the whole point of
		// it: `land` reads it to decide whether the work is done. An interactive
		// session's exit code says when somebody closed the window, which is not
		// an answer to that question. Refusing beats quietly picking one.
		return sandbox.Options{}, errors.New("console and verify cannot be combined: " +
			"verify decides the run's exit code, and an interactive session's exit code is whenever you quit")
	}
	if req.SkipPermissions && req.Agent == "" {
		return sandbox.Options{}, errors.New("skip_permissions needs an agent: a plain command is already whatever argv you gave it")
	}
	if req.Resume != "" && req.Agent == "" {
		return sandbox.Options{}, errors.New("resume needs an agent: only an agent has conversations")
	}
	if req.Resume != "" && !req.Console {
		// A headless resume would replay one prompt into an old conversation and
		// exit. That is a real thing to want, but it is not what anyone means by
		// "carry this on", and the request as written says nothing about what to
		// say next.
		return sandbox.Options{}, errors.New("resume needs console: resuming a conversation is something you do interactively")
	}
	if req.Console && len(req.Fallback) > 0 {
		// Routing changes which agent runs, and a console run's argv is built from
		// the *chosen* agent's descriptor — so a gate applied to the requested one
		// proves nothing. Rather than gate twice and hope, the combination is
		// refused: an interactive session is something somebody is watching, and
		// silently attaching them to a different agent than they picked is worse
		// than asking them to pick again.
		return sandbox.Options{}, errors.New(
			"console and fallback cannot be combined: routing would start a different agent than the one you are about to attach to, " +
				"and an interactive session is the case where that matters most")
	}
	if req.Resume != "" && len(req.Fallback) > 0 {
		// A session id is a primary key into one vendor's store. Resuming it with
		// another agent cannot work — see internal/handoff — so the request is
		// refused rather than routed into a failure inside the container.
		return sandbox.Options{}, errors.New(
			"resume and fallback cannot be combined: a session id belongs to the agent that wrote it, and another agent cannot reopen it")
	}
	if req.Console && req.Prompt != "" && req.Agent != "" {
		if d, ok := agents.Lookup(req.Agent); ok && !d.CanSeedConsole() {
			// Refused rather than dropped. Silently starting the session without
			// the prompt would look like the agent ignored it, and appending it
			// anyway is what this refusal replaces: opencode reads a lone
			// positional as a directory, so the run died with "Failed to change
			// directory to /workspace/<your prompt>".
			return sandbox.Options{}, fmt.Errorf(
				"%s cannot be given a prompt for an interactive session: it has no way to be seeded on the command line, "+
					"and passing one would be read as a directory rather than a message.\n"+
					"  Untick console to run it headless — %s spells the prompt correctly there — or leave the prompt empty and type it in the session.",
				req.Agent, req.Agent)
		}
	}
	if req.Console && req.Agent == "" {
		// A plain command already reaches a console the same way — it is the argv
		// the caller chose. This field exists to swap an *agent* out of headless
		// mode, and there is nothing to swap without one.
		return sandbox.Options{}, errors.New("console needs an agent: a plain command is already whatever argv you gave it")
	}

	opts := sandbox.Options{
		Project: project,
		Detach:  true,
		Console: req.Console,
		RepoID:  repoID,
		Branch:  branch,
		Base:    req.Base,
		Verify:  req.Verify,
		Image:   req.Image,
		Memory:  req.Memory,
		CPUs:    req.CPUs,
		Allow:   req.Allow,
		// Normalised and validated by BuildSpec — a bare port becomes a loopback
		// bind there, and a malformed spec is refused before a container exists.
		Publish:     req.Publish,
		ExtraMounts: extraMounts,
	}
	for k, v := range req.Env {
		if config.IsReservedEnv(k) {
			return sandbox.Options{}, fmt.Errorf("env %q: %s", k, config.ReservedEnvReason())
		}
		opts.Env = append(opts.Env, k+"="+v)
	}

	if req.Agent != "" {
		// Routing, the half of it that happens before a container exists.
		//
		// The probe runs here and skips an agent whose provider is not answering,
		// which is the case this feature exists for, and the response says which
		// agent was chosen so a client is never guessing. The other half — the run
		// that fails ten minutes later — is not something a handler can watch, so
		// it belongs to the daemon: handleCreateRun registers the launch with the
		// supervisor, which outlives every request (supervisor.go).
		chosen, routedFrom, reason, err := s.routeAgent(ctx, req)
		if err != nil {
			return sandbox.Options{}, err
		}
		agent, ok := agents.Lookup(chosen)
		if !ok {
			return sandbox.Options{}, fmt.Errorf("unknown agent %q (known: %s)", chosen, strings.Join(agents.Names(), ", "))
		}
		opts.RoutedFrom, opts.RouteReason = routedFrom, reason
		// One id for the whole episode, minted for the agent that was *asked*
		// for rather than at the first switch — without it on both attempts, a
		// failover reads afterwards as two unrelated runs and "did routing help"
		// is unanswerable for exactly the runs it helped. Only when there is
		// somewhere to fall through to: an id on a run that could never route
		// describes an episode that cannot happen. The supervisor overwrites
		// both fields when it starts a later attempt of an episode already
		// under way.
		if len(req.Fallback) > 0 {
			opts.RouteID, opts.RouteAttempt = routing.NewID(), 1
		}
		opts.Agent = agent.Name
		opts.EnvAllow = agent.EnvAllow
		opts.Env = append(opts.Env, agent.Env...)
		opts.Prompt = req.Prompt
		// Headless or interactive, decided here because this is where the argv is
		// built and the two must agree: Autonomous runs the prompt to completion
		// and exits, while Command starts the agent's normal UI with the prompt
		// seeding its first turn — which is the mode that can stop and ask.
		if req.Console {
			opts.Command = agent.Console(req.Prompt, req.SkipPermissions)
			if req.Resume != "" {
				// Resume replaces the prompt: the conversation already has one.
				// The flag comes from the verified descriptor rather than being
				// written here, the same rule cli/recover_resume.go keeps.
				resumeArgs, ok := resumeArgsFor(agent.Name)
				if !ok {
					return sandbox.Options{}, fmt.Errorf("agent %q has no verified resume flag", agent.Name)
				}
				opts.Command = concatArgs(agent.Console("", req.SkipPermissions), resumeArgs, []string{req.Resume})
				// Recorded, so the conversation belonging to this run is known
				// rather than inferred: a resumed session began before its
				// container, which every correlation heuristic assumes cannot
				// happen.
				opts.SessionID = req.Resume
			}
		} else {
			opts.Command = fleet.WithVerify(agent.Autonomous(req.Prompt, nil), req.Verify)
		}

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

// concatArgs joins argv fragments into one fresh slice.
//
// Fresh on purpose: the fragments include a descriptor's own Command, and
// appending to that would alias the table every later run reads from.
func concatArgs(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// resumeArgsFor is the flag that makes an agent continue an existing session.
//
// Read from internal/agentctx's store table, which already records it per agent
// and keeps it honest against what has been verified. It lives here rather than
// on the descriptor because a descriptor says what runs *inside* the container;
// which flag reopens a transcript is a fact about host-side session storage,
// which is agentctx's job.
func resumeArgsFor(agent string) ([]string, bool) {
	store, ok := agentctx.Lookup(agent)
	if !ok || len(store.Resume) == 0 {
		return nil, false
	}
	return store.Resume, true
}

// routeAgent picks the agent this run will start with.
//
// The preflight half of routing: probe each candidate and take the first that
// answers, so a provider that is down when somebody presses Launch is skipped
// before a container exists. The response carries which agent it got, so the
// client never has to guess.
//
// It is asked again on every attempt of an episode, which is why it returns the
// agent rather than assuming the head of the chain: the supervisor's retry goes
// through the same builder, so a fallback whose own provider has since gone down
// is skipped the same way the first one was.
//
// Returns the chosen agent, the one it was asked for when they differ, and why.
func (s *Server) routeAgent(ctx context.Context, req RunCreateRequest) (chosen, routedFrom, reason string, err error) {
	// Unattended: a Studio run is detached, so an agent that stops to ask
	// permission hangs with nobody to answer — the same rule internal/agents
	// applies to a fleet.
	chain, err := routing.Resolve(req.Agent, req.Fallback, true)
	if err != nil {
		return "", "", "", err
	}
	if len(chain) == 1 {
		return chain[0], "", "", nil
	}

	var skipped []string
	for i, name := range chain {
		// Through runningProviders, not the field: POST /v1/routing/providers
		// assigns this map under providerMu, and a launch that ranged over it
		// unguarded is the data race that mutex was added for.
		avail := routing.Probe(ctx, name, runningProviders(s))
		if avail.Reachable {
			if i == 0 {
				return name, "", "", nil
			}
			return name, req.Agent, strings.Join(skipped, "; "), nil
		}
		skipped = append(skipped, fmt.Sprintf("%s: %s", name, avail.Reason))
	}
	// Every candidate was asked and none answered. Refusing beats launching into
	// an outage and letting the container discover it: the run would fail slowly,
	// having spent a container start, and the reason would be buried in its logs.
	return "", "", "", fmt.Errorf("no agent in the chain is available — %s", strings.Join(skipped, "; "))
}

// chainFor is the agent order this request asked for, whether or not every
// member of it can be reached.
//
// Resolve is the one place that validates a chain — unknown agents, duplicates,
// agents with no verified headless mode — so asking it again here beats
// rebuilding the list from the request and getting a different answer than the
// launch did. A chain it refuses leaves nothing to supervise, which is correct:
// the launch is about to be refused for the same reason.
func chainFor(req RunCreateRequest) []string {
	chain, err := routing.Resolve(req.Agent, req.Fallback, true)
	if err != nil {
		return nil
	}
	return chain
}
