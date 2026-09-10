package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// bucket is the smallest S3 that `recover fetch` can talk to: put, get, head and
// a list. Its own copy rather than internal/rescue's, which is in a _test.go of
// another package and so cannot be imported — the alternative is no behavioural
// test of this path at all, which is what the first version of this fix shipped
// with.
type bucket struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (b *bucket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/snaps/")
	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		b.objects[key] = body
	case http.MethodHead:
		obj, ok := b.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(obj)))
	case http.MethodGet:
		if r.URL.Query().Get("list-type") == "2" {
			fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
			return
		}
		obj, ok := b.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(obj)
	}
}

func fakeBucket(t *testing.T) (*bucket, *config.S3Spec) {
	t.Helper()
	b := &bucket{objects: map[string][]byte{}}
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	return b, &config.S3Spec{Bucket: "snaps", Endpoint: srv.URL, PathStyle: true}
}

func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// `recover fetch ID` must not decide from the local manifest that there is
// nothing to fetch.
//
// This is the test the first version of this fix did not have, and its absence
// was the finding: both new tests lived in `package rescue` and exercised
// `rescue.Fetch`, which already handled a manifest with no remote. Restoring the
// guard in this file therefore left the whole suite green — so the fix had no
// regression guard at all, and the next person to re-add it as an optimisation
// would have seen nothing fail.
//
// The state it builds is the one that produces the bug in the wild: `Mirror`
// puts the bundle, puts the manifest, and *then* records the upload, so a
// process that dies in between leaves the object in the bucket and the manifest
// without it.
func TestFetchByIDIgnoresAManifestThatRecordsNoUpload(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := t.TempDir()
	b, spec := fakeBucket(t)
	ctx := context.Background()

	gitAt(t, repo, "init", "-q", "-b", "main", ".")
	gitAt(t, repo, "config", "user.email", "t@e.st")
	gitAt(t, repo, "config", "user.name", "t")
	write(t, filepath.Join(repo, "a.txt"), "one\n")
	gitAt(t, repo, "add", "-A")
	gitAt(t, repo, "commit", "-qm", "init")

	write(t, filepath.Join(repo, "a.txt"), "the uncommitted work\n")
	snap, err := rescue.Capture(repo, rescue.CaptureOptions{S3: spec})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(b.objects) == 0 {
		t.Fatal("setup: nothing reached the bucket")
	}

	// The manifest forgets the upload, as an interrupted Mirror leaves it.
	sess := snap.Session
	sess.Remote = nil
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}

	// And the objects go, which is the state fetch exists for.
	gitAt(t, repo, "update-ref", "-d", snap.Ref)
	gitAt(t, repo, "reflog", "expire", "--expire=now", "--all")
	gitAt(t, repo, "gc", "--prune=now", "--quiet")

	if err := fetchRemoteSnapshot(ctx, spec, repo, repo, "", snap.ID); err != nil {
		t.Fatalf("fetch by id with no recorded upload: %v", err)
	}

	// The commit is back, and the manifest now says where it came from rather
	// than continuing to claim the snapshot never left the machine.
	if gitAt(t, repo, "rev-parse", "--verify", "--quiet", snap.Ref) != snap.Commit {
		t.Error("the snapshot ref does not point at the fetched commit")
	}
	back, err := rescue.Find(repo, snap.ID)
	if err != nil {
		t.Fatalf("after fetching, the snapshot is still not usable: %v", err)
	}
	if !back.Remote.Uploaded() {
		t.Errorf("the manifest still records no upload after a successful fetch: %+v", back.Remote)
	}
	if back.Remote.Bucket != spec.Bucket {
		t.Errorf("recorded bucket = %q, want %q", back.Remote.Bucket, spec.Bucket)
	}
}

// A snapshot that is genuinely not in the bucket still fails, and names the
// bucket — so "try anyway" did not turn a clear refusal into a vague one.
func TestFetchByIDStillFailsWhenTheBucketIsEmpty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := t.TempDir()
	_, spec := fakeBucket(t)

	gitAt(t, repo, "init", "-q", "-b", "main", ".")
	gitAt(t, repo, "config", "user.email", "t@e.st")
	gitAt(t, repo, "config", "user.name", "t")
	write(t, filepath.Join(repo, "a.txt"), "one\n")
	gitAt(t, repo, "add", "-A")
	gitAt(t, repo, "commit", "-qm", "init")

	write(t, filepath.Join(repo, "a.txt"), "work\n")
	snap, err := rescue.Capture(repo, rescue.CaptureOptions{}) // no mirror
	if err != nil {
		t.Fatal(err)
	}
	gitAt(t, repo, "update-ref", "-d", snap.Ref)
	gitAt(t, repo, "reflog", "expire", "--expire=now", "--all")
	gitAt(t, repo, "gc", "--prune=now", "--quiet")

	err = fetchRemoteSnapshot(context.Background(), spec, repo, repo, "", snap.ID)
	if err == nil {
		t.Fatal("fetching an object that was never uploaded succeeded")
	}
	if !strings.Contains(err.Error(), spec.Bucket) {
		t.Errorf("the refusal does not name the bucket: %v", err)
	}
}

// write puts a file on disk, creating its directory.
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
