package studioapi

import (
	"fmt"
	"net/http"

	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// handleListWorktrees is GET /worktrees.
func (s *Server) handleListWorktrees(w http.ResponseWriter, r *http.Request) {
	infos, err := worktree.List(s.Project)
	if err != nil {
		// Not 502: git is local machinery, not an upstream service. A failure here
		// means this server's own project directory could not be read, which is a
		// server-side fault however it happened.
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]Worktree, 0, len(infos))
	for _, info := range infos {
		out = append(out, s.toWorktree(info))
	}
	writeJSON(w, http.StatusOK, WorktreesResponse{Worktrees: out})
}

// handleCreateWorktree is POST /worktrees. It resolves (creating if needed) a
// git worktree for the given branch under sandbox-cli's managed worktree
// directory, same as `sandbox-cli worktree add` / `--worktree`.
func (s *Server) handleCreateWorktree(w http.ResponseWriter, r *http.Request) {
	var req WorktreeCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Branch == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("branch is required"))
		return
	}
	info, err := worktree.Resolve(s.Project, req.Branch)
	if err != nil {
		// 422 rather than 500: by far the likeliest reason git declines is the
		// branch this request named — unknown, already checked out elsewhere, or
		// not a valid ref — and git's own message says which.
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	status := http.StatusOK
	if info.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, s.toWorktree(info))
}

// handleGetWorktree is GET /worktrees/{branch}.
func (s *Server) handleGetWorktree(w http.ResponseWriter, r *http.Request) {
	branch := r.PathValue("branch")
	path, exists, err := worktree.Path(s.Project, branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, fmt.Errorf("no worktree for branch %q", branch))
		return
	}
	writeJSON(w, http.StatusOK, s.toWorktree(worktree.Info{Branch: branch, Path: path}))
}

// handleDeleteWorktree is DELETE /worktrees/{branch}?force=1. Without force,
// git refuses to remove a worktree holding modified or untracked files — the
// safe default, since those edits exist nowhere else.
func (s *Server) handleDeleteWorktree(w http.ResponseWriter, r *http.Request) {
	branch := r.PathValue("branch")
	force := r.URL.Query().Has("force")
	if err := worktree.Remove(s.Project, branch, force); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) toWorktree(info worktree.Info) Worktree {
	dirty := worktree.Dirty(s.Project, info.Branch, 0)
	return Worktree{
		Branch:     info.Branch,
		Path:       info.Path,
		Dirty:      dirty,
		DirtyCount: len(dirty),
	}
}
