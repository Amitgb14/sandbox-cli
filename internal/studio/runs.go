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
		out = append(out, Agent{Name: name, Login: login})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}
