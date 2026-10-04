package studio

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentstate"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// AgentState is one agent sandbox and what its agent is doing, decided by the
// same code `sandbox-cli agent state` uses.
type AgentState struct {
	Sandbox string           `json:"sandbox"`
	Name    string           `json:"name,omitempty"`
	Agent   string           `json:"agent"`
	State   agentstate.State `json:"state"`
	Why     string           `json:"why"`
}

// agentStates answers "which of my agents is waiting for me" for every live
// agent sandbox on the context. Host-side, because deciding reads each agent's
// conversation out of its sandbox, and that reader is the CLI's.
func (s *Server) agentStates(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	c := s.clientFor(r)
	list, err := c.Sandboxes(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	now := time.Now()
	out := []AgentState{}
	for _, sb := range list {
		if sb.Labels[agentstate.AgentLabel] == "" || sb.State == api.StateTerminated {
			continue
		}
		rep := agentstate.Look(ctx, c, sb, now)
		out = append(out, AgentState{Sandbox: rep.Sandbox, Name: rep.Name, Agent: rep.Agent, State: rep.State, Why: agentstate.Describe(rep.State)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Sandbox < out[j].Sandbox })
	writeJSON(w, http.StatusOK, out)
}
