package studioapi

import (
	"net/http"

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
	if base == "" {
		base = worktree.HeadBranch(s.Project)
	}

	// Two questions, one answer: what this branch committed beyond its base, and
	// what is still uncommitted in its checkout. Merged by path so a file that
	// was both committed and edited again appears once, with the totals added.
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
	for _, st := range worktree.DiffStat(s.Project, branch, base) {
		add(st)
	}
	for _, st := range worktree.WorkingStatIn(run.Workspace) {
		add(st)
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
		files[i].Hunks = s.hunksFor(run.Workspace, branch, base, files[i])
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
func (s *Server) hunksFor(workspace, branch, base string, f DiffFile) []DiffHunk {
	if f.Binary {
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
