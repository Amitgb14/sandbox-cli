package studio

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

func (s *Server) launch(w http.ResponseWriter, r *http.Request) {
	var req LaunchRequest
	if !decode(w, r, &req) {
		return
	}
	req.Org = orgOf(r)
	switch {
	case (req.Agent == "") == (len(req.Command) == 0):
		writeErr(w, http.StatusBadRequest, "name an agent or a command, not both")
		return
	case req.Console && req.Agent == "":
		writeErr(w, http.StatusBadRequest, "a console run is an agent's")
		return
	case req.Agent != "" && !req.Console && req.Prompt == "":
		writeErr(w, http.StatusBadRequest, "a headless agent run needs a prompt")
		return
	case req.Image != "" && req.Snapshot != "":
		// A snapshot carries its own disk; an image beside it would be ignored
		// by one reading and obeyed by another.
		writeErr(w, http.StatusBadRequest, "start from an image or a snapshot, not both")
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
	// Hosted, an unattended agent is a job: the gateway runs it with the
	// tenant's own secrets, where its API key belongs. Studio has no key of
	// the user's to give it, and must not give it the host's.
	if s.hosted != nil && req.Agent != "" && !req.Console {
		writeErr(w, http.StatusBadRequest, "run an agent unattended as a job (Jobs), with its API key stored as a secret")
		return
	}
	if s.Launch == nil {
		writeErr(w, http.StatusNotImplemented, "this Studio cannot launch runs")
		return
	}
	// Not the request's cancellation: a run outlives the request that started
	// it. Its values stay, so a hosted session the gateway stops accepting
	// mid-launch is still ended.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	res, err := s.Launch(ctx, s.clientFor(r), req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// Agent is one agent Studio can run. Only agents with a verified headless
// mode are listed (see launch); `sandbox-cli agent ls` shows the rest.
type Agent struct {
	Name  string `json:"name"`
	Login string `json:"login"` // "saved", "-", or "not kept"
	// ProviderHost is the agent's API, which an agent run may always reach.
	ProviderHost string `json:"provider_host,omitempty"`
	// LoginFiles are what is kept of its login between runs, relative to the
	// sandbox user's home: copied out when a run ends, back in when one starts.
	LoginFiles []string `json:"login_files,omitempty"`
	// Env is each variable the agent takes from the environment, and whether
	// it is set in the one Studio runs in: an agent run forwards those that
	// are. Names only. A value never leaves this process, here as everywhere.
	Env []AgentEnv `json:"env,omitempty"`
}

// AgentEnv is one variable an agent reads, by name.
type AgentEnv struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

func (s *Server) agents(w http.ResponseWriter, _ *http.Request) {
	out := []Agent{}
	for _, name := range agents.Names() {
		d, _ := agents.Lookup(name)
		if s.hosted != nil {
			// Hosted: the machine Studio runs on is nobody's here. Whether it
			// holds a login or an API key in its environment is not the
			// user's business, and neither would be used for them.
			out = append(out, Agent{Name: name, Login: "in sandbox", ProviderHost: d.ProviderHost, LoginFiles: d.AuthPaths})
			continue
		}
		login := "-"
		if len(d.AuthPaths) == 0 {
			login = "not kept"
		} else {
			for _, rel := range d.AuthPaths {
				if _, err := os.Stat(filepath.Join(agenthome.LoginDir(d), filepath.FromSlash(rel))); err == nil {
					login = "saved"
				}
			}
		}
		a := Agent{Name: name, Login: login, ProviderHost: d.ProviderHost, LoginFiles: d.AuthPaths}
		for _, e := range d.EnvAllow {
			_, set := os.LookupEnv(e)
			a.Env = append(a.Env, AgentEnv{Name: e, Set: set})
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}
