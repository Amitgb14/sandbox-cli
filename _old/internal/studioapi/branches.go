package studioapi

import (
	"net/http"

	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// handleListBranches is GET /v1/branches: every local branch in one repository,
// and which one is checked out.
//
// It exists because "base branch" was a text box. The base is stamped as a label
// at launch and `fleet land` reads it back to decide what to merge into, so a
// typo there is not caught until landing — by which time the run has happened
// against the wrong recorded intent. A list of what actually exists turns that
// into a choice.
//
// Deliberately *not* folded into /v1/worktrees. That answers "which branches
// have a worktree", which is a different and smaller question — the base is
// usually `main`, which most often has no worktree of its own. A picker built
// from the worktree list would omit exactly the answer people want.
//
// No `repo=all`. A base branch belongs to one repository by construction: it is
// what a run in *that* repository will be landed into, and a union across
// repositories would offer names that mean nothing where they were chosen.
func (s *Server) handleListBranches(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scopeOf(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	names, current, err := worktree.Branches(sc.Project)
	if err != nil {
		// Not 502, for the reason handleListWorktrees gives: git is local
		// machinery, so a failure is this server's own directory being unreadable.
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, BranchList{Branches: names, Current: current})
}
