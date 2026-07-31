package worktree

import (
	"fmt"
	"os"
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

// FileStat is one file's change in a diff: how it changed and by how much.
type FileStat struct {
	Path       string
	Status     string // "added" | "modified" | "deleted" | "renamed"
	Insertions int
	Deletions  int
	Binary     bool
}

// DiffStat reports what branch committed beyond base, per file.
func DiffStat(dir, branch, base string) []FileStat {
	if branch == "" || base == "" || branch == base {
		return nil
	}
	root, err := RepoRoot(dir)
	if err != nil {
		return nil
	}
	// Three dots: what this branch added since it diverged, not everything that
	// happened on base meanwhile. `land` merges, so the changes being reviewed
	// are the branch's own.
	return fileStats(root, base+"..."+branch)
}

// WorkingStatIn reports what is changed but not committed in a checkout,
// addressed by directory rather than by branch.
//
// By directory because a run's workspace is not always a managed worktree: a
// plain `sandbox-cli claude` mounts the repository you are standing in, and
// resolving "the worktree for this branch" then finds nothing and reports that
// the agent changed nothing at all — which is exactly wrong for the runs people
// look at most.
//
// Included wherever a run's work is shown, because uncommitted is usually where
// an agent's output still is: one that wrote files and did not commit has
// produced the work worth reviewing, and a diff of commits alone calls that
// "nothing changed".
func WorkingStatIn(dir string) []FileStat {
	if dir == "" {
		return nil
	}
	// HEAD rather than the index: staged and unstaged both count as "not
	// committed", and which side of `git add` a change sits on is not the
	// question being asked.
	stats := fileStats(dir, "HEAD")

	// Untracked files are in no diff, and for a scaffolding agent they are most
	// of the work. Counted as added with no line counts, which is honest — git
	// has nothing to compare them against.
	out, err := runGit(dir, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return stats
	}
	for _, p := range strings.Split(strings.TrimSpace(out), "\n") {
		if p != "" {
			stats = append(stats, FileStat{Path: p, Status: "added"})
		}
	}
	return stats
}

// fileStats runs numstat and name-status for one revision range and merges them:
// numstat carries the counts, name-status the kind of change, and neither alone
// answers "what changed and by how much".
func fileStats(dir string, revs ...string) []FileStat {
	numstat, err := runGit(dir, append([]string{"diff", "--numstat"}, revs...)...)
	if err != nil {
		return nil
	}
	status := map[string]string{}
	if out, err := runGit(dir, append([]string{"diff", "--name-status"}, revs...)...); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			status[fields[len(fields)-1]] = statusWord(fields[0])
		}
	}

	var stats []FileStat
	for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		st := FileStat{Path: fields[2], Status: "modified"}
		if s, ok := status[st.Path]; ok {
			st.Status = s
		}
		// git reports a binary file as "-\t-\tpath". Passed through as binary
		// with no counts rather than dropped: "changed, and cannot be shown as
		// text" is a different fact from "did not change".
		if fields[0] == "-" || fields[1] == "-" {
			st.Binary = true
		} else {
			st.Insertions, _ = strconv.Atoi(fields[0])
			st.Deletions, _ = strconv.Atoi(fields[1])
		}
		stats = append(stats, st)
	}
	return stats
}

func statusWord(code string) string {
	switch {
	case strings.HasPrefix(code, "A"):
		return "added"
	case strings.HasPrefix(code, "D"):
		return "deleted"
	case strings.HasPrefix(code, "R"):
		return "renamed"
	default:
		return "modified"
	}
}

// maxDiffBytes bounds one file's unified diff. A generated lockfile or a
// vendored tree is a legitimate change and an unreadable one, and a viewer that
// tries to render megabytes of it stops being a viewer. Truncation is reported
// by the caller rather than hidden.
const maxDiffBytes = 256 * 1024

// FileDiff returns the unified diff of one path, as git writes it.
//
// rev selects what to compare against: a range for committed work, "HEAD" for
// what is uncommitted in a checkout. Parsing is left to the caller — this
// package owns talking to git, and every call here goes through runGit so the
// repository's own config cannot make git run commands (internal/githard).
func FileDiff(dir string, args ...string) string {
	if dir == "" || len(args) < 2 {
		return ""
	}
	// The last argument is the path; everything before it selects what to compare.
	path := args[len(args)-1]
	revs := args[:len(args)-1]
	// No colour, no external textconv, and three lines of context: enough to see
	// where a change sits without shipping the file twice.
	gitArgs := append([]string{"diff", "--no-color", "--no-textconv", "--unified=3"}, revs...)
	gitArgs = append(gitArgs, "--", path)
	out, err := runGit(dir, gitArgs...)
	if err != nil || out == "" {
		return ""
	}
	if len(out) > maxDiffBytes {
		return out[:maxDiffBytes]
	}
	return out
}

// UntrackedContent returns a new file's contents, for showing it as an addition.
//
// Untracked files are in no diff at all, and for an agent that scaffolds
// something they are the whole of the work — so "git has nothing to compare
// this against" must not become "there is nothing to show".
func UntrackedContent(dir, path string) string {
	if dir == "" || path == "" {
		return ""
	}
	// Through git rather than os.ReadFile so the path is resolved by the thing
	// that listed it, and a path escaping the checkout cannot be read by asking
	// for it: `git show` only knows about files in this repository.
	out, err := runGit(dir, "show", ":"+path)
	if err != nil {
		// Not yet in the index, which is the ordinary case for untracked.
		b, rerr := os.ReadFile(filepath.Join(dir, path))
		if rerr != nil {
			return ""
		}
		out = string(b)
	}
	if len(out) > maxDiffBytes {
		return out[:maxDiffBytes]
	}
	return out
}

// StatSince reports what changed in a checkout between two trees.
//
// Both sides are built the same way — a snapshot written with `add -A`, so both
// hold untracked files — which is what makes the comparison honest. Diffing a
// snapshot against the working tree instead reports every untracked file as a
// deletion, because `git diff <commit>` only considers what git tracks while the
// snapshot holds everything.
//
// This is the difference between "what is uncommitted here" and "what did that
// run change": anything the workspace already had is in the before-tree, so it
// cancels out rather than being credited to an agent that never touched it.
func StatBetween(dir, before, after string) []FileStat {
	if dir == "" || before == "" || after == "" {
		return nil
	}
	return fileStats(dir, before, after)
}

// InCommit reports whether a path existed in a commit's tree.
func InCommit(dir, commit, path string) bool {
	if dir == "" || commit == "" || path == "" {
		return false
	}
	// cat-file -e is an existence test: it prints nothing and fails when the
	// object is not there, which is exactly the question.
	_, err := runGit(dir, "cat-file", "-e", commit+":"+path)
	return err == nil
}
