package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/mirror/mirrortest"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@x", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func runMirror(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newMirrorCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

// The commands, end to end against a bucket in memory: push a run's work, list
// it, and fetch it back on a machine with no record of the upload; and on the
// machine that has one, a fetch is checked against it.
func TestMirrorPushLsFetch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	_, srv := mirrortest.NewServer(t)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("WORK_KEY", "AKIDEXAMPLE")
	t.Setenv("WORK_SECRET", "secret")
	os.MkdirAll(filepath.Join(home, "sandbox"), 0o700)
	os.WriteFile(filepath.Join(home, "sandbox", "config.yaml"), []byte("mirror:\n  s3:\n    bucket: b\n    endpoint: "+srv.URL+
		"\n    path_style: true\n    access_key_env: WORK_KEY\n    secret_key_env: WORK_SECRET\n"), 0o600)

	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	gitIn(t, repo, "commit", "-q", "--allow-empty", "-m", "work")
	work := gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "reset", "-q", "--hard", "HEAD~1")
	const id = "sbx_00000000000000aa"
	gitIn(t, repo, "update-ref", "refs/sandbox/"+id, work)
	s := workspace.Session{Sandbox: id, Repo: repo, BroughtBack: "refs/sandbox/" + id, Done: true}
	s.Save()

	if _, err := runMirror(t, "push", id); err != nil {
		t.Fatal(err)
	}
	s, _ = workspace.LoadSession(id)
	if len(s.Mirrored) != 1 || s.Mirrored[0].SHA != work {
		t.Fatalf("the upload was not recorded: %+v", s.Mirrored)
	}
	t.Chdir(repo)
	if out, err := runMirror(t, "ls"); err != nil || !strings.Contains(out, "bring-back") || !strings.Contains(out, work[:12]) {
		t.Errorf("ls: %v\n%s", err, out)
	}

	// This machine's record disagrees with the bucket: refused.
	s.Mirrored[0].SHA = strings.Repeat("1", 40)
	s.Save()
	if _, err := runMirror(t, "fetch", id); err == nil {
		t.Error("a fetch that disagrees with this machine's record was accepted")
	}

	// Another machine: a clone without the work, and no record of the upload.
	other := t.TempDir()
	gitIn(t, other, "clone", "-q", "--no-local", repo, ".")
	t.Setenv("XDG_CONFIG_HOME", home) // the same config: the user's own
	os.Remove(filepath.Join(home, "sandbox", "sessions", id+".json"))
	t.Chdir(other)
	out, err := runMirror(t, "fetch", id)
	if err != nil || !strings.Contains(out, "refs/sandbox/mirror/"+id) {
		t.Fatalf("fetch on another machine: %v\n%s", err, out)
	}
	if gitIn(t, other, "rev-parse", "refs/sandbox/mirror/"+id) != work {
		t.Error("the fetched ref is not the work")
	}
}

// With no mirror configured, nothing is attempted and nothing is said.
func TestNoMirrorIsQuiet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	msg, err := mirrorWork(context.Background(), nil, &workspace.Session{}, "refs/sandbox/x", "bring-back")
	if msg != "" || err != nil {
		t.Errorf("%q %v", msg, err)
	}
	if spec, err := mirrorSpecFor(t.TempDir()); spec != nil || err != nil {
		t.Errorf("%+v %v", spec, err)
	}
}
