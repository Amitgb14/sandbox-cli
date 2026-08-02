package studioapi

import (
	"net/http"
	"sort"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// handleAgents lists the agents this API can launch. Only names with a verified
// headless mode are ever registered in internal/agents (see its package doc),
// which is exactly the constraint POST /runs needs: every run this API starts is
// detached, and an agent that stops to ask permission would hang forever with no
// terminal attached to answer it.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	names := agents.Names()
	out := make([]AgentInfo, 0, len(names))
	for _, n := range names {
		d, ok := agents.Lookup(n)
		if !ok {
			continue
		}
		envAllow := append([]string(nil), d.EnvAllow...)
		sort.Strings(envAllow)
		out = append(out, AgentInfo{
			Name:       d.Name,
			PersistDir: d.PersistDir,
			EnvAllow:   envAllow,
		})
	}
	writeJSON(w, http.StatusOK, AgentsResponse{Agents: out})
}
