package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A bundle comes from the guest, so it is whatever a compromised agent made
// it: bring-back and checkpoints fetch from one only when it verifies against
// this repository and carries exactly the ref asked for, and then only into
// refs/sandbox/. Nothing else in the repository moves, whatever the bundle
// says. Served here by a hostile API, standing in for a guest that lies.
func TestBundlesFromTheGuestAreCheckedBeforeFetching(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	host := t.TempDir()
	g := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@x", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	g(host, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(host, "a.txt"), []byte("a\n"), 0o644)
	g(host, "add", "a.txt")
	g(host, "commit", "-qm", "base")
	base := g(host, "rev-parse", "HEAD")

	// The guest: a clone with work on `sandbox`, and the shapes a hostile one
	// could send instead.
	guest := t.TempDir()
	g(guest, "clone", "-q", host, ".")
	g(guest, "checkout", "-qb", "sandbox")
	os.WriteFile(filepath.Join(guest, "b.txt"), []byte("work\n"), 0o644)
	g(guest, "add", "b.txt")
	g(guest, "commit", "-qm", "work")
	g(guest, "branch", "-f", "main", "sandbox") // a "main" that would move the user's
	g(guest, "update-ref", "refs/sandbox/checkpoint", "sandbox")
	other := t.TempDir()
	g(other, "init", "-q", "-b", "sandbox")
	os.WriteFile(filepath.Join(other, "x"), []byte("x\n"), 0o644)
	g(other, "add", "x")
	g(other, "commit", "-qm", "unrelated")
	g(other, "commit", "-q", "--allow-empty", "-m", "on top")

	bundle := func(dir string, args ...string) []byte {
		p := filepath.Join(t.TempDir(), "b.bundle")
		g(dir, append([]string{"bundle", "create", "-q", p}, args...)...)
		b, _ := os.ReadFile(p)
		return b
	}
	shapes := map[string][]byte{
		"good":          bundle(guest, base+"..sandbox"),
		"two heads":     bundle(guest, base+"..sandbox", base+"..main"),
		"the wrong ref": bundle(guest, base+"..main"),
		"unrelated":     bundle(other, "HEAD~1..sandbox"),
		"checkpoint":    bundle(guest, base+"..refs/sandbox/checkpoint"),
	}
	var serve []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/run"):
			json.NewEncoder(w).Encode(api.RunResult{Stdout: []byte("deadbeef\n")})
		case strings.HasSuffix(r.URL.Path, "/workspace/bundle"):
			w.Write(serve)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	s := Session{Sandbox: "sbx_x", Repo: host, Base: base, Branch: SandboxBranch}
	before := g(host, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags")
	head := g(host, "symbolic-ref", "HEAD")
	unmoved := func(what string) {
		t.Helper()
		if got := g(host, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags"); got != before {
			t.Errorf("%s moved the repository's own refs:\n%s\nwas\n%s", what, got, before)
		}
		if g(host, "symbolic-ref", "HEAD") != head {
			t.Errorf("%s moved HEAD", what)
		}
	}

	for _, bad := range []string{"two heads", "the wrong ref", "unrelated"} {
		serve = shapes[bad]
		if ref, err := BringBack(context.Background(), c, s, "x-"+strings.ReplaceAll(bad, " ", "-")); err == nil {
			t.Errorf("bring-back fetched %q into %s", bad, ref)
		}
		unmoved(bad)
	}
	serve = shapes["the wrong ref"]
	if _, _, err := Checkpoint(context.Background(), c, s); err == nil {
		t.Error("a checkpoint carrying a branch, not the checkpoint ref, was fetched")
	}
	unmoved("a checkpoint with the wrong ref")

	serve = shapes["good"]
	ref, err := BringBack(context.Background(), c, s, "ok")
	if err != nil || ref != "refs/sandbox/ok" {
		t.Fatalf("a good bundle: %q %v", ref, err)
	}
	unmoved("a good bundle")
	serve = shapes["checkpoint"]
	if ref, fetched, err := Checkpoint(context.Background(), c, s); err != nil || !fetched || !strings.HasPrefix(ref, CheckpointRefPrefix) {
		t.Fatalf("a good checkpoint: %q %v %v", ref, fetched, err)
	}
	unmoved("a good checkpoint")
	if refs := g(host, "for-each-ref", "--format=%(refname)", "refs/sandbox"); refs != "refs/sandbox/checkpoints/sbx_x\nrefs/sandbox/ok" {
		t.Errorf("refs/sandbox holds %q", refs)
	}
}
