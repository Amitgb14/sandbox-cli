package studioapi

import (
	"fmt"
	"net/http"

	"github.com/Amitgb14/sandbox-cli/internal/rescue"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// handleRunDiff is GET /v1/runs/{id}/diff: what the agent changed.
//
// Read from git in the run's own workspace, not from anything the run reported
// about itself. That is the same reason `land` reads the worktree rather than
// trusting a summary: an agent's account of its work is the agent's account, and
// the diff is the evidence.
//
// Uncommitted work is included, because that is where an agent's output usually
// still is when you come to look at it — a run that has finished has often
// written files and not committed them, and a diff that showed only commits
// would report "nothing changed" for exactly the runs worth reviewing.
//
// The honest limit, worth knowing before trusting the attribution: this is the
// state of the run's workspace, not a record of what that run did to it. For a
// --worktree or fleet run they are the same thing, because the checkout belongs
// to that run alone. For a run started in the repository you were standing in,
// the workspace is shared with you — so anything you had left uncommitted
// appears here too, credited to an agent that never touched it.
//
// Closing that gap needs a before-image to compare against, which internal/rescue
// already takes for the runs it snapshots; until this reads one, the endpoint
// answers "what is uncommitted here" and a client should say so rather than
// "what this agent changed".
//
// A run with no branch (a plain `run` outside a repository) has nothing to diff
// and answers an empty list rather than an error: there is no failure here, just
// no changes to describe.
func (s *Server) handleRunDiff(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	// The run's own workspace, which is the directory it could actually change —
	// a managed worktree for a --worktree run, and the repository itself for one
	// started where you were standing.
	run := toRun(c, s.Engine)

	branch := c.Labels[sandbox.LabelBranch]
	base := c.Labels[sandbox.LabelBase]
	files := []DiffFile{}
	if run.Workspace == "" {
		// No workspace mount means nothing on disk this run could have changed.
		writeJSON(w, http.StatusOK, files)
		return
	}
	// The repository *this run* belongs to, read off its own sandbox.repo label
	// rather than assumed to be the daemon's default. A run launched against a
	// second registered repository would otherwise have its base branch — and,
	// below, its fallback diff — computed in a checkout it never touched.
	sc := s.scopeOfRun(c.Labels[sandbox.LabelRepo])
	if base == "" {
		base = worktree.HeadBranch(sc.Project)
	}

	// The before-image, when the run recorded one. With it the question is "what
	// did this run change"; without it, only "what is uncommitted here" — and the
	// two differ in a checkout the user also works in, where their own unfinished
	// edits would otherwise be credited to an agent that never touched them.
	baseline := c.Labels[sandbox.LabelBaseline]
	byPath := map[string]*DiffFile{}
	add := func(stat worktree.FileStat) {
		f, ok := byPath[stat.Path]
		if !ok {
			f = &DiffFile{Path: stat.Path, Status: stat.Status, Hunks: []DiffHunk{}}
			byPath[stat.Path] = f
		}
		f.Insertions += stat.Insertions
		f.Deletions += stat.Deletions
		f.Binary = f.Binary || stat.Binary
	}
	after := ""
	if baseline != "" {
		// The workspace as it stands, built the same way the baseline was, so the
		// two compare like for like.
		if t, err := rescue.TreeOf(run.Workspace); err == nil {
			after = t
		}
	}
	if baseline != "" && after != "" {
		for _, st := range worktree.StatBetween(run.Workspace, baseline, after) {
			add(st)
		}
	} else {
		// No snapshot was taken — not a repository, or snapshots switched off.
		// The workspace's uncommitted state is still worth showing; it is just a
		// broader question, and DiffScope says which one was answered.
		for _, st := range worktree.DiffStat(sc.Project, branch, base) {
			add(st)
		}
		for _, st := range worktree.WorkingStatIn(run.Workspace) {
			add(st)
		}
	}

	for _, f := range byPath {
		files = append(files, *f)
	}
	sortDiffFiles(files)

	// Content is fetched per file, after the list is known, and only up to a
	// bound: a run that touched four hundred files is a legitimate run, and
	// fetching every hunk for it would turn one screen into four hundred git
	// invocations. The files past the bound keep their counts and lose their
	// hunks, which is the same trade `dirty` already makes when it truncates.
	for i := range files {
		if i >= maxDiffFilesWithHunks {
			break
		}
		files[i].Hunks = s.hunksFor(run.Workspace, branch, base, baseline, after, files[i])
	}
	writeJSON(w, http.StatusOK, files)
}

func sortDiffFiles(files []DiffFile) {
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].Path < files[j-1].Path; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
}

// maxDiffFilesWithHunks bounds how many files get their content fetched.
const maxDiffFilesWithHunks = 60

// hunksFor reads one file's content-level changes.
//
// Three sources, tried in the order that matches how work actually arrives: the
// uncommitted state of the checkout, then anything the branch committed beyond
// its base, then — for a file git has never seen — the file itself, presented as
// one addition. A binary file gets none of them and says so through its own
// flag, rather than through an empty hunk list that reads as "no changes".
func (s *Server) hunksFor(workspace, branch, base, baseline, after string, f DiffFile) []DiffHunk {
	if f.Binary {
		return []DiffHunk{}
	}
	// Against the before-image when there is one: it is the comparison that
	// isolates this run's work, and it holds untracked files too.
	if baseline != "" && after != "" {
		if text := worktree.FileDiff(workspace, baseline, after, f.Path); text != "" {
			return parseUnifiedDiff(text)
		}
		if f.Status == "added" {
			return addedFileHunk(worktree.UntrackedContent(workspace, f.Path))
		}
		return []DiffHunk{}
	}
	if text := worktree.FileDiff(workspace, "HEAD", f.Path); text != "" {
		return parseUnifiedDiff(text)
	}
	if branch != "" && base != "" && branch != base {
		if text := worktree.FileDiff(workspace, base+"..."+branch, f.Path); text != "" {
			return parseUnifiedDiff(text)
		}
	}
	if f.Status == "added" {
		return addedFileHunk(worktree.UntrackedContent(workspace, f.Path))
	}
	return []DiffHunk{}
}

// handleCommitDiff is GET /v1/commits/{sha}/diff?repo=: what one commit changed.
//
// Scoped to one repository — the registered one named by repo, or the one this
// daemon was started in — so the sha is looked up there and nowhere else. It is
// validated as hex before it reaches git, because a value beginning with a dash
// is read as an option rather than a revision, and `--upload-pack=` is the
// classic way that ends badly. git decides whether the object exists; this
// decides whether the string is allowed to be a question.
func (s *Server) handleCommitDiff(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scopeOf(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	sha := r.PathValue("sha")
	stats := worktree.CommitStat(sc.Project, sha)
	if stats == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no commit %q in this project", sha))
		return
	}

	files := make([]DiffFile, 0, len(stats))
	for i, st := range stats {
		f := DiffFile{
			Path:       st.Path,
			Status:     st.Status,
			Insertions: st.Insertions,
			Deletions:  st.Deletions,
			Binary:     st.Binary,
			Hunks:      []DiffHunk{},
		}
		// Same bound as a run's diff, and for the same reason: one commit can
		// legitimately touch hundreds of files, and a screen is not a patch file.
		if i < maxDiffFilesWithHunks && !st.Binary {
			f.Hunks = parseUnifiedDiff(worktree.CommitFileDiff(sc.Project, sha, st.Path))
		}
		files = append(files, f)
	}
	writeJSON(w, http.StatusOK, files)
}

// handleWorktreeDiff is GET /v1/worktrees/{branch}/diff?repo=: what this branch
// has that its base does not, plus whatever is uncommitted in its checkout.
//
// The same two questions a run's diff answers, asked of the *branch* rather than
// of a container — which is what makes it useful after the container is gone.
// A run's diff needs a container to exist; a worktree outlives every container
// that worked in it, and reviewing an agent's work is something you usually do
// afterwards.
//
// It deliberately reuses hunksFor rather than reading git a second way. The
// three sources it tries, in order — the uncommitted state, then commits beyond
// the base, then an untracked file rendered as one addition — are the same three
// that make a run's diff correct, and a second implementation is a second chance
// to get "added file with no diff output" wrong.
func (s *Server) handleWorktreeDiff(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scopeOf(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	branch := r.PathValue("branch")
	// The checkout answers here too, and its answer is narrower on purpose: it
	// *is* what other branches are measured against, so "beyond its base" is
	// empty by definition and what remains is whatever is uncommitted in it.
	primary := branch == worktree.HeadBranch(sc.Project)
	path := sc.Project
	if !primary {
		p, exists, err := worktree.Path(sc.Project, branch)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, fmt.Errorf("no worktree for branch %q", branch))
			return
		}
		path = p
	}

	// The recorded base when a run stamped one, else the checked-out branch —
	// the same resolution the listing and the commits view use, so "6 ahead" and
	// a six-commit diff are counting the same thing. A view that derived the base
	// differently would eventually disagree with `land`, which is the one that
	// matters.
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
	if primary {
		// Nothing to compare against: measuring a branch against itself yields
		// every commit twice or none, and the honest answer for the checkout is
		// its working tree.
		base = ""
	}

	byPath := map[string]*DiffFile{}
	add := func(stat worktree.FileStat) {
		f, ok := byPath[stat.Path]
		if !ok {
			f = &DiffFile{Path: stat.Path, Status: stat.Status, Hunks: []DiffHunk{}}
			byPath[stat.Path] = f
		}
		f.Insertions += stat.Insertions
		f.Deletions += stat.Deletions
		f.Binary = f.Binary || stat.Binary
	}
	// Committed work first, then the working tree. Both, not either: a branch
	// with three commits and an unsaved fourth file is the ordinary state of an
	// agent's worktree, and showing only one half is how a review misses the
	// part that was still in flight.
	if base != "" {
		for _, st := range worktree.DiffStat(sc.Project, branch, base) {
			add(st)
		}
	}
	for _, st := range worktree.WorkingStatIn(path) {
		add(st)
	}

	files := make([]DiffFile, 0, len(byPath))
	for _, f := range byPath {
		files = append(files, *f)
	}
	sortDiffFiles(files)
	for i := range files {
		if i >= maxDiffFilesWithHunks {
			break
		}
		// No baseline: a branch has no before-image the way a run does. The
		// question here is "what is on this branch", not "what did one container
		// change", and hunksFor falls through to exactly that.
		files[i].Hunks = s.hunksFor(path, branch, base, "", "", files[i])
	}
	writeJSON(w, http.StatusOK, files)
}
