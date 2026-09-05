package studioapi

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/Amitgb14/sandbox-cli/internal/rescue"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// handleRecoverRun is POST /runs/{id}/recover. It restores the crash-recovery
// snapshot (internal/rescue) most recently associated with this run's branch —
// the same correlation `sandbox-cli recover` performs by agent, project and
// time window, narrowed here to "the branch this run worked on" rather than
// left to the caller to pick a session id by hand.
//
// There may be nothing to restore: a run that finished cleanly, or one that
// never wrote a snapshot, has none. That is reported as 404, not an empty
// success, so a client does not read "recovered nothing" as "recovered
// everything".
func (s *Server) handleRecoverRun(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	branch := c.Labels[sandbox.LabelBranch]
	if branch == "" {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Errorf("run %s has no recorded branch, so its snapshots cannot be found", shortID(c.ID)))
		return
	}

	var req RunRecoverRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Snapshots are recorded per repository (~/.config/sandbox/rescue/<repo-id>/),
	// so the search has to happen in the repository this run belonged to — read
	// off its own label, not assumed to be the daemon's default.
	sess, err := s.findRescueSession(s.scopeOfRun(c.Labels[sandbox.LabelRepo]), branch, c.Labels[sandbox.LabelAgent])
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	resp, status, err := s.restoreSession(sess, req.Mode, req.Branch)
	if err != nil {
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// findRescueSession picks the most recently active rescue session recorded for
// branch (and, when the run carried one, the same agent), the same "what was I
// working on" question `sandbox-cli recover` answers by hand.
func (s *Server) findRescueSession(sc repoScope, branch, agent string) (rescue.Session, error) {
	repoRoot, err := rescue.MainRepoRoot(sc.Project)
	if err != nil {
		return rescue.Session{}, err
	}
	sessions, err := rescue.Sessions(repoRoot)
	if err != nil {
		return rescue.Session{}, err
	}
	var matches []rescue.Session
	for _, sess := range sessions {
		if sess.Branch != branch {
			continue
		}
		if agent != "" && sess.Agent != "" && sess.Agent != agent {
			continue
		}
		// Never a baseline. baselineFor records one before every launch, holding
		// the workspace as it was *before* the agent touched it — and since it is
		// stamped with the same branch and agent and is the most recent thing
		// here, it used to win this selection outright. Restoring it is worse
		// than finding nothing: not a 404, but a restore that looks like it
		// worked and hands back the work's starting point.
		if sess.Outcome == baselineOutcome {
			continue
		}
		matches = append(matches, sess)
	}
	if len(matches) == 0 {
		return rescue.Session{}, fmt.Errorf(
			"no crash-recovery snapshot found for branch %q; `sandbox-cli recover list` shows every session", branch)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Activity().After(matches[j].Activity()) })
	return matches[0], nil
}
