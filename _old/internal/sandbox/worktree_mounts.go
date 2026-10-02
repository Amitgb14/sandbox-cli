package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// LinkedWorktreeMounts returns the extra `host:container:mode` binds a sandbox
// needs when its workspace is a linked git worktree, or nil for a normal checkout.
//
// It lives here, rather than in whichever caller happened to need it first,
// because there are now two: the CLI's own run path and the fleet runner. Both
// must apply these or the agent can edit files and never commit them — and both
// must apply the *same* ones, since the third mount below is a containment fix
// and not a convenience.
//
// Three mounts, for three distinct reasons:
//
//  1. The worktree's .git is a pointer *file* holding an absolute host path into
//     the parent repo, which lives outside the workspace. Without the parent .git
//     mounted at that same path, every git command inside the container fails
//     with "not a git repository".
//
//  2. The worktree is mounted a second time at its own host path. The parent repo
//     records each linked worktree by absolute path and treats a record whose path
//     has vanished as a deleted worktree, so inside the container every one of them
//     reads as prunable. Since the parent .git is mounted read-write so the agent
//     can commit, a `git worktree prune` (or the one `git gc` runs for itself)
//     would reach out of the container and delete the user's entire worktree
//     registry. Making the path resolve is one extra bind of a directory that is
//     already mounted, so it grants no reach the container did not have a moment
//     ago.
//
//  3. The parent repository's .git/hooks, read-only over the read-write bind
//     above. Hooks are not project source: they are programs the *user's* git runs,
//     on the host, as them. An agent that writes a pre-commit hook is not editing
//     the project, it is waiting for the user's next commit — a confirmed escape.
//     hooks specifically and not .git as a whole, because agents legitimately run
//     `git config` and git itself writes indexes and refs constantly.
//
// The .git path comes from a pointer file inside the workspace, which the agent
// can rewrite, and is about to be mounted read-write at its own host location — so
// it goes through the same non-overridable refusals as the workspace itself.
// worktree.GitCommonDir already requires the target to look like a real git
// directory; RefuseUnsafeHostPath is the second layer, and the one that would still
// hold if that check were ever loosened.
func LinkedWorktreeMounts(projectDir string) ([]string, error) {
	dir := config.ExpandTilde(projectDir)
	gitDir, ok := worktree.GitCommonDir(dir)
	if !ok || RefuseUnsafeHostPath(gitDir) != nil {
		return nil, nil
	}

	mounts := []string{gitDir + ":" + gitDir + ":rw"}
	if wt, err := filepath.Abs(dir); err == nil {
		mounts = append(mounts, wt+":"+wt+":rw")
	}
	h := filepath.Join(gitDir, "hooks")
	mount, err := hooksDir(h)
	if err != nil {
		return nil, err
	}
	if mount {
		mounts = append(mounts, h+":"+h+":ro")
	}
	return mounts, nil
}

// hooksDir reports whether p is a real hooks directory to cover with a read-only
// mount, and refuses when it is a symlink.
//
// A missing hooks directory is ordinary — nothing to cover, nothing to mount. A
// symlink is not. The rw mount lets the agent replace `.git/hooks` with a link,
// and docker follows a link in a bind source: `hooks -> /home/you/.ssh` was
// mounted into the next run, and `hooks -> /` would have been, with
// RefuseUnsafeHostPath never consulted because the string was never a path
// anybody typed. Confirmed against the real engine. Skipping the mount instead
// would not be safe either: the host's own git follows the link, so a link into
// the workspace is a hooks directory the agent writes. Refusing — on every run,
// in every profile — is the one answer that is safe, and it is said loudly
// because the likeliest author is a previous run.
//
// Lstat leaves a window between this check and the engine resolving the mount,
// which a *concurrent* run in the same repository could use. That is narrower
// than what this closes, and closing it needs the engine to refuse links itself.
func hooksDir(p string) (bool, error) {
	fi, err := os.Lstat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("checking %s: %w", p, err)
	case fi.Mode()&fs.ModeSymlink != 0:
		target, _ := os.Readlink(p)
		return false, fmt.Errorf("refusing to start: %s is a symlink (to %q), and git runs what it finds there on the host. "+
			"An earlier sandbox run may have planted it — inspect it, then replace it with a real directory "+
			"(or set core.hooksPath in your own config)", p, target)
	}
	return fi.IsDir(), nil
}
