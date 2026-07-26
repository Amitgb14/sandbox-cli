package rescue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// Finding kinds. The kind is what `recover repair` dispatches on.
const (
	// KindWorktreeAdmin is the one that takes a repository out completely: the
	// worktree's .git pointer file names an admin directory in the parent repo
	// that no longer exists, so *every* git command fails with "not a git
	// repository" and the user cannot even see their own changes.
	KindWorktreeAdmin = "worktree-admin-missing"
	// KindStaleLock is a lock file left behind by a git process that was killed
	// mid-write.
	KindStaleLock = "stale-lock"
	// KindInProgress is an interrupted merge/rebase/cherry-pick. Reported, never
	// touched: only the user knows whether they want to finish it or abort it.
	KindInProgress = "operation-in-progress"
)

// staleLockAge is how old a lock file must be before it is called stale. There
// is no reliable, portable way to ask "is a git process still holding this?",
// so age stands in — well past any plausible live operation in a repository
// whose sandbox has already exited.
const staleLockAge = 5 * time.Minute

// Finding is one thing wrong with a repository, and what can be done about it.
type Finding struct {
	Kind    string
	Summary string // one line, what is wrong
	Detail  string // where, and what it means
	Fix     string // what Repair will do; "" when this is report-only
	Advice  string // what the user should do when Repair will not act

	// Resolved targets for the fixer.
	worktreeDir string // the checkout whose .git is dangling
	gitDir      string // the admin directory to rebuild
	commonDir   string // <main>/.git
	branch      string // branch the worktree was on, when known
	head        string // fallback commit when the branch itself is gone
	lockPath    string
}

// Repairable reports whether Repair can act on this finding as it stands.
func (f Finding) Repairable() bool { return f.Fix != "" }

// RepairableWith reports whether Repair can act once branchOverride is taken
// into account, and returns the description to show the user.
//
// The override exists because the branch is the one thing a destroyed worktree
// admin directory takes with it: when no session manifest recorded it, Diagnose
// can only say "re-run with --branch NAME". That advice used to be unreachable —
// the finding had Advice but no Fix, so Repairable() was false and the caller
// skipped it before Repair ever saw the override. Supplying the name is exactly
// what makes the finding actionable, so the two questions have to be asked
// together.
func (f Finding) RepairableWith(branchOverride string) (string, bool) {
	if f.Repairable() {
		return f.Fix, true
	}
	if f.Kind == KindWorktreeAdmin && strings.TrimSpace(branchOverride) != "" {
		return fmt.Sprintf("recreate %s for branch %q and rebuild the index", f.gitDir, branchOverride), true
	}
	return "", false
}

// Diagnose inspects the repository containing dir and reports what is broken.
//
// It deliberately works from the filesystem rather than from git: the headline
// failure it exists to catch is the one where git itself refuses to run, so
// anything that shells out to git can only be a secondary check.
func Diagnose(dir string) ([]Finding, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var findings []Finding

	dotGit := filepath.Join(abs, ".git")
	fi, statErr := os.Lstat(dotGit)
	switch {
	case statErr != nil:
		return nil, fmt.Errorf("%q is not a git repository (no .git)", abs)

	case !fi.IsDir():
		// A linked worktree. Resolve the pointer by hand and see whether the admin
		// directory it names is really there.
		gitDir, perr := pointerTarget(abs, dotGit)
		if perr != nil {
			return nil, perr
		}
		if !isDir(gitDir) {
			findings = append(findings, worktreeAdminFinding(abs, gitDir))
		} else {
			findings = append(findings, lockFindings(gitDir)...)
			findings = append(findings, inProgressFindings(gitDir)...)
		}
		if common, ok := commonDirOf(gitDir); ok {
			findings = append(findings, lockFindings(common)...)
		}

	default:
		findings = append(findings, lockFindings(dotGit)...)
		findings = append(findings, inProgressFindings(dotGit)...)
		findings = append(findings, orphanedWorktreeFindings(dotGit)...)
	}
	return findings, nil
}

// worktreeAdminFinding builds the headline finding, filling in the branch from
// the session manifests when it can. That lookup is the reason sessions are
// recorded outside the repository: with the admin directory gone, the branch the
// agent was working on exists nowhere else on disk.
func worktreeAdminFinding(wtDir, gitDir string) Finding {
	f := Finding{
		Kind:        KindWorktreeAdmin,
		Summary:     "git is unusable here: this worktree's administrative directory is missing",
		Detail:      fmt.Sprintf("%s points at %s, which does not exist", filepath.Join(wtDir, ".git"), gitDir),
		worktreeDir: wtDir,
		gitDir:      gitDir,
	}
	// <main>/.git/worktrees/<name> -> <main>/.git
	f.commonDir = filepath.Dir(filepath.Dir(gitDir))
	if repoRoot := filepath.Dir(f.commonDir); repoRoot != "" {
		if sess, ok := lastSessionFor(repoRoot, wtDir); ok {
			f.branch, f.head = sess.Branch, sess.HeadAtStart
		}
	}
	if f.branch != "" {
		f.Fix = fmt.Sprintf("recreate %s for branch %q and rebuild the index", gitDir, f.branch)
	} else {
		f.Advice = "sandbox-cli has no record of the branch this worktree was on; " +
			"re-run with --branch NAME to rebuild it"
	}
	return f
}

// lockFindings reports lock files old enough that no live git can own them.
func lockFindings(gitDir string) []Finding {
	var out []Finding
	for _, name := range []string{"index.lock", "HEAD.lock", "packed-refs.lock", "config.lock"} {
		p := filepath.Join(gitDir, name)
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		age := time.Since(fi.ModTime())
		if age < staleLockAge {
			continue
		}
		out = append(out, Finding{
			Kind:     KindStaleLock,
			Summary:  fmt.Sprintf("stale %s left by an interrupted git", name),
			Detail:   fmt.Sprintf("%s, untouched for %s — git refuses to write while it is there", p, age.Round(time.Minute)),
			Fix:      "delete the lock file",
			lockPath: p,
		})
	}
	return out
}

// inProgressFindings reports an interrupted multi-step operation. Never
// repaired: finishing or aborting a merge is the user's call, and guessing wrong
// discards real work.
func inProgressFindings(gitDir string) []Finding {
	ops := []struct{ file, what, advice string }{
		{"MERGE_HEAD", "a merge", "git merge --continue, or git merge --abort"},
		{"CHERRY_PICK_HEAD", "a cherry-pick", "git cherry-pick --continue, or git cherry-pick --abort"},
		{"REVERT_HEAD", "a revert", "git revert --continue, or git revert --abort"},
		{"BISECT_LOG", "a bisect", "git bisect reset"},
	}
	var out []Finding
	for _, op := range ops {
		if _, err := os.Stat(filepath.Join(gitDir, op.file)); err != nil {
			continue
		}
		out = append(out, Finding{
			Kind:    KindInProgress,
			Summary: op.what + " was interrupted and is still in progress",
			Detail:  filepath.Join(gitDir, op.file) + " is present",
			Advice:  op.advice,
		})
	}
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if isDir(filepath.Join(gitDir, d)) {
			out = append(out, Finding{
				Kind:    KindInProgress,
				Summary: "a rebase was interrupted and is still in progress",
				Detail:  filepath.Join(gitDir, d) + " is present",
				Advice:  "git rebase --continue, or git rebase --abort",
			})
		}
	}
	return out
}

// orphanedWorktreeFindings looks the other way round: run from the main repo,
// find sandbox worktrees still on disk whose registry entry has been deleted.
// That is what an in-container `git worktree prune` (or the one `git gc` runs
// for itself) does when it reaches out through the read-write .git mount.
func orphanedWorktreeFindings(commonDir string) []Finding {
	root := filepath.Dir(commonDir)
	sessions, err := Sessions(root)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []Finding
	for _, s := range sessions {
		wt := s.Workspace
		if wt == "" || wt == root || seen[wt] {
			continue
		}
		seen[wt] = true
		dotGit := filepath.Join(wt, ".git")
		fi, err := os.Lstat(dotGit)
		if err != nil || fi.IsDir() {
			continue // gone, or not a linked worktree
		}
		gitDir, err := pointerTarget(wt, dotGit)
		if err != nil || isDir(gitDir) {
			continue
		}
		f := worktreeAdminFinding(wt, gitDir)
		f.Summary = fmt.Sprintf("worktree %q is orphaned: its administrative directory is missing", filepath.Base(wt))
		out = append(out, f)
	}
	return out
}

// Repair applies one finding's fix.
//
// It never moves a ref. That restraint is deliberate: another checkout may be
// sitting on the branch being repaired, and moving it would silently make the
// user's own working tree dirty in a way nothing would explain later.
func Repair(f Finding, branchOverride string) error {
	switch f.Kind {
	case KindStaleLock:
		if err := os.Remove(f.lockPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", f.lockPath, err)
		}
		return nil

	case KindWorktreeAdmin:
		branch := branchOverride
		if branch == "" {
			branch = f.branch
		}
		if branch == "" && f.head == "" {
			return fmt.Errorf("cannot repair %s: the branch it was on is unknown; pass --branch NAME", f.worktreeDir)
		}
		return repairWorktreeAdmin(f, branch)

	default:
		return fmt.Errorf("%s is reported only, not repaired: %s", f.Kind, f.Advice)
	}
}

// repairWorktreeAdmin rebuilds the three files a linked worktree needs in the
// parent repository, then rebuilds the index.
//
// `git worktree repair` is not the answer here and is not attempted: it
// reconnects worktrees that *moved*, and a deleted admin directory has nothing
// left to reconnect. The files below are the whole contract — HEAD (what this
// worktree has checked out), commondir (how to find the shared object store),
// and gitdir (the way back to the checkout).
func repairWorktreeAdmin(f Finding, branch string) error {
	// f.gitDir traces back to the `gitdir:` line of a .git pointer file inside the
	// workspace — which the sandbox mounts read-write, so the agent chooses it.
	// Everything below creates a directory and writes files there, so an
	// unvalidated target made `recover repair` a write-anywhere primitive: the
	// audit's proof planted a symlink in the (also rw-mounted) parent .git and had
	// three files land in an unrelated directory. rescue's stated rule is that it
	// only ever creates objects and refs under refs/sandbox/; this is what keeps
	// that true for the repair path.
	if err := validWorktreeAdminDir(f.gitDir); err != nil {
		return err
	}
	if err := os.MkdirAll(f.gitDir, 0o755); err != nil {
		return fmt.Errorf("recreating %s: %w", f.gitDir, err)
	}
	head := "ref: refs/heads/" + branch + "\n"
	if branch == "" || !branchExists(f.commonDir, branch) {
		// The branch itself went with the crash. Detach at the last commit we know
		// this worktree was on rather than inventing a branch: the user gets a
		// working repository and can name the branch themselves.
		if f.head == "" {
			return fmt.Errorf("branch %q no longer exists and no starting commit was recorded; pass --branch NAME", branch)
		}
		head = f.head + "\n"
	}
	files := map[string]string{
		"HEAD":      head,
		"commondir": "../..\n",
		"gitdir":    filepath.Join(f.worktreeDir, ".git") + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(f.gitDir, name), []byte(content), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", filepath.Join(f.gitDir, name), err)
		}
	}
	// Rebuild the index from HEAD. Plain reset, never --hard: the uncommitted work
	// in this checkout is exactly what we are here to save.
	if _, err := run(context.Background(), f.worktreeDir, nil, "reset", "-q"); err != nil {
		return fmt.Errorf("rebuilding the index in %s: %w", f.worktreeDir, err)
	}
	return nil
}

// pointerTarget reads a worktree's .git pointer file and returns the absolute
// admin directory it names.
func pointerTarget(wtDir, dotGit string) (string, error) {
	b, err := os.ReadFile(dotGit)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", dotGit, err)
	}
	target := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if target == "" {
		return "", fmt.Errorf("%s is empty; it should hold a \"gitdir:\" line", dotGit)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(wtDir, target)
	}
	return filepath.Clean(target), nil
}

// commonDirOf returns <main>/.git for a linked worktree's admin directory.
func commonDirOf(gitDir string) (string, bool) {
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common := strings.TrimSpace(string(b))
		if common != "" {
			if !filepath.IsAbs(common) {
				common = filepath.Join(gitDir, common)
			}
			if isDir(common) {
				return filepath.Clean(common), true
			}
		}
	}
	parent := filepath.Dir(filepath.Dir(gitDir))
	return parent, isDir(parent)
}

// lastSessionFor finds the most recent recorded run whose workspace was wtDir.
func lastSessionFor(repoRoot, wtDir string) (Session, bool) {
	sessions, err := Sessions(repoRoot)
	if err != nil {
		return Session{}, false
	}
	// Compare symlink-resolved: the session recorded the path the run was given,
	// and the user may well be standing on a different spelling of it (/var vs
	// /private/var on macOS, a symlinked checkout anywhere).
	want := resolved(wtDir)
	for _, s := range sessions { // already newest-first
		if resolved(s.Workspace) == want && s.Branch != "" {
			return s, true
		}
	}
	return Session{}, false
}

// resolved is filepath.EvalSymlinks with a fallback for paths that no longer
// exist — a worktree directory the crash took with it still has to compare
// equal to its own recorded path.
func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func branchExists(commonDir, branch string) bool {
	if branch == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(commonDir, "refs", "heads", filepath.FromSlash(branch))); err == nil {
		return true
	}
	// Packed refs: a plain substring check is enough for a yes/no here, and it
	// works with a git that cannot run.
	b, err := os.ReadFile(filepath.Join(commonDir, "packed-refs"))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), " refs/heads/"+branch+"\n")
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// validWorktreeAdminDir checks that path is where a linked worktree's
// administrative directory can legitimately live: <main>/.git/worktrees/<name>,
// with symlinks resolved.
//
// Two checks, because either alone is bypassable. The *lexical* one — the parent
// must be named "worktrees" and its parent must be a real git directory — stops
// a pointer that simply names somewhere else (`gitdir: /tmp/anywhere/x`). The
// *resolved* one stops the same thing arriving through a symlink, which is how
// the audit reached outside the repository: the parent .git of a worktree is
// bind-mounted read-write into the container, so the agent can plant a link
// inside it and have the write follow that link out.
//
// Both are deliberately shape checks rather than a list of forbidden
// destinations. A forbidden-list has to anticipate the target; requiring the
// path to *be* a worktree admin directory means an attacker has to already own a
// real git directory at the destination, which is the thing they are trying to
// obtain.
func validWorktreeAdminDir(path string) error {
	refuse := func(why string) error {
		return fmt.Errorf("refusing to repair %q: %s.\n"+
			"  This path comes from the .git pointer file in the worktree, which an agent "+
			"can rewrite; a worktree's admin directory must be <repo>/.git/worktrees/<name>", path, why)
	}
	if path == "" || !filepath.IsAbs(path) {
		return refuse("it is not an absolute path")
	}
	parent := filepath.Dir(path) // .../.git/worktrees
	if filepath.Base(parent) != "worktrees" {
		return refuse("its parent directory is not named \"worktrees\"")
	}
	// The .git it claims to belong to must be a real git directory. Resolving
	// first means a symlinked .git cannot stand in for one.
	gitCommon, err := filepath.EvalSymlinks(filepath.Dir(parent))
	if err != nil || !worktree.IsGitDir(gitCommon) {
		return refuse("the repository it names does not exist or is not a git directory")
	}
	// If "worktrees" itself already exists, it must really be that directory and
	// not a link pointing somewhere else — the case the audit exploited.
	if realParent, err := filepath.EvalSymlinks(parent); err == nil {
		if filepath.Base(realParent) != "worktrees" || filepath.Dir(realParent) != gitCommon {
			return refuse("its \"worktrees\" directory is a link out of the repository")
		}
	}
	return nil
}
