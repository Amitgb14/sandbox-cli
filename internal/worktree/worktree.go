// Package worktree makes "one sandbox per git branch" a one-liner. It manages
// git worktrees in a sandbox-owned location (under the config dir, so the user's
// project directory stays clean) so several agents can run in parallel, each on
// its own branch in its own container, without colliding — then reviewed with a
// normal `git checkout <branch>`.
package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// gitBin is the git executable; a variable so tests can stub it if needed.
var gitBin = "git"

// Info describes a resolved worktree.
type Info struct {
	Branch  string
	Path    string
	Created bool // true when Resolve created it this call (vs. reused)
}

// RepoRoot returns the top-level directory of the git repository containing dir.
func RepoRoot(dir string) (string, error) {
	out, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%q is not a git repository (required for --worktree): %w", dir, err)
	}
	return strings.TrimSpace(out), nil
}

// Resolve ensures a git worktree for branch exists in the repo containing dir and
// returns it. If the branch does not exist it is created from the current HEAD.
// An existing sandbox worktree for the branch is reused.
func Resolve(dir, branch string) (Info, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return Info{}, fmt.Errorf("worktree: empty branch name")
	}
	root, err := RepoRoot(dir)
	if err != nil {
		return Info{}, err
	}
	// Reuse the worktree git says holds this branch, wherever it sits: its
	// directory name may no longer match the branch (see lookup), and re-adding
	// a branch that is already checked out is an error.
	if path, ok := lookup(root, branch); ok {
		return Info{Branch: branch, Path: path}, nil
	}
	path := worktreePath(root, branch)
	info := Info{Branch: branch, Path: path}

	// Reuse an existing worktree directory (git tracks it; re-adding would error).
	if isDir(path) {
		return info, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Info{}, fmt.Errorf("worktree: preparing directory: %w", err)
	}

	args := []string{"worktree", "add"}
	if branchExists(root, branch) {
		args = append(args, path, branch)
	} else {
		args = append(args, "-b", branch, path)
	}
	if _, err := runGit(root, args...); err != nil {
		return Info{}, fmt.Errorf("creating worktree for branch %q: %w", branch, err)
	}
	info.Created = true
	return info, nil
}

// List returns the sandbox-managed worktrees for the repo containing dir (those
// living under the managed base directory).
func List(dir string) ([]Info, error) {
	root, err := RepoRoot(dir)
	if err != nil {
		return nil, err
	}
	return listIn(root)
}

// listIn is List for an already-resolved repository root.
func listIn(root string) ([]Info, error) {
	out, err := runGit(root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	// Compare on symlink-resolved paths: `git worktree list` reports resolved
	// paths (e.g. /private/var/... on macOS) while worktreeBase is the logical
	// path (/var/...), so a raw prefix check would drop every managed worktree.
	base := resolveSymlinks(worktreeBase(root))
	var infos []Info
	var cur Info
	flush := func() {
		if cur.Path != "" && strings.HasPrefix(resolveSymlinks(cur.Path), base) {
			infos = append(infos, cur)
		}
		cur = Info{}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return infos, nil
}

// lookup returns the path of the sandbox-managed worktree that currently has
// branch checked out. The directory is named after the branch it was created
// for, but the two drift apart the moment anything inside the worktree switches
// branches (an agent running `git checkout -b`), so git — not the name — is the
// authority on which path holds which branch.
//
// ok is false when no managed worktree has that branch checked out; callers then
// fall back to the name-derived path.
func lookup(root, branch string) (string, bool) {
	// A detached-HEAD worktree lists no branch, so an empty name would match one
	// at random; there is no such branch to look up anyway.
	if branch == "" {
		return "", false
	}
	infos, err := listIn(root)
	if err != nil {
		return "", false
	}
	for _, wt := range infos {
		if wt.Branch == branch {
			return wt.Path, true
		}
	}
	return "", false
}

// Remove deletes the sandbox worktree for branch (git worktree remove). When
// force is false git refuses if the worktree holds modified or untracked files,
// which is the safe default: those edits exist nowhere else.
func Remove(dir, branch string, force bool) error {
	root, err := RepoRoot(dir)
	if err != nil {
		return err
	}
	path, ok := lookup(root, branch)
	if !ok {
		path = worktreePath(root, branch)
		if !isDir(path) {
			return fmt.Errorf("no sandbox worktree for branch %q (see: sandbox-cli worktree list)", branch)
		}
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if _, err := runGit(root, args...); err != nil {
		if !force && strings.Contains(err.Error(), "use --force") {
			return fmt.Errorf("worktree for branch %q has uncommitted work at\n  %s\n"+
				"Commit it first:  sandbox-cli worktree commit %s -m \"...\"\n"+
				"Or discard it:    sandbox-cli worktree rm --force %s",
				branch, path, branch, branch)
		}
		return fmt.Errorf("removing worktree for branch %q: %w", branch, err)
	}
	return nil
}

// Path returns the managed worktree path for branch in the repo containing dir,
// and whether that worktree currently exists. It is the scriptable form of
// List — `cd "$(sandbox-cli worktree path BRANCH)"` — so nobody has to type the
// sandbox-owned directory by hand.
func Path(dir, branch string) (path string, exists bool, err error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", false, fmt.Errorf("worktree: empty branch name")
	}
	root, err := RepoRoot(dir)
	if err != nil {
		return "", false, err
	}
	if path, ok := lookup(root, branch); ok {
		return path, true, nil
	}
	path = worktreePath(root, branch)
	return path, isDir(path), nil
}

// Dirty reports the paths of modified or untracked files in the worktree for
// branch, capped at limit entries (0 = uncapped). Used to warn at exit that work
// lives only in the worktree, rather than letting it surface much later as a
// confusing `worktree rm` refusal. Any error yields no paths: this is a
// best-effort nicety and must never fail a run.
func Dirty(dir, branch string, limit int) []string {
	path, exists, err := Path(dir, branch)
	if err != nil || !exists {
		return nil
	}
	// -z: NUL-separated and never quoted. Plain --porcelain quotes paths
	// containing spaces or non-ASCII, which would surface to the user as
	// `"weird name.txt"`.
	out, err := runGit(path, "status", "--porcelain", "-z")
	if err != nil {
		return nil
	}
	var files []string
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		status, name := entry[:2], entry[3:]
		// Renames and copies emit the destination in this entry and the source
		// as the next NUL-separated field; report the destination and skip it.
		if status[0] == 'R' || status[0] == 'C' {
			i++
		}
		files = append(files, name)
		if limit > 0 && len(files) >= limit {
			break
		}
	}
	return files
}

// ErrGitFailed wraps a non-zero exit from the git subprocess. git has already
// written its own diagnostics to stderr, so callers should set an exit code
// rather than print the error again. Errors that are *not* this (an unknown
// branch, a missing repo) still need reporting.
type ErrGitFailed struct{ Err error }

func (e *ErrGitFailed) Error() string { return e.Err.Error() }
func (e *ErrGitFailed) Unwrap() error { return e.Err }

// Git runs git inside the worktree for branch, streaming output to the caller's
// stdout/stderr, so the worktree can be operated on by branch name instead of
// requiring the user to cd into the sandbox-owned directory. A non-zero git exit
// is returned as *ErrGitFailed.
func Git(dir, branch string, args ...string) error {
	path, exists, err := Path(dir, branch)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no worktree for branch %q", branch)
	}
	cmd := exec.Command(gitBin, args...)
	cmd.Dir = path
	// Passthrough: the user's own git command, so inherit the environment as-is
	// (locale, credential helpers, signing) rather than pinning it like runGit.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return &ErrGitFailed{Err: err}
	}
	return nil
}

// Branch reports the branch checked out in the repository containing dir, or ""
// when dir is not a git repository (git isn't required to use the sandbox). A
// detached HEAD has no branch name, so the short commit id stands in.
func Branch(dir string) string {
	out, err := runGit(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(out)
	if branch == "HEAD" { // detached
		sha, err := runGit(dir, "rev-parse", "--short", "HEAD")
		if err != nil {
			return ""
		}
		return strings.TrimSpace(sha)
	}
	return branch
}

// GitCommonDir reports the repository's main .git directory when dir is a git
// worktree — i.e. when its ".git" is a pointer *file* rather than a directory.
//
// This matters for the sandbox: a worktree's .git file holds an absolute host
// path into the parent repo (".git/worktrees/<name>"), and that path is outside
// the workspace, so inside the container git would resolve the pointer, find
// nothing, and fail with "not a git repository". Mounting the returned directory
// at the same absolute path makes git work normally in the sandbox.
//
// ok is false for a normal repository (.git is a directory) or a non-repository,
// where nothing extra needs mounting.
func GitCommonDir(dir string) (path string, ok bool) {
	dotGit := filepath.Join(dir, ".git")
	fi, err := os.Lstat(dotGit)
	if err != nil || fi.IsDir() {
		return "", false // normal repo, or not a repo at all
	}
	b, err := os.ReadFile(dotGit)
	if err != nil {
		return "", false
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if gitDir == "" {
		return "", false
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	// <main>/.git/worktrees/<name>/commondir points back at <main>/.git.
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
	// Fall back to the conventional layout: .git/worktrees/<name> -> .git
	if parent := filepath.Dir(filepath.Dir(gitDir)); isDir(parent) {
		return filepath.Clean(parent), true
	}
	return "", false
}

func branchExists(root, branch string) bool {
	_, err := runGit(root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// worktreeBase is the managed directory holding all worktrees for one repo,
// namespaced by repo name + a short hash of its absolute path so identically
// named repos never collide.
func worktreeBase(repoRoot string) string {
	root := config.ConfigRoot()
	if root == "" {
		root = filepath.Join(os.TempDir(), "sandbox")
	}
	return filepath.Join(root, "worktrees", repoID(repoRoot))
}

// repoID namespaces one repository: its directory name plus a short hash of its
// absolute path, so two clones sharing a name never share a namespace.
func repoID(repoRoot string) string {
	sum := sha256.Sum256([]byte(repoRoot))
	return filepath.Base(repoRoot) + "-" + hex.EncodeToString(sum[:])[:8]
}

// RepoID is the stable identity of the repository containing dir. Container
// names, container labels and managed worktree paths are all built from it, so
// they agree with each other by construction rather than by coincidence — which
// is what lets a later command find every container belonging to one repo.
func RepoID(dir string) (string, error) {
	root, err := mainRepoRoot(dir)
	if err != nil {
		return "", err
	}
	return repoID(root), nil
}

// mainRepoRoot resolves the *main* repository root for dir, following a linked
// worktree back to the repository it belongs to. `git rev-parse --show-toplevel`
// reports a worktree's own directory, so relying on it alone would give each
// branch of one repo a different identity — and every branch of a repo sharing
// one identity is exactly what the addressing scheme needs.
func mainRepoRoot(dir string) (string, error) {
	if common, ok := GitCommonDir(dir); ok {
		return filepath.Dir(common), nil // <main>/.git -> <main>
	}
	return RepoRoot(dir)
}

// SanitizeName reduces a branch or repository identifier to one safe path or
// container-name segment ("feature/login" -> "feature-login"). Exported because
// container names are assembled from the same pieces as worktree paths: two
// spellings of one branch would mean two containers where the rule is one.
func SanitizeName(s string) string { return sanitizeBranch(s) }

// worktreePath is the managed path for a single branch's worktree.
func worktreePath(repoRoot, branch string) string {
	return filepath.Join(worktreeBase(repoRoot), sanitizeBranch(branch))
}

// sanitizeBranch turns a branch name into a safe single path segment
// (e.g. "feature/login" -> "feature-login").
func sanitizeBranch(b string) string {
	var sb strings.Builder
	prevDash := true // suppress leading separators
	for _, r := range b {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_':
			sb.WriteRune(r)
			prevDash = false
		case !prevDash:
			sb.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(sb.String(), "-")
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// resolveSymlinks returns the symlink-resolved path, falling back to the cleaned
// input when it can't be resolved (e.g. the path does not exist yet).
func resolveSymlinks(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// gitEnv pins the environment for git subprocesses whose output we parse:
// LC_ALL=C keeps messages in English (we match on some of them, and a translated
// locale would silently break that), and GIT_TERMINAL_PROMPT=0 makes git fail
// instead of blocking forever on a credential prompt with no terminal attached.
func gitEnv() []string {
	return append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command(gitBin, args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", fmt.Errorf("%s: %w", msg, err)
		}
		return "", err
	}
	return out.String(), nil
}
