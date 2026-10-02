package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@x"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// runScript runs the guest's checkpoint script against dir, standing in for
// /workspace.
func runScript(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", strings.Replace(checkpointScript, "cd /workspace", "cd "+dir, 1))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("checkpoint script: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// A checkpoint captures everything in the working tree — staged, unstaged and
// untracked — while leaving what the agent is working with exactly as it was:
// its index (a half-staged change stays half-staged), its HEAD, and its list of
// branches.
func TestCheckpointLeavesTheAgentsStateAlone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "sandbox")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	gitIn(t, dir, "add", "a.txt")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	head := gitIn(t, dir, "rev-parse", "HEAD")

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644)
	gitIn(t, dir, "add", "a.txt")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("three\n"), 0o644) // staged two, working three
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("untracked\n"), 0o644)
	staged := gitIn(t, dir, "diff", "--cached")
	branches := gitIn(t, dir, "branch", "--list")

	commit := runScript(t, dir)
	if gitIn(t, dir, "rev-parse", "refs/sandbox/checkpoint") != commit {
		t.Fatalf("checkpoint ref not at %s", commit)
	}
	if got := gitIn(t, dir, "show", commit+":a.txt"); got != "three" {
		t.Errorf("checkpoint has a.txt %q, want the working tree's", got)
	}
	if got := gitIn(t, dir, "show", commit+":new.txt"); got != "untracked" {
		t.Errorf("an untracked file was not checkpointed: %q", got)
	}
	if gitIn(t, dir, "rev-parse", commit+"^") != head {
		t.Error("the checkpoint is not on the agent's HEAD")
	}
	if gitIn(t, dir, "rev-parse", "HEAD") != head || gitIn(t, dir, "diff", "--cached") != staged ||
		gitIn(t, dir, "branch", "--list") != branches {
		t.Error("the agent's HEAD, index or branches changed")
	}
	if gitIn(t, dir, "status", "--porcelain") != "MM a.txt\n?? new.txt" {
		t.Errorf("status: %q", gitIn(t, dir, "status", "--porcelain"))
	}

	if out := runScript(t, dir); out != "unchanged" {
		t.Errorf("a second checkpoint of the same tree said %q", out)
	}
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("edited\n"), 0o644)
	if out := runScript(t, dir); out == "unchanged" || out == commit {
		t.Errorf("a changed tree was not checkpointed again: %q", out)
	}
}

func TestBringBackRefusesReservedNames(t *testing.T) {
	for _, name := range []string{"fleet", "checkpoints"} {
		if _, err := BringBack(context.Background(), nil, Session{}, name); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Commits in a sandbox need an identity. The neutral one is the default; the
// repository's own is used only when asked for.
func TestIdentity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	gitIn(t, dir, "config", "user.name", "Ada Lovelace")
	gitIn(t, dir, "config", "user.email", "ada@example.com")
	if id := Identity(dir, false); id["GIT_AUTHOR_NAME"] != "sandbox" || id["GIT_COMMITTER_EMAIL"] != "sandbox@localhost" {
		t.Errorf("default identity %v", id)
	}
	if id := Identity(dir, true); id["GIT_AUTHOR_NAME"] != "Ada Lovelace" || id["GIT_COMMITTER_EMAIL"] != "ada@example.com" {
		t.Errorf("own identity %v", id)
	}
}
