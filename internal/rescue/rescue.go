// Package rescue is the crash safety net for the user's work.
//
// A sandbox mounts the project — and, for a worktree run, the parent .git —
// read-write at their real host paths, so everything the agent does lands
// directly on the host with nothing in between. When the container, the docker
// daemon, or sandbox-cli itself dies mid-write, the files are still on disk but
// git may no longer be able to see them: a pruned .git/worktrees entry makes
// every git command fail with "not a git repository", and an agent that runs
// `git reset --hard` throws away its own commits with no reflog the user knows
// how to reach.
//
// This package answers both halves of that. While a run is in flight a
// Snapshotter commits the workspace into the repository's own object store
// under refs/sandbox/snapshots/, using a private index file so the user's index,
// HEAD, branches and working tree are never written to. When something has
// already gone wrong, Diagnose/Repair fix the broken repository and
// List/Restore put any snapshot back on a named branch.
//
// The one rule every function here keeps: rescue only ever *creates* git objects
// and refs under refs/sandbox/. It never moves a branch, never rewrites the
// index, and never touches a file in the working tree unless the user explicitly
// asked for a restore into it.
package rescue

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/githard"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// gitBin is the git executable; a variable so tests can stub it if needed.
var gitBin = "git"

const (
	// RefPrefix is the namespace every snapshot ref lives under. refs/sandbox is
	// outside refs/heads and refs/remotes on purpose: `git branch` and `git log
	// --all` ignore it, `git push` never sends it, and nothing in a normal
	// workflow trips over it — while still being a real ref, which is what keeps
	// the objects it points at safe from `git gc`.
	RefPrefix = "refs/sandbox/snapshots/"

	// RestoreBranchPrefix namespaces the branches `recover restore` creates, so a
	// recovered branch is obvious in `git branch` and easy to clean up later.
	RestoreBranchPrefix = "sandbox-recover/"
)

// snapshotIdentity is stamped on every snapshot commit. It is pinned rather than
// borrowed from the user's git config for two reasons: `commit-tree` fails
// outright when no identity is configured, which would turn a missing
// user.email into a lost snapshot; and a snapshot is sandbox-cli's commit, not
// the user's, so it should not be attributed to them.
var snapshotIdentity = []string{
	"GIT_AUTHOR_NAME=sandbox-cli",
	"GIT_AUTHOR_EMAIL=sandbox-cli@localhost",
	"GIT_COMMITTER_NAME=sandbox-cli",
	"GIT_COMMITTER_EMAIL=sandbox-cli@localhost",
}

// baseEnv pins the environment for every git subprocess this package runs.
// LC_ALL=C keeps messages parseable, GIT_TERMINAL_PROMPT=0 makes git fail rather
// than block forever on a credential prompt with no terminal attached (a
// snapshot runs unattended, behind the agent's own UI).
func baseEnv() []string {
	return append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
}

// run executes git in dir and returns its output with surrounding whitespace
// trimmed — the right thing for the object ids, paths and yes/no answers this
// package asks git for.
//
// Use runRaw when the output *is* the content: trimming a patch removes the
// trailing newline every hunk needs, and `git apply` rejects what comes back.
func run(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	out, err := runRaw(ctx, dir, env, args...)
	return strings.TrimSpace(out), err
}

// runRaw is run without the trimming. Every call goes through here so the pinned
// environment and the "no auto-gc" rule are impossible to forget at a call site.
func runRaw(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	full := append(append([]string{"-c", "gc.auto=0"}, githard.Args(dir)...), args...)
	cmd := exec.CommandContext(ctx, gitBin, full...)
	cmd.Dir = dir
	cmd.Env = append(baseEnv(), githard.Env(dir)...)
	cmd.Env = append(cmd.Env, env...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s: %w", args[0], msg, err)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return out.String(), nil
}

// stream runs git with the caller's stdout/stderr attached, for the commands
// whose output is the answer (`show`, `diff`). git has already explained any
// failure on stderr, so the error is returned bare for the caller to turn into
// an exit code.
func stream(dir string, args ...string) error {
	full := append(append([]string{"-c", "gc.auto=0"}, githard.Args(dir)...), "--no-pager")
	full = append(full, args...)
	cmd := exec.Command(gitBin, full...)
	cmd.Dir = dir
	cmd.Env = append(baseEnv(), githard.Env(dir)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Available reports whether git is usable at all. Rescue is best-effort: a host
// without git gets no safety net rather than a failed run.
func Available() bool {
	_, err := exec.LookPath(gitBin)
	return err == nil
}

// MainRepoRoot returns the top-level directory of the *main* repository for dir,
// which is the same answer from the main checkout and from every linked worktree
// of it. That shared identity is the point: a crash happens in a --worktree
// sandbox, and the user goes looking for their work from their normal checkout.
//
// It falls back to parsing the .git pointer file by hand, because the case this
// package exists for is precisely the one where git refuses to answer.
func MainRepoRoot(dir string) (string, error) {
	if out, err := run(context.Background(), dir, nil, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && out != "" {
		// <main>/.git -> <main>. This is the linked-worktree case and the reason
		// the common dir is asked for at all: every worktree of a repository
		// answers with the main checkout.
		if filepath.Base(out) == ".git" {
			return filepath.Dir(out), nil
		}
		// Anything else means the git directory is not beside the tree it
		// belongs to — a submodule (<super>/.git/modules/<name>) or a repository
		// created with --separate-git-dir. Returning it would name git's internal
		// storage as the repository, and this answer becomes a project root that
		// gets bind-mounted at /workspace: the agent would be handed an object
		// store instead of the source, and a snapshot would be taken of the
		// wrong tree. The working tree is the answer in both cases, and git will
		// name it.
		if top, err := run(context.Background(), dir, nil, "rev-parse", "--path-format=absolute", "--show-toplevel"); err == nil && top != "" {
			return top, nil
		}
		// No working tree: a bare repository, which genuinely is its own root.
		return out, nil
	}
	if common, ok := worktree.GitCommonDir(dir); ok {
		return filepath.Dir(common), nil
	}
	root, err := worktree.RepoRoot(dir)
	if err != nil {
		return "", fmt.Errorf("%q is not a git repository", dir)
	}
	return root, nil
}

// headSHA returns the commit HEAD points at, or "" when there is none (an
// unborn branch in a fresh repository) or git cannot say.
func headSHA(ctx context.Context, dir string) string {
	out, err := run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// isAncestor reports whether commit a is reachable from b.
func isAncestor(ctx context.Context, dir, a, b string) bool {
	_, err := run(ctx, dir, nil, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// objectExists reports whether the repository still has the named object. A
// snapshot ref can outlive its objects only if someone deliberately deleted
// them, but a *pruned* ref with a sha still in a manifest is normal, so
// restoring always checks before promising anything.
func objectExists(ctx context.Context, dir, sha string) bool {
	_, err := run(ctx, dir, nil, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// sanitizeRef turns a branch name into something safe to embed in a new ref
// name, mirroring worktree.sanitizeBranch's approach (path separators and
// anything git dislikes collapse to a dash).
func sanitizeRef(b string) string {
	var sb strings.Builder
	prevDash := true
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
