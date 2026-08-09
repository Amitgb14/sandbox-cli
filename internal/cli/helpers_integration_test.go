//go:build docker_integration || podman_integration

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A workspace the container can actually write to.
//
// t.TempDir() is 0700 — os.MkdirTemp hardcodes that mode and ignores the umask —
// and on Linux a bind mount carries real uids: the container runs as uid 1001
// with the host's primary gid, so the group is granted nothing and every write
// to /workspace fails with EACCES. Eleven tests failed that way at once, each
// reporting a missing file rather than the permission error underneath it.
//
// This is a property of MkdirTemp, not of the tool. An ordinary project
// directory is 0775 under the usual umask 002, which the container reaches
// through the shared group; only a directory created with an explicit 0700 is
// out of reach. So the fix belongs here rather than in sandbox.BuildSpec, which
// would otherwise be widening the mode of a directory the user chose.
//
// It also explains why the suite was green for so long: off Linux, Docker
// Desktop virtualizes bind-mount ownership, so the mode never decided anything
// and every one of these tests passed on a mode that could not work.
//
// Both tags: the podman file is built under its own, and a helper that existed
// for only one of them would be copied rather than shared.
func testWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	allowWrite(t, dir)
	// t.TempDir() nests the per-test directory one level inside a per-run one,
	// and a container must traverse *every* component of a path — the rule
	// sandbox.EnsureGuestDir exists for. Search is all an ancestor needs.
	allowTraverse(t, filepath.Dir(dir))
	return dir
}

// allowWrite gives the container's group read, write and search on dir. 0770
// rather than 0777: the group is the whole mechanism, and nothing here should
// make a test directory world-writable on a machine somebody else uses.
func allowWrite(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatalf("opening %s for the container: %v", dir, err)
	}
}

// allowTraverse gives the container's group search on dir and nothing else, for
// a directory it must pass through rather than write in.
func allowTraverse(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o710); err != nil {
		t.Fatalf("opening %s for traversal: %v", dir, err)
	}
}

// allowTraverseTo grants search on every directory from root down to leaf's
// parent, inclusive.
//
// For a path the test creates itself, opening the leaf and its immediate parent
// is enough. For one built *under* a root by the code under test — the managed
// worktree location is the case here — the directories in between are created
// with whatever umask the run has, and a stricter one would break a single test
// in a way that reads as a worktree bug rather than a mode.
func allowTraverseTo(t *testing.T, root, leaf string) {
	t.Helper()
	// Through symlinks first, and on macOS that is not pedantry: /var is a link
	// to /private/var, worktree.Path resolves the paths it hands out, and
	// t.TempDir() does not — so the two arrive spelled differently and Rel
	// answers with a climb out of the root.
	root = resolvedPath(t, root)
	leaf = resolvedPath(t, leaf)
	rel, err := filepath.Rel(root, leaf)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("%s is not under %s (rel %q): %v", leaf, root, rel, err)
	}
	cur := root
	allowTraverse(t, cur)
	for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		if part == "." || part == "" {
			break
		}
		cur = filepath.Join(cur, part)
		allowTraverse(t, cur)
	}
}

// resolvedPath is EvalSymlinks with the test's own failure message. The path must
// exist by the time it is called, which is why the chain is opened after the
// worktree is created rather than before.
func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving %s: %v", path, err)
	}
	return p
}
