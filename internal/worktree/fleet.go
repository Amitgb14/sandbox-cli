package worktree

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// The queries and operations a supervisor needs on top of creating and listing
// worktrees: which repository a checkout belongs to, whether a branch produced
// anything, and the commit/merge pair that lands it.
//
// Every one of them goes through runGit, and so inherits githard — these run
// inside a repository an agent has been writing to, and git is a programmable
// tool. `add -A` runs clean filters, `merge` runs merge drivers named by
// .gitattributes, and both are files the agent controls. The deliberate exception
// elsewhere in this package is Git(), which is the *user's* own `worktree git`
// command; nothing here may use it.

// MainRepo reports the absolute path of the *main* repository containing dir,
// following a linked worktree back to the repo it belongs to. It returns "" when
// dir is not in a git repository (git is not required to use the sandbox).
//
// This is what makes "every sandbox container for this project" answerable: with
// --worktree each agent runs in a different directory, so only the shared parent
// repository identifies them as belonging together.
func MainRepo(dir string) string {
	// A linked worktree's .git is a pointer file; its common dir is <main>/.git,
	// whose parent is the main checkout.
	if common, ok := GitCommonDir(dir); ok {
		return filepath.Dir(common)
	}
	root, err := RepoRoot(dir)
	if err != nil {
		return ""
	}
	return root
}

// Ahead counts the commits on branch that are not yet in base — the "did this
// agent produce anything I haven't got?" number. Returns 0 for an unknown branch
// or a non-repository rather than an error: it is a status column, and a missing
// count must never fail the listing.
func Ahead(dir, branch, base string) int {
	if branch == "" || base == "" || branch == base {
		return 0
	}
	root, err := RepoRoot(dir)
	if err != nil {
		return 0
	}
	out, err := runGit(root, "rev-list", "--count", base+".."+branch)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return n
}

// HeadBranch reports the branch checked out in the repository containing dir, or
// "" when HEAD is detached (or dir is not a repository). Unlike Branch it does
// *not* fall back to a commit id: a caller about to merge into "the current
// branch" needs a real branch name, and a detached HEAD is a reason to refuse,
// not something to paper over with a sha.
func HeadBranch(dir string) string {
	out, err := runGit(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	b := strings.TrimSpace(out)
	if b == "HEAD" {
		return "" // detached
	}
	return b
}

// IsClean reports whether the working tree at dir has no staged, unstaged, or
// untracked changes. A landing refuses to merge into a dirty checkout, where a
// merge would entangle the user's in-progress work with the merge commit.
func IsClean(dir string) (bool, error) {
	out, err := runGit(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// CommitAll stages everything in branch's worktree (including untracked files)
// and commits it with message. It reports committed=false, with no error, when
// the worktree is clean — landing a branch whose agent already committed is not
// a failure. A non-zero git exit is returned as *ErrGitFailed.
//
// It verifies that the checkout is still *on* branch before staging anything, and
// that check is the point rather than a formality. Path falls back to the
// name-derived directory and reports it as existing, so an agent that ran
// `git checkout -b` inside its own worktree — the exact drift this package exists
// to handle — would otherwise have `add -A && commit` land its work on whatever
// branch the checkout moved to, and the merge that follows would take the
// untouched original. A commit made on the wrong branch after a refusal would be
// the worst of both.
//
// The dirty check is a direct `status --porcelain` rather than Dirty(), which
// swallows every error by design because it feeds a status column. Here an
// unreadable worktree must refuse: reading it as "clean" would merge the branch
// without the agent's uncommitted work and report success.
func CommitAll(dir, branch, message string) (committed bool, err error) {
	path, exists, err := Path(dir, branch)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, fmt.Errorf("no worktree for branch %q", branch)
	}
	if on := HeadBranch(path); on != branch {
		where := "a detached HEAD"
		if on != "" {
			where = fmt.Sprintf("%q", on)
		}
		return false, fmt.Errorf("the worktree for %q is on %s; "+
			"the agent moved it, so committing here would put the work on the wrong branch. "+
			"Check %s and commit it yourself", branch, where, path)
	}
	out, err := runGit(path, "status", "--porcelain")
	if err != nil {
		return false, &ErrGitFailed{Err: err}
	}
	if strings.TrimSpace(out) == "" {
		return false, nil
	}
	if _, err := runGit(path, "add", "-A"); err != nil {
		return false, &ErrGitFailed{Err: err}
	}
	if _, err := runGit(path, "commit", "-m", message); err != nil {
		return false, &ErrGitFailed{Err: err}
	}
	return true, nil
}

// Merge merges branch into the branch currently checked out in the repository
// containing dir, always with --no-ff so the branch's history stays a visible,
// revertible unit. On a merge conflict git leaves the merge in progress and this
// returns *ErrGitFailed; the caller is expected to surface git's own message and
// stop rather than attempting any resolution.
func Merge(dir, branch string) error {
	root, err := RepoRoot(dir)
	if err != nil {
		return err
	}
	if _, err := runGit(root, "merge", "--no-ff", branch); err != nil {
		return &ErrGitFailed{Err: err}
	}
	return nil
}

// Behind counts commits on base that branch does not have — how far a branch has
// fallen behind what it is meant to land on.
//
// The mirror of Ahead, and worth having alongside it: "3 ahead" says there is
// something to land, "3 ahead, 40 behind" says landing it will be a merge. Same
// bargain as Ahead on failure — an unanswerable question counts as zero, because
// this is a status number and one broken worktree must not blank the rest.
func Behind(dir, branch, base string) int {
	if branch == "" || base == "" || branch == base {
		return 0
	}
	root, err := RepoRoot(dir)
	if err != nil {
		return 0
	}
	out, err := runGit(root, "rev-list", "--count", branch+".."+base)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return n
}

// Head returns the abbreviated commit id a branch points at, or "" when it
// cannot be read. Abbreviated because it is shown, not resolved: a caller that
// needs to address the commit has the branch name, which is what every other
// operation here takes.
func Head(dir, branch string) string {
	if branch == "" {
		return ""
	}
	root, err := RepoRoot(dir)
	if err != nil {
		return ""
	}
	out, err := runGit(root, "rev-parse", "--short", branch)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
