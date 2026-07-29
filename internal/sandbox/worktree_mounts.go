package sandbox

import (
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
func LinkedWorktreeMounts(projectDir string) []string {
	dir := config.ExpandTilde(projectDir)
	gitDir, ok := worktree.GitCommonDir(dir)
	if !ok || RefuseUnsafeHostPath(gitDir) != nil {
		return nil
	}

	mounts := []string{gitDir + ":" + gitDir + ":rw"}
	if wt, err := filepath.Abs(dir); err == nil {
		mounts = append(mounts, wt+":"+wt+":rw")
	}
	if h := filepath.Join(gitDir, "hooks"); isDirPath(h) {
		mounts = append(mounts, h+":"+h+":ro")
	}
	return mounts
}

// isDirPath reports whether p exists and is a directory. A missing hooks
// directory is ordinary — nothing to cover, nothing to mount.
func isDirPath(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
