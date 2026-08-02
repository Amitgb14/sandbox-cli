package studioapi

import (
	"net/http"
	"sort"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
)

// maxListedSessions bounds the picker. A long-lived agent HOME holds hundreds
// of conversations and the useful ones are the recent ones; the whole list is
// what `sandbox-cli context list` is for.
const maxListedSessions = 50

// handleAgentSessions is GET /v1/agents/{agent}/sessions — conversations that
// can be resumed.
//
// Only the sandbox-owned store is listed, for the same reason the console reads
// only that one: those are the sessions a container can actually reopen. The
// user's own ~/.claude history is a real store and is not this daemon's to
// offer, since resuming it here would mean mounting the host's history into a
// container that was not asked to have it.
func (s *Server) handleAgentSessions(w http.ResponseWriter, r *http.Request) {
	agent := r.PathValue("agent")
	f, ok := agentctx.Resolve(agent, agentctx.DefaultRoots(), time.Now())
	if !ok || f.State != agentctx.StateVerified {
		// Not an error: an agent nobody has logged into has no conversations,
		// which an empty list says without inventing a reason.
		writeJSON(w, http.StatusOK, SessionListResponse{Sessions: []SessionSummary{}})
		return
	}
	f = sandboxStore(f)
	if f.Dir == "" {
		writeJSON(w, http.StatusOK, SessionListResponse{Sessions: []SessionSummary{}})
		return
	}

	found, _, err := agentctx.List(f, agentctx.ListOpts{})
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Modified.After(found[j].Modified) })
	if len(found) > maxListedSessions {
		found = found[:maxListedSessions]
	}

	out := make([]SessionSummary, 0, len(found))
	for _, sess := range found {
		out = append(out, SessionSummary{
			ID:       sess.ID,
			Title:    sess.Title,
			Turns:    sess.Turns,
			Modified: sess.Modified,
			// Partial means sandbox-cli has no verified reader for this format:
			// the id and dates are real, the title and turn count are unknown and
			// are reported as unknown rather than as zero.
			Partial: sess.Partial,
		})
	}
	writeJSON(w, http.StatusOK, SessionListResponse{Sessions: out})
}
