package studioapi

import (
	"fmt"
	"net/http"
	"os"
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
	mode := req.Mode
	if mode == "" {
		mode = RestoreModeBranch
	}

	// Snapshots are recorded per repository (~/.config/sandbox/rescue/<repo-id>/),
	// so the search has to happen in the repository this run belonged to — read
	// off its own label, not assumed to be the daemon's default.
	sess, err := s.findRescueSession(s.scopeOfRun(c.Labels[sandbox.LabelRepo]), branch, c.Labels[sandbox.LabelAgent])
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	opts := rescue.RestoreOptions{Branch: req.Branch}
	var patchFile string
	switch mode {
	case RestoreModeBranch:
		opts.Mode = rescue.RestoreBranch
	case RestoreModeWorktree:
		opts.Mode = rescue.RestoreWorktree
	case RestoreModePatch:
		opts.Mode = rescue.RestorePatch
		// Restore writes the patch to the process's own stdout when Out is ""/"-",
		// which is the server's terminal, not the HTTP response. A temp file lets
		// this API return the text instead of printing it somewhere the caller
		// cannot see.
		f, terr := os.CreateTemp("", "sandbox-studio-recover-*.patch")
		if terr != nil {
			writeError(w, http.StatusInternalServerError, terr)
			return
		}
		patchFile = f.Name()
		f.Close()
		opts.Out = patchFile
		defer os.Remove(patchFile)
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("mode must be %q, %q, or %q", RestoreModeBranch, RestoreModePatch, RestoreModeWorktree))
		return
	}

	result, err := rescue.Restore(sess.Workspace, sess.ID, opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}

	resp := RunRecoverResponse{
		SessionID:          sess.ID,
		Mode:               mode,
		Branch:             result.Branch,
		Files:              result.Files,
		MatchesWorkingTree: result.MatchesWorkingTree,
	}
	if patchFile != "" {
		if b, err := os.ReadFile(patchFile); err == nil {
			resp.Patch = string(b)
		}
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
		matches = append(matches, sess)
	}
	if len(matches) == 0 {
		return rescue.Session{}, fmt.Errorf(
			"no crash-recovery snapshot found for branch %q; `sandbox-cli recover list` shows every session", branch)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Activity().After(matches[j].Activity()) })
	return matches[0], nil
}
