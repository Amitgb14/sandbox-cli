package studio

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
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
	case req.CPUs < 0 || req.MemoryMB < 0 || req.DiskMB < 0:
		writeErr(w, http.StatusBadRequest, "cpus, memory_mb and disk_mb: zero for the server's default, or more")
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
	if err := s.applyEgressRules(r, &req); err != nil {
		writeErr(w, http.StatusInternalServerError, "egress rules: "+err.Error())
		return
	}
	// Not the request's context: a run outlives the request that started it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := s.Launch(ctx, req)
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
	// Set is whether the environment Studio runs in sets it, and Saved
	// whether a key is saved for it (agenthome.SaveKey). A run forwards the
	// environment's value, else the saved one.
	Set   bool `json:"set"`
	Saved bool `json:"saved"`
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
				if _, err := os.Stat(filepath.Join(agenthome.LoginDir(d), filepath.FromSlash(rel))); err == nil {
					login = "saved"
				}
			}
		}
		a := Agent{Name: name, Login: login, ProviderHost: d.ProviderHost, LoginFiles: d.AuthPaths}
		saved := agenthome.SavedKeys(d)
		for _, e := range d.EnvAllow {
			_, set := os.LookupEnv(e)
			_, kept := saved[e]
			a.Env = append(a.Env, AgentEnv{Name: e, Set: set, Saved: kept})
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

// applyEgressRules adds the enabled egress rules to a launch, after what the
// request named itself.
//
// A deny rule applies to every run that has a network. An allow rule applies
// to a run that will be an allowlist — one that asks for it, or asks for
// nothing on a server whose default is one — and to no other: a run on open
// egress reaches the host already, and turning it into an allowlist because a
// rule named one host would cut it off from every other.
func (s *Server) applyEgressRules(r *http.Request, req *LaunchRequest) error {
	allow, deny, err := egressFor()
	if err != nil || (len(allow) == 0 && len(deny) == 0) {
		return err
	}
	req.Deny = append(req.Deny, deny...)
	switch req.Network {
	case api.NetworkAllowlist:
		req.Allow = append(req.Allow, allow...)
	case "":
		if len(req.Allow) > 0 {
			// An allowlist already, by its own --allow.
			req.Allow = append(req.Allow, allow...)
			return nil
		}
		if len(allow) == 0 {
			return nil
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		caps, err := s.clientFor(r).Capabilities(ctx)
		if err != nil {
			return err
		}
		if caps.Network.Default.Mode == api.NetworkAllowlist {
			req.Allow = append(req.Allow, allow...)
		}
	}
	return nil
}
