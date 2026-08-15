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

// handleListWorktrees is GET /worktrees?repo=.
//
// Without repo it answers for the repository this daemon was started in, which
// is what it answered before repositories were plural. With one it answers for
// that registered repository — never for a path, which is the rule projects.go
// exists to keep.
func (s *Server) handleListWorktrees(w http.ResponseWriter, r *http.Request) {
	// `repo=all` is the union across every registered repository, and it exists
	// because "All repositories" in a UI has to mean that.
	//
	// The absent parameter cannot serve both: it means "the repository this
	// daemon was started in", which is what every client written before
	// repositories were plural relies on. So the third meaning gets its own
	// spelling rather than a changed default — an id is never "all", so nothing
	// is made ambiguous. Runs did not need this because docker lists containers
	// across every repository already; worktrees are per-repository on disk, and
	// a dashboard showing all repositories' runs beside one repository's
	// worktrees is comparing two different questions.
	if r.URL.Query().Get("repo") == "all" {
		s.listWorktreesEverywhere(w, r)
		return
	}
	sc, err := s.scopeOf(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	infos, err := worktree.List(sc.Project)
	if err != nil {
		// Not 502: git is local machinery, not an upstream service. A failure here
		// means this server's own project directory could not be read, which is a
		// server-side fault however it happened.
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Containers read once for the whole listing rather than per row: it is one
	// call to the engine either way, and asking per branch would let two rows of
	// one response describe different moments.
	runs := s.runsByBranch(r.Context(), sc)
	out := make([]Worktree, 0, len(infos)+1)
	// The repository's own checkout first: it is a branch you can look at, it is
	// where a run without --worktree works, and leaving it out is why `main`
	// appeared in no picker.
	if primary, ok := s.primaryWorktree(sc, runs); ok {
		out = append(out, primary)
	}
	for _, info := range infos {
		out = append(out, s.toWorktree(sc, info, runs))
	}
	writeJSON(w, http.StatusOK, WorktreesResponse{Worktrees: out})
}

// listWorktreesEverywhere answers `?repo=all`: every registered repository's
// worktrees in one listing, each row carrying its own repo id.
//
// A repository that cannot be read is **skipped rather than fatal**. One
// unmounted volume must not empty a dashboard that is asking about four
// repositories — the rows that can be answered are still true, and the missing
// one is already reported as such by GET /projects, which is where a client
// learns about it rather than from a listing that failed entirely.
func (s *Server) listWorktreesEverywhere(w http.ResponseWriter, r *http.Request) {
	out := []Worktree{}
	for _, p := range s.projects() {
		if p.Missing {
			continue
		}
		infos, err := worktree.List(p.Root)
		if err != nil {
			continue
		}
		sc := repoScope{Project: p.Root, RepoID: p.ID}
		runs := s.runsByBranch(r.Context(), sc)
		if primary, ok := s.primaryWorktree(sc, runs); ok {
			out = append(out, primary)
		}
		for _, info := range infos {
			out = append(out, s.toWorktree(sc, info, runs))
		}
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
	sc, err := s.scopeFor(req.Repo)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	info, err := worktree.Resolve(sc.Project, req.Branch)
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
	writeJSON(w, status, s.toWorktree(sc, info, s.runsByBranch(r.Context(), sc)))
}

// handleGetWorktree is GET /worktrees/{branch}?repo=.
func (s *Server) handleGetWorktree(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scopeOf(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	branch := r.PathValue("branch")
	runs := s.runsByBranch(r.Context(), sc)
	// The checkout is addressable by its own branch name, or the listing would
	// offer a row whose detail page 404s.
	if primary, ok := s.primaryWorktree(sc, runs); ok && primary.Branch == branch {
		writeJSON(w, http.StatusOK, primary)
		return
	}
	path, exists, err := worktree.Path(sc.Project, branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, fmt.Errorf("no worktree for branch %q", branch))
		return
	}
	writeJSON(w, http.StatusOK, s.toWorktree(sc, worktree.Info{Branch: branch, Path: path}, runs))
}

// handleDeleteWorktree is DELETE /worktrees/{branch}?repo=&force=1. Without
// force, git refuses to remove a worktree holding modified or untracked files —
// the safe default, since those edits exist nowhere else.
func (s *Server) handleDeleteWorktree(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scopeOf(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	branch := r.PathValue("branch")
	// Never the repository's own checkout. `worktree.Remove` would refuse it
	// anyway, but a listing that offers a row has to explain why one of them
	// cannot be removed rather than passing git's message through.
	if primary, ok := s.primaryWorktree(sc, nil); ok && primary.Branch == branch {
		writeError(w, http.StatusConflict, fmt.Errorf(
			"%q is this repository's own checkout, not a managed worktree — there is nothing here to remove", branch))
		return
	}
	force := r.URL.Query().Has("force")
	if err := worktree.Remove(sc.Project, branch, force); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runsByBranch indexes one repository's containers by the branch they worked,
// newest first, so a listing can answer "what ran here" without one engine call
// per row. An engine that cannot be reached yields an empty index rather than an
// error: the worktrees are real whether or not docker is up, and a status
// listing that fails entirely because one of its columns is unavailable is worse
// than one with that column empty.
//
// The repo id comes from the scope rather than from the server, which is the
// whole of what makes a second repository's listing say anything: filtering on
// s.RepoID while walking another repository's worktrees would show every branch
// with no runs against it.
func (s *Server) runsByBranch(ctx context.Context, sc repoScope) map[string][]runtime.ContainerInfo {
	out := map[string][]runtime.ContainerInfo{}
	infos, err := s.RT.Containers(ctx, map[string]string{
		sandbox.LabelCLI:  "1",
		sandbox.LabelRepo: sc.RepoID,
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

func (s *Server) toWorktree(sc repoScope, info worktree.Info, runs map[string][]runtime.ContainerInfo) Worktree {
	dirty := worktree.Dirty(sc.Project, info.Branch, 0)
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
		Head:       worktree.Head(sc.Project, info.Branch),
		RepoID:     sc.RepoID,
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
		base = worktree.HeadBranch(sc.Project)
	}
	if base != "" {
		b := base
		wt.Base = &b
		wt.Ahead = worktree.Ahead(sc.Project, info.Branch, base)
		wt.Behind = worktree.Behind(sc.Project, info.Branch, base)
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
	sc, err := s.scopeOf(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	branch := r.PathValue("branch")
	path, exists, err := worktree.Path(sc.Project, branch)
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
	for _, c := range s.runsByBranch(r.Context(), sc)[branch] {
		if b := c.Labels[sandbox.LabelBase]; b != "" {
			base = b
			break
		}
	}
	if base == "" {
		base = worktree.HeadBranch(sc.Project)
	}

	out := make([]Commit, 0, 32)
	for _, c := range worktree.Commits(sc.Project, branch, base, commitsLimit) {
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

// primaryWorktree describes the repository's own checkout as a Worktree row.
//
// Built by hand rather than through toWorktree, because two of that function's
// helpers resolve a *managed* worktree by branch and find nothing for this one:
// worktree.Dirty asks Path() where the branch lives, so the checkout's
// uncommitted files would read as zero — the one number somebody looks at this
// row for. WorkingStatIn asks the directory instead, which is the right question
// here since the directory is known.
//
// No base, and that is deliberate rather than missing: the checkout is what
// other branches are measured against, so "0 ahead, 0 behind" against itself is
// noise, and `land` merges into this branch rather than landing it.
//
// A detached HEAD yields nothing at all. There is no branch to name it by, and
// inventing one would put a row in a picker that no request could address.
func (s *Server) primaryWorktree(sc repoScope, runs map[string][]runtime.ContainerInfo) (Worktree, bool) {
	branch := worktree.HeadBranch(sc.Project)
	if branch == "" {
		return Worktree{}, false
	}
	wt := Worktree{
		Branch:  branch,
		Path:    sc.Project,
		RepoID:  sc.RepoID,
		Primary: true,
		Head:    worktree.Head(sc.Project, branch),
		Dirty:   []string{},
	}
	for _, st := range worktree.WorkingStatIn(sc.Project) {
		wt.Dirty = append(wt.Dirty, st.Path)
	}
	wt.DirtyCount = len(wt.Dirty)
	if fi, err := os.Stat(sc.Project); err == nil {
		wt.CreatedAt = fi.ModTime().UTC().Format(time.RFC3339)
	}
	for _, c := range runs[branch] {
		if c.Running() {
			id := shortID(c.ID)
			wt.RunID = &id
			break
		}
	}
	wt.Verified = verifiedByLastRun(runs[branch])
	return wt, true
}
