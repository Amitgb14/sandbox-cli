package rescue

import (
	"path/filepath"
	"testing"
)

// TestMainRepoRootNamesAWorkingTree pins the four layouts git can put a
// repository in, because the answer here becomes a Studio project root — and a
// project root is bind-mounted at /workspace. Naming git's internal storage
// instead of the tree hands an agent an object store and takes a snapshot of the
// wrong thing, which is why the submodule case is a bug rather than a curiosity.
func TestMainRepoRootNamesAWorkingTree(t *testing.T) {
	t.Run("a linked worktree resolves to the main checkout", func(t *testing.T) {
		main := initRepo(t)
		linked := filepath.Join(t.TempDir(), "wt")
		git(t, main, "worktree", "add", "-q", "-b", "feature", linked)
		t.Cleanup(func() { _, _ = gitCmd(main, "worktree", "remove", "--force", linked).CombinedOutput() })

		got, err := MainRepoRoot(linked)
		if err != nil {
			t.Fatal(err)
		}
		if want := realpath(t, main); got != want {
			t.Errorf("MainRepoRoot(worktree) = %s, want the main checkout %s", got, want)
		}
	})

	t.Run("a submodule resolves to its own tree, not the superproject's git dir", func(t *testing.T) {
		mod := initRepo(t)
		super := initRepo(t)
		// protocol.file.allow: git refuses local-path submodules by default since
		// CVE-2022-39253. This is a fixture on disk, not a clone of anything a
		// repository named.
		if out, err := gitCmd(super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", mod, "mod").CombinedOutput(); err != nil {
			t.Skipf("submodule add failed (%v): %s", err, out)
		}
		inside := filepath.Join(super, "mod")

		got, err := MainRepoRoot(inside)
		if err != nil {
			t.Fatal(err)
		}
		// The old rule returned <super>/.git/modules/mod here.
		if want := realpath(t, inside); got != want {
			t.Errorf("MainRepoRoot(submodule) = %s, want the submodule tree %s", got, want)
		}
	})

	t.Run("a separate git dir resolves to the tree it belongs to", func(t *testing.T) {
		base := t.TempDir()
		tree := filepath.Join(base, "tree")
		gitdir := filepath.Join(base, "sep.git")
		if out, err := gitCmd(base, "init", "-q", "--separate-git-dir", gitdir, tree).CombinedOutput(); err != nil {
			t.Skipf("init --separate-git-dir failed (%v): %s", err, out)
		}

		got, err := MainRepoRoot(tree)
		if err != nil {
			t.Fatal(err)
		}
		if want := realpath(t, tree); got != want {
			t.Errorf("MainRepoRoot(separate git dir) = %s, want %s", got, want)
		}
	})

	t.Run("a bare repository is its own root", func(t *testing.T) {
		base := t.TempDir()
		bare := filepath.Join(base, "bare.git")
		if out, err := gitCmd(base, "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
			t.Skipf("init --bare failed (%v): %s", err, out)
		}

		got, err := MainRepoRoot(bare)
		if err != nil {
			t.Fatal(err)
		}
		// No working tree to name, so the repository is the answer — and the
		// second rev-parse must not turn its failure into one.
		if want := realpath(t, bare); got != want {
			t.Errorf("MainRepoRoot(bare) = %s, want %s", got, want)
		}
	})
}

func realpath(t *testing.T, dir string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
