package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestClaudeHistoryMountCreatesTheProjectBucket is the regression for issue #15.
//
// The mount points the host's per-project history directory at the container's
// -workspace bucket, which is where Claude Code writes when cwd is /workspace.
// It used to be skipped when the host directory did not exist yet — and that
// made it a chicken-and-egg: the host bucket only appears once Claude Code has
// run on the *host* in this project, so a project only ever used inside the
// sandbox never had one, and every one of its sessions landed in the persisted
// HOME's shared -workspace bucket instead, attributable to nothing.
func TestClaudeHistoryMountCreatesTheProjectBucket(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()

	rf := &runFlags{project: project}
	src, target, ok := claudeHistoryMount(rf)
	if !ok {
		t.Fatal("no history mount for a project with no host history — this is issue #15")
	}
	want := filepath.Join(home, ".claude", "projects", claudeProjectBucket(project))
	if src != want {
		t.Errorf("src = %q, want %q", src, want)
	}
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		t.Errorf("host bucket was not created: %v", err)
	}
	// The container side must stay the bucket Claude Code will actually write to.
	if target != "/sandbox/home/.claude/projects/-workspace" {
		t.Errorf("target = %q, want the -workspace bucket", target)
	}
}

// A file sitting where the bucket should be is left alone rather than clobbered:
// this reaches into the user's own ~/.claude, so the only thing it may do there
// is create the one directory it needs.
func TestClaudeHistoryMountDoesNotClobberANonDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()

	p := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(p, claudeProjectBucket(project))
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := claudeHistoryMount(rfFor(project)); ok {
		t.Error("mounted over a plain file in the user's ~/.claude")
	}
	b, err := os.ReadFile(blocker)
	if err != nil || string(b) != "not a directory" {
		t.Errorf("the existing file was modified: %q, %v", b, err)
	}
}

func rfFor(project string) *runFlags { return &runFlags{project: project} }
