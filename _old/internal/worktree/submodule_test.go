package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepoIDSeparatesASubmoduleFromItsSuperproject pins the identity half of the
// same defect `rescue.TestMainRepoRootNamesAWorkingTree` pins the path half of.
//
// `.git/modules/<name>` and `.git/worktrees/<name>` sit at the same depth, so the
// pointer-file fallback used to read a submodule as a worktree of its
// superproject: RepoID answered with the *super's* id while the resolved root was
// the submodule's tree. A registry entry then carried one repository's id and
// another's path, and container labels — which are how every later command finds
// a run — belonged to the wrong repository.
func TestRepoIDSeparatesASubmoduleFromItsSuperproject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	mod := filepath.Join(base, "mod")
	super := filepath.Join(base, "super")
	for _, dir := range []string{mod, super} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"-c", "init.templateDir=", "init", "-q", "-b", "main", "."},
			{"add", "f"},
			{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-qm", "init"},
		} {
			if out, err := gitIn(dir, args...); err != nil {
				t.Skipf("git %v failed (%v): %s", args, err, out)
			}
		}
	}
	// protocol.file.allow: git refuses local-path submodules by default since
	// CVE-2022-39253. This is a fixture on disk, not a clone of anything a
	// repository named.
	if out, err := gitIn(super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", mod, "mod"); err != nil {
		t.Skipf("submodule add failed (%v): %s", err, out)
	}

	inside := filepath.Join(super, "mod")
	if common, ok := GitCommonDir(inside); ok {
		t.Errorf("GitCommonDir(submodule) = %q, want no answer: a submodule is not a linked worktree", common)
	}

	subID, err := RepoID(inside)
	if err != nil {
		t.Fatal(err)
	}
	superID, err := RepoID(super)
	if err != nil {
		t.Fatal(err)
	}
	if subID == superID {
		t.Errorf("RepoID(submodule) = RepoID(superproject) = %s; a submodule is its own repository", subID)
	}
	if !strings.HasPrefix(subID, "mod-") {
		t.Errorf("RepoID(submodule) = %s, want an id named for the submodule", subID)
	}
}

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}
