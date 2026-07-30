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
		env := append([]string(nil), d.Env...)
		if env == nil {
			env = []string{}
		}
		// The prompt is elided rather than filled with a sample: this is shown as
		// "what would run", and a placeholder in argv position reads as a value
		// someone chose.
		invocation := d.Invocation("<prompt>", nil)
		out = append(out, AgentInfo{
			Name:                 d.Name,
			Label:                d.Name,
			PersistDir:           d.PersistDir,
			EnvAllow:             envAllow,
			Env:                  env,
			HeadlessVerified:     true,
			AutonomousInvocation: invocation,
		})
	}
	writeJSON(w, http.StatusOK, AgentsResponse{Agents: out})
}
