package studioapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// handleListWorktrees is GET /worktrees.
func (s *Server) handleListWorktrees(w http.ResponseWriter, r *http.Request) {
	infos, err := worktree.List(s.Project)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	// Containers read once for the whole listing rather than per row: it is one
	// call to the engine either way, and asking per branch would let two rows of
	// one response describe different moments.
	runs := s.runsByBranch(r.Context())
	out := make([]Worktree, 0, len(infos))
	for _, info := range infos {
		out = append(out, s.toWorktree(info, runs))
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
		writeError(w, http.StatusBadGateway, err)
		return
	}
	status := http.StatusOK
	if info.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, s.toWorktree(info, s.runsByBranch(r.Context())))
}

// handleGetWorktree is GET /worktrees/{branch}.
func (s *Server) handleGetWorktree(w http.ResponseWriter, r *http.Request) {
	branch := r.PathValue("branch")
	path, exists, err := worktree.Path(s.Project, branch)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, fmt.Errorf("no worktree for branch %q", branch))
		return
	}
	writeJSON(w, http.StatusOK, s.toWorktree(worktree.Info{Branch: branch, Path: path}, s.runsByBranch(r.Context())))
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

// runsByBranch indexes this repository's containers by the branch they worked,
// newest first, so a listing can answer "what ran here" without one engine call
// per row. An engine that cannot be reached yields an empty index rather than an
// error: the worktrees are real whether or not docker is up, and a status
// listing that fails entirely because one of its columns is unavailable is worse
// than one with that column empty.
func (s *Server) runsByBranch(ctx context.Context) map[string][]runtime.ContainerInfo {
	out := map[string][]runtime.ContainerInfo{}
	infos, err := s.RT.Containers(ctx, map[string]string{
		sandbox.LabelCLI:  "1",
		sandbox.LabelRepo: s.RepoID,
	})
	if err != nil {
		return out
	}
	for _, c := range infos {
		if b := c.Labels[sandbox.LabelBranch]; b != "" {
			out[b] = append(out[b], c)
		}
	}
	return out
}

func (s *Server) toWorktree(info worktree.Info, runs map[string][]runtime.ContainerInfo) Worktree {
	dirty := worktree.Dirty(s.Project, info.Branch, 0)
	if dirty == nil {
		// A nil slice marshals to `null`, not `[]`, so a clean worktree would
		// still hand the client something it has to guard before iterating.
		dirty = []string{}
	}

	wt := Worktree{
		Branch:     info.Branch,
		Path:       info.Path,
		Dirty:      dirty,
		DirtyCount: len(dirty),
		Head:       worktree.Head(s.Project, info.Branch),
		RepoID:     s.RepoID,
	}

	if fi, err := os.Stat(info.Path); err == nil {
		wt.CreatedAt = fi.ModTime().UTC().Format(time.RFC3339)
	}

	// The recorded base, not the checked-out one. `land` treats the label as the
	// intent and a disagreement with HEAD as a refusal rather than a preference,
	// so a listing that quietly counted against whatever happens to be checked
	// out would show a different number than the merge will use.
	base := ""
	for _, c := range runs[info.Branch] {
		if b := c.Labels[sandbox.LabelBase]; b != "" {
			base = b
			break
		}
	}
	if base == "" {
		base = worktree.HeadBranch(s.Project)
	}
	if base != "" {
		b := base
		wt.Base = &b
		wt.Ahead = worktree.Ahead(s.Project, info.Branch, base)
		wt.Behind = worktree.Behind(s.Project, info.Branch, base)
	}

	for _, c := range runs[info.Branch] {
		if c.Running() {
			id := shortID(c.ID)
			wt.RunID = &id
			break
		}
	}
	wt.Verified = verifiedByLastRun(runs[info.Branch])
	return wt
}

// verifiedByLastRun reports what the branch's most recent run said about its own
// definition of done.
//
// Nil is a third answer and not a synonym for false: no container left to ask,
// or a run that declared no verify at all, means *nothing checked this* — which
// is what `land` reports as unverified and refuses on. A client that rendered
// nil and false the same way would erase the one distinction that decides
// whether the work may be merged.
//
// Classified exactly as fleet.verifyState and land.checkVerified do, because a
// third reading of the same two facts is a third chance for them to disagree.
func verifiedByLastRun(cs []runtime.ContainerInfo) *bool {
	for _, c := range cs {
		if c.Labels[sandbox.LabelVerify] == "" {
			continue // declared no verify: it cannot answer this
		}
		if c.Running() {
			return nil // still deciding
		}
		passed := c.ExitCode == 0
		return &passed
	}
	return nil
}

// handleWorktreeCommits is GET /v1/worktrees/{branch}/commits: what this branch
// has that its base does not.
//
// The base comes from the label a run stamped, falling back to the checked-out
// branch — the same resolution the listing uses, so a screen showing "6 ahead"
// and a screen showing six commits are counting the same thing. A view that
// derived the base differently would eventually disagree with `land`, which is
// the one that matters.
func (s *Server) handleWorktreeCommits(w http.ResponseWriter, r *http.Request) {
	branch := r.PathValue("branch")
	path, exists, err := worktree.Path(s.Project, branch)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, fmt.Errorf("no worktree for branch %q", branch))
		return
	}
	_ = path

	base := ""
	for _, c := range s.runsByBranch(r.Context())[branch] {
		if b := c.Labels[sandbox.LabelBase]; b != "" {
			base = b
			break
		}
	}
	if base == "" {
		base = worktree.HeadBranch(s.Project)
	}

	out := make([]Commit, 0, 32)
	for _, c := range worktree.Commits(s.Project, branch, base, commitsLimit) {
		out = append(out, Commit{
			SHA: c.SHA, ShortSHA: c.ShortSHA, Subject: c.Subject,
			Author: c.Author, Date: c.Date,
			Files: c.Files, Insertions: c.Insertions, Deletions: c.Deletions,
		})
	}
	writeJSON(w, http.StatusOK, CommitsResponse{Base: base, Commits: out})
}

// commitsLimit bounds the listing. A branch an agent has been working for a week
// is a legitimate branch, and a screen is not a log viewer.
const commitsLimit = 100
