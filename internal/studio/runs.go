package studio

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// Run is a session record — a sandbox the CLI or Studio cloned a repository
// into — joined with what its sandboxd says about it now.
type Run struct {
	Sandbox      string            `json:"sandbox"`
	Repo         string            `json:"repo"`
	RepoID       string            `json:"repo_id"`
	Agent        string            `json:"agent,omitempty"`
	Started      time.Time         `json:"started"`
	State        string            `json:"state"` // the sandbox's, or "gone"
	Labels       map[string]string `json:"labels,omitempty"`
	Done         bool              `json:"done"`
	BroughtBack  string            `json:"brought_back,omitempty"`
	Checkpoint   string            `json:"checkpoint,omitempty"`
	CheckpointAt time.Time         `json:"checkpoint_at,omitempty"`
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	sessions, err := workspace.Sessions()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	live := map[string]api.Sandbox{}
	if list, err := s.Client.Sandboxes(ctx); err == nil {
		for _, sb := range list {
			live[sb.ID] = sb
		}
	}
	out := []Run{}
	for _, se := range sessions {
		// Only this context's: another context's sandboxd is not the one asked.
		if se.Context != "" && se.Context != s.Context {
			continue
		}
		run := Run{Sandbox: se.Sandbox, Repo: se.Repo, RepoID: repoID(se.Repo), Agent: se.Agent, Started: se.Started,
			Done: se.Done, BroughtBack: se.BroughtBack, Checkpoint: se.Checkpoint, CheckpointAt: se.CheckpointAt, State: "gone"}
		if sb, ok := live[se.Sandbox]; ok && sb.State != api.StateTerminated {
			run.State, run.Labels = sb.State, sb.Labels
		}
		out = append(out, run)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

func (s *Server) launch(w http.ResponseWriter, r *http.Request) {
	var req LaunchRequest
	if !decode(w, r, &req) {
		return
	}
	reposMu.Lock()
	repos, err := s.loadRepos()
	reposMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// No repository is a sandbox with an empty /workspace: nothing is cloned
	// in, and nothing comes back to bring home.
	var path string
	for _, rp := range repos {
		if rp.ID == req.Repo && !rp.Missing {
			path = rp.Path
		}
	}
	switch {
	case req.Repo != "" && path == "":
		writeErr(w, http.StatusNotFound, "no repository with that id; add it first")
		return
	case (req.Agent == "") == (len(req.Command) == 0):
		writeErr(w, http.StatusBadRequest, "name an agent or a command, not both")
		return
	case req.Console && req.Agent == "":
		writeErr(w, http.StatusBadRequest, "a console run is an agent's")
		return
	case req.Agent != "" && !req.Console && req.Prompt == "":
		writeErr(w, http.StatusBadRequest, "a headless agent run needs a prompt")
		return
	}
	// Studio offers only the agents with a verified headless mode, console
	// runs included. Headless needs it outright: nobody will answer a
	// question, and an agent that stops to ask does not fail, it hangs. A
	// console run does not, but the interactive-only wrappers are unverified
	// beyond starting, and they stay a CLI choice (sandbox-cli agent <name>)
	// rather than something Studio puts on a menu.
	if req.Agent != "" {
		if _, ok := agents.Lookup(req.Agent); !ok {
			msg := "unknown agent " + req.Agent
			if _, ok := agents.LookupInteractive(req.Agent); ok {
				msg = req.Agent + " has no verified headless mode, so Studio does not run it; use sandbox-cli agent " + req.Agent
			}
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
	}
	if s.Launch == nil {
		writeErr(w, http.StatusNotImplemented, "this Studio cannot launch runs")
		return
	}
	// Not the request's context: a run outlives the request that started it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := s.Launch(ctx, path, req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// bringBack fetches a run's commits into refs/sandbox/<sandbox>, exactly as
// `sandbox-cli bring-back` does. Studio's runs are detached, so this is how
// their work comes home.
func (s *Server) bringBack(w http.ResponseWriter, r *http.Request) {
	se, err := workspace.LoadSession(r.PathValue("sandbox"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no record of that run")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	ref, err := workspace.BringBack(ctx, s.Client, se, se.Sandbox)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	se.MarkBroughtBack(ref)
	out := map[string]string{"ref": ref}
	if ref != "" && s.Mirror != nil {
		// As a CLI bring-back does: copied off the machine when the user has a
		// mirror, and a failure said rather than failing what already worked.
		if msg, err := s.Mirror(ctx, &se, ref); err != nil {
			out["mirror_error"] = err.Error()
		} else if msg != "" {
			out["mirrored"] = msg
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) forgetRun(w http.ResponseWriter, r *http.Request) {
	if _, err := workspace.LoadSession(r.PathValue("sandbox")); err != nil {
		writeErr(w, http.StatusNotFound, "no record of that run")
		return
	}
	if err := workspace.Forget(r.PathValue("sandbox")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Agent is one agent Studio can run. Only agents with a verified headless
// mode are listed (see launch); `sandbox-cli agent ls` shows the rest.
type Agent struct {
	Name  string `json:"name"`
	Login string `json:"login"` // "saved", "-", or "not kept"
}

func (s *Server) agents(w http.ResponseWriter, _ *http.Request) {
	out := []Agent{}
	for _, name := range agents.Names() {
		d, _ := agents.Lookup(name)
		login := "-"
		if len(d.AuthPaths) == 0 {
			login = "not kept"
		} else {
			for _, rel := range d.AuthPaths {
				if _, err := os.Stat(filepath.Join(workspace.LoginDir(d), filepath.FromSlash(rel))); err == nil {
					login = "saved"
				}
			}
		}
		out = append(out, Agent{Name: name, Login: login})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}
