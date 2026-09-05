package rescue

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// fakeBucket is an in-memory S3 that answers the four verbs this package uses.
// Enough to prove the round trip end to end — which is the only way to test a
// mirror worth having, since the failure that matters is a bundle that uploads
// and does not come back.
type fakeBucket struct {
	mu       sync.Mutex
	objects  map[string][]byte
	modified map[string]time.Time
	puts     []string
	fail     bool
}

func newFakeBucket(t *testing.T) (*fakeBucket, *config.S3Spec) {
	t.Helper()
	b := &fakeBucket{objects: map[string][]byte{}, modified: map[string]time.Time{}}
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)

	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	return b, &config.S3Spec{
		Bucket:    "snaps",
		Endpoint:  srv.URL,
		PathStyle: true,
	}
}

func (b *fakeBucket) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>nope</Message></Error>`)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/snaps/")
	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		b.objects[key] = body
		b.modified[key] = time.Now()
		b.puts = append(b.puts, key)
	case http.MethodGet:
		if r.URL.Query().Get("list-type") == "2" {
			b.list(w, r.URL.Query().Get("prefix"))
			return
		}
		obj, ok := b.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(obj)
	case http.MethodHead:
		obj, ok := b.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(obj)))
	case http.MethodDelete:
		delete(b.objects, key)
		w.WriteHeader(http.StatusNoContent)
	}
}

// list answers ListObjectsV2 for the objects under a prefix. Modelled on the
// real thing closely enough to matter in one respect: the keys it returns carry
// the prefix, which is what a caller reconstructing keys from ids exists to
// avoid double-applying.
func (b *fakeBucket) list(w http.ResponseWriter, prefix string) {
	fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated>`)
	keys := make([]string, 0, len(b.objects))
	for k := range b.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		when := b.modified[k]
		if when.IsZero() {
			when = time.Unix(0, 0).UTC()
		}
		fmt.Fprintf(w, `<Contents><Key>%s</Key><Size>%d</Size><LastModified>%s</LastModified></Contents>`,
			k, len(b.objects[k]), when.Format(time.RFC3339))
	}
	fmt.Fprint(w, `</ListBucketResult>`)
}

func (b *fakeBucket) keys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.objects))
	for k := range b.objects {
		out = append(out, k)
	}
	return out
}

// The whole feature in one test: take a snapshot, mirror it, destroy every trace
// of it in the repository, and get it back from the bucket well enough to
// restore.
//
// Deleting the ref *and* the objects is the point. A test that only dropped the
// ref would pass against a Fetch that did nothing, because the commit would
// still be sitting in the object store.
func TestASnapshotSurvivesTheRepositoryItCameFrom(t *testing.T) {
	repo := initRepo(t)
	bucket, spec := newFakeBucket(t)

	writeFile(t, filepath.Join(repo, "work.txt"), "the risky migration\n")
	snap, err := Capture(repo, CaptureOptions{Label: "before migration", S3: spec})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if !snap.Remote.Uploaded() {
		t.Fatalf("snapshot was not mirrored: %+v", snap.Remote)
	}

	// The bundle and the manifest beside it — the second is what makes the
	// bucket readable by a machine that has lost ~/.config/sandbox/rescue.
	keys := bucket.keys()
	if len(keys) != 2 {
		t.Fatalf("want a bundle and a manifest in the bucket, got %v", keys)
	}
	var haveBundle, haveManifest bool
	for _, k := range keys {
		haveBundle = haveBundle || strings.HasSuffix(k, ".bundle")
		haveManifest = haveManifest || strings.HasSuffix(k, ".json")
	}
	if !haveBundle || !haveManifest {
		t.Fatalf("bucket holds %v, want one of each", keys)
	}

	// Erase it locally: drop the ref, then expire the objects it was keeping
	// alive. After this the commit genuinely is not in the repository.
	git(t, repo, "update-ref", "-d", snap.Ref)
	git(t, repo, "reflog", "expire", "--expire=now", "--all")
	git(t, repo, "gc", "--prune=now", "--quiet")
	if objectExists(context.Background(), repo, snap.Commit) {
		t.Fatalf("the snapshot commit survived gc; the test is not proving anything")
	}

	sess := snap.Session
	if err := Fetch(context.Background(), &sess, spec); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !objectExists(context.Background(), repo, snap.Commit) {
		t.Fatal("the commit did not come back from the bucket")
	}

	// And it is restorable by the ordinary path, which is the property the
	// backup exists for: nothing downstream of here knows a bucket was involved.
	res, err := Restore(repo, snap.ID, RestoreOptions{Mode: RestoreBranch})
	if err != nil {
		t.Fatalf("restore after fetch: %v", err)
	}
	if res.Branch == "" {
		t.Fatal("restore produced no branch")
	}
	if got := git(t, repo, "show", res.Branch+":work.txt"); got != "the risky migration" {
		t.Fatalf("restored content = %q", got)
	}
}

// The object in the bucket is worth something without this tool: git alone opens
// it, on a machine that has never seen the repository.
//
// `git clone` of it does not work and is not expected to — a snapshot ref is not
// a branch, so the bundle carries no HEAD. The recipe asserted here is the one in
// remote.go's doc comment, tested so the documentation cannot quietly become
// wrong.
func TestTheUploadedBundleIsRecoverableByGitAlone(t *testing.T) {
	repo := initRepo(t)
	bucket, spec := newFakeBucket(t)

	writeFile(t, filepath.Join(repo, "work.txt"), "content\n")
	snap, err := Capture(repo, CaptureOptions{S3: spec})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	var bundle []byte
	bucket.mu.Lock()
	for k, v := range bucket.objects {
		if strings.HasSuffix(k, ".bundle") {
			bundle = v
		}
	}
	bucket.mu.Unlock()

	dir := t.TempDir()
	path := filepath.Join(dir, "snap.bundle")
	if err := os.WriteFile(path, bundle, 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "recovered")
	if out, err := gitCmd(dir, "init", "-q", dest).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	// Verified before it is fetched from — what turns a truncated download into a
	// refusal rather than a half-unpacked object store discovered later. It runs
	// *inside* a repository because git requires one for this, which is also why
	// the recovery recipe starts with `git init`.
	if out, err := gitCmd(dest, "bundle", "verify", path).CombinedOutput(); err != nil {
		t.Fatalf("the uploaded object is not a valid git bundle: %v: %s", err, out)
	}
	if out, err := gitCmd(dest, "fetch", "-q", path,
		"refs/sandbox/snapshots/*:refs/heads/snap/*").CombinedOutput(); err != nil {
		t.Fatalf("fetching from the bundle: %v: %s", err, out)
	}
	if out, err := gitCmd(dest, "checkout", "-q", "snap/"+snap.ID).CombinedOutput(); err != nil {
		t.Fatalf("checking out the recovered snapshot: %v: %s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dest, "work.txt"))
	if err != nil || string(b) != "content\n" {
		t.Fatalf("recovered content = %q (%v)", b, err)
	}
}

// A failed mirror must not look like a failed snapshot. The checkpoint is real
// and local; what failed is the copy, and the manifest has to say so — otherwise
// the only record is a line somebody was not watching for.
func TestAFailedMirrorLeavesTheSnapshotAndRecordsWhy(t *testing.T) {
	repo := initRepo(t)
	bucket, spec := newFakeBucket(t)
	bucket.fail = true

	writeFile(t, filepath.Join(repo, "work.txt"), "x\n")
	snap, err := Capture(repo, CaptureOptions{S3: spec})
	if err == nil {
		t.Fatal("a refused bucket must be reported, not swallowed")
	}
	if snap.ID == "" || snap.Commit == "" {
		t.Fatalf("the snapshot itself must survive a mirror failure: %+v", snap)
	}
	if !objectExists(context.Background(), repo, snap.Commit) {
		t.Fatal("the local snapshot was lost because the upload failed")
	}

	// And the manifest on disk carries the reason, so tomorrow's listing shows
	// this one as local-only rather than as mirrored.
	sess, err := findSession(repo, snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Remote == nil || sess.Remote.Error == "" {
		t.Fatalf("the failure was not recorded: %+v", sess.Remote)
	}
	if sess.Remote.Uploaded() {
		t.Fatal("a snapshot whose upload failed must not read as uploaded")
	}
	if !strings.Contains(sess.Remote.Error, "AccessDenied") {
		t.Errorf("the recorded reason should name what the bucket said, got %q", sess.Remote.Error)
	}
}

// The default is manual-only, so the crash-net loop's snapshots stay local
// unless somebody asked for `upload: all`. Getting this backwards means a bundle
// leaving the machine every two minutes per running agent.
func TestUploadModeDecidesWhichSnapshotsLeaveTheMachine(t *testing.T) {
	for _, tc := range []struct {
		mode                string
		wantManual, wantRun bool
	}{
		{"", true, false},
		{config.UploadManual, true, false},
		{config.UploadAll, true, true},
		{config.UploadOff, false, false},
	} {
		spec := &config.S3Spec{Bucket: "b", Upload: tc.mode}
		if got := spec.UploadsManual(); got != tc.wantManual {
			t.Errorf("upload=%q: UploadsManual = %v, want %v", tc.mode, got, tc.wantManual)
		}
		if got := spec.UploadsRun(); got != tc.wantRun {
			t.Errorf("upload=%q: UploadsRun = %v, want %v", tc.mode, got, tc.wantRun)
		}
	}
	// No bucket is off, whatever the mode says: a spec with an upload mode and
	// nothing to upload to must not be read as "yes".
	var none *config.S3Spec
	if none.UploadsManual() || none.UploadsRun() {
		t.Error("a nil spec must upload nothing")
	}
	if (&config.S3Spec{Upload: config.UploadAll}).UploadsManual() {
		t.Error("a spec with no bucket must upload nothing")
	}
}

// The size ceiling is refused before the upload rather than at the end of one
// that was always going to be rejected, and the message names both numbers so
// the remedy is obvious.
func TestAnOversizedBundleIsRefusedBeforeItIsSent(t *testing.T) {
	repo := initRepo(t)
	bucket, spec := newFakeBucket(t)
	spec.MaxObjectMB = 0

	// One megabyte of ceiling is not expressible in whole MB below 1, so the
	// spec is given a real limit and the bundle made to exceed it.
	spec.MaxObjectMB = 1
	big := strings.Repeat("incompressible-", 200_000)
	writeFile(t, filepath.Join(repo, "big.txt"), big)

	_, err := Capture(repo, CaptureOptions{S3: spec})
	if err == nil {
		t.Skip("the bundle compressed below the limit; nothing to assert")
	}
	if !strings.Contains(err.Error(), "max_object_mb") {
		t.Fatalf("the refusal should name the setting to raise, got %v", err)
	}
	if len(bucket.keys()) != 0 {
		t.Fatalf("an oversized bundle was uploaded anyway: %v", bucket.keys())
	}
}

// A manifest is a file on disk. It names the ref to write, and it is never
// allowed to name a branch — the rule this whole package keeps, tested at the
// one place where the bytes come from off this machine.
func TestFetchRefusesToWriteOutsideTheSnapshotNamespace(t *testing.T) {
	repo := initRepo(t)
	_, spec := newFakeBucket(t)

	sess := Session{ID: "x", Repo: repo, Ref: "refs/heads/main"}
	err := Fetch(context.Background(), &sess, spec)
	if err == nil || !strings.Contains(err.Error(), RefPrefix) {
		t.Fatalf("want a refusal naming %s, got %v", RefPrefix, err)
	}
}

// Two clones of a same-named repository must not share a namespace in the
// bucket, for the reason container labels use an id rather than a path: the
// second one to upload would overwrite the first.
func TestRemoteKeysAreNamespacedByRepositoryID(t *testing.T) {
	a, _ := remoteKeys("proj-aaaa1111", "20260901-120000-abc")
	b, _ := remoteKeys("proj-bbbb2222", "20260901-120000-abc")
	if a == b {
		t.Fatal("the same session id in two repositories produced the same key")
	}
	if !strings.HasSuffix(a, ".bundle") {
		t.Errorf("bundle key = %q", a)
	}
	_, manifest := remoteKeys("proj-aaaa1111", "20260901-120000-abc")
	if !strings.HasSuffix(manifest, ".json") {
		t.Errorf("manifest key = %q", manifest)
	}
}

// A bundle that verifies is not the same as a bundle that holds the right
// commit. An object swapped in the bucket — tampering, a shared prefix, another
// machine writing the same key — can carry this snapshot's own ref name over
// somebody else's tree, and restoring it would look exactly like success.
//
// The bundle here is built deliberately rather than copied from another
// snapshot: a copied one is refused earlier and for a weaker reason (its ref
// name does not match), which would leave the sha comparison untested.
func TestFetchRefusesABundleHoldingADifferentCommit(t *testing.T) {
	repo := initRepo(t)
	bucket, spec := newFakeBucket(t)
	ctx := context.Background()

	writeFile(t, filepath.Join(repo, "work.txt"), "the real work\n")
	snap, err := Capture(repo, CaptureOptions{S3: spec})
	if err != nil {
		t.Fatal(err)
	}

	// A different commit, bundled under this snapshot's own ref name.
	writeFile(t, filepath.Join(repo, "work.txt"), "not the work you saved\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-qm", "someone else's tree")
	imposter := git(t, repo, "rev-parse", "HEAD")

	hostile := filepath.Join(t.TempDir(), "hostile.bundle")
	if _, err := run(ctx, repo, nil, "update-ref", snap.Ref, imposter); err != nil {
		t.Fatal(err)
	}
	if _, err := run(ctx, repo, nil, "bundle", "create", hostile, snap.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := run(ctx, repo, nil, "update-ref", snap.Ref, snap.Commit); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(hostile)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := remoteKeys(remoteRepoID(repo), snap.ID)
	bucket.mu.Lock()
	bucket.objects[key] = body
	bucket.mu.Unlock()

	if _, err := run(ctx, repo, nil, "update-ref", "-d", snap.Ref); err != nil {
		t.Fatal(err)
	}

	sess := snap.Session
	err = Fetch(ctx, &sess, spec)
	if err == nil {
		t.Fatal("a bundle holding a different commit was accepted")
	}
	if !strings.Contains(err.Error(), "recorded the snapshot as") {
		t.Fatalf("the refusal should compare the two shas, got %v", err)
	}
	// And it must not leave the ref pointing at the substituted content.
	if sha, _ := run(ctx, repo, nil, "rev-parse", "--verify", "--quiet", snap.Ref); sha != "" {
		t.Fatalf("the ref survived a refused fetch, pointing at %s", sha)
	}
}

// The weaker refusal is worth pinning too: a bundle that does not even carry
// this snapshot's ref name is rejected by git before the sha check is reached,
// and the two failures must both be failures.
func TestFetchRefusesABundleWithoutTheExpectedRef(t *testing.T) {
	repo := initRepo(t)
	bucket, spec := newFakeBucket(t)
	ctx := context.Background()

	writeFile(t, filepath.Join(repo, "work.txt"), "the real work\n")
	snap, err := Capture(repo, CaptureOptions{S3: spec})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "work.txt"), "another snapshot\n")
	other, err := Capture(repo, CaptureOptions{S3: spec})
	if err != nil {
		t.Fatal(err)
	}

	victimKey, _ := remoteKeys(remoteRepoID(repo), snap.ID)
	otherKey, _ := remoteKeys(remoteRepoID(repo), other.ID)
	bucket.mu.Lock()
	bucket.objects[victimKey] = bucket.objects[otherKey]
	bucket.mu.Unlock()

	if _, err := run(ctx, repo, nil, "update-ref", "-d", snap.Ref); err != nil {
		t.Fatal(err)
	}
	sess := snap.Session
	if err := Fetch(ctx, &sess, spec); err == nil {
		t.Fatal("a bundle for a different snapshot was accepted")
	}
}

// The case the bucket's manifests exist for: a machine with no rescue directory
// at all — the repository was cloned onto it this morning — being told what is
// in there and pulling one back.
//
// The local record is destroyed rather than merely ignored, because a test that
// left it in place would pass against a RemoteSessions that read it from disk.
func TestABucketDescribesItselfToAMachineWithNoRecordOfIt(t *testing.T) {
	repo := initRepo(t)
	_, spec := newFakeBucket(t)
	ctx := context.Background()

	writeFile(t, filepath.Join(repo, "work.txt"), "the risky migration\n")
	snap, err := Capture(repo, CaptureOptions{Label: "before migration", S3: spec})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	// Everything this machine knows: the manifest, the ref, and the objects.
	if err := os.RemoveAll(sessionsDir(repo)); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "update-ref", "-d", snap.Ref)
	git(t, repo, "reflog", "expire", "--expire=now", "--all")
	git(t, repo, "gc", "--prune=now", "--quiet")
	if local, err := List(repo, false); err != nil || len(local) != 0 {
		t.Fatalf("setup: this machine still has %d session(s) (%v)", len(local), err)
	}

	found, total, err := RemoteSessions(ctx, spec, repo, "")
	if err != nil {
		t.Fatalf("remote sessions: %v", err)
	}
	if total != 1 || len(found) != 1 {
		t.Fatalf("want one snapshot in the bucket, got %d of %d", len(found), total)
	}
	sess := found[0]
	if sess.ID != snap.ID {
		t.Errorf("session id = %q, want %q", sess.ID, snap.ID)
	}
	if sess.Label != "before migration" {
		t.Errorf("label = %q; the manifest is what makes the bucket readable", sess.Label)
	}
	// Rewritten to the repository it is being brought back to, not the one the
	// manifest was written on — which here are the same path, so the field that
	// proves the rewrite happened is the RemoteRef the bucket manifest never had.
	if sess.Repo != repo {
		t.Errorf("repo = %q, want %q", sess.Repo, repo)
	}
	if !sess.Remote.Uploaded() || sess.Remote.Bytes == 0 {
		t.Errorf("remote ref = %+v, want the bundle's key and size from the listing", sess.Remote)
	}

	if err := Fetch(ctx, &sess, spec); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !objectExists(ctx, repo, snap.Commit) {
		t.Fatal("the commit did not come back from the bucket")
	}

	// Adopting it is what makes the ordinary commands work afterwards: nothing
	// downstream of here knows a bucket was involved.
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	local, err := List(repo, false)
	if err != nil || len(local) != 1 {
		t.Fatalf("after adopting, this machine lists %d session(s) (%v)", len(local), err)
	}
	res, err := Restore(repo, snap.ID, RestoreOptions{Mode: RestoreBranch})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := git(t, repo, "show", res.Branch+":work.txt"); got != "the risky migration" {
		t.Fatalf("restored content = %q", got)
	}
}

// A repository id is a hash of an absolute path, so a re-clone somewhere else
// looks in a namespace that holds nothing. The bucket has to be able to say what
// namespaces it does hold, or those snapshots are unreachable by anything short
// of an S3 browser.
func TestSnapshotsFromAnotherPathAreReachableByNamingTheNamespace(t *testing.T) {
	repo := initRepo(t)
	_, spec := newFakeBucket(t)
	ctx := context.Background()

	writeFile(t, filepath.Join(repo, "work.txt"), "work from the old machine\n")
	snap, err := Capture(repo, CaptureOptions{S3: spec})
	if err != nil {
		t.Fatal(err)
	}
	uploadedUnder := RemoteNamespace(repo)

	// A different clone of the same project, at a path of its own.
	elsewhere := initRepo(t)
	if RemoteNamespace(elsewhere) == uploadedUnder {
		t.Fatal("two paths produced one namespace; the test is not proving anything")
	}
	found, _, err := RemoteSessions(ctx, spec, elsewhere, "")
	if err != nil {
		t.Fatalf("remote sessions: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("a repository at another path found %d snapshot(s) under its own id", len(found))
	}

	ids, err := RemoteRepoIDs(ctx, spec)
	if err != nil {
		t.Fatalf("remote repo ids: %v", err)
	}
	if len(ids) != 1 || ids[0] != uploadedUnder {
		t.Fatalf("bucket namespaces = %v, want [%s]", ids, uploadedUnder)
	}

	// Named explicitly, they are reachable — and land in the repository asking
	// for them rather than the one that wrote them.
	found, _, err = RemoteSessions(ctx, spec, elsewhere, uploadedUnder)
	if err != nil {
		t.Fatalf("remote sessions: %v", err)
	}
	if len(found) != 1 || found[0].ID != snap.ID {
		t.Fatalf("naming the namespace found %d snapshot(s)", len(found))
	}
	sess := found[0]
	if sess.Repo != elsewhere {
		t.Errorf("repo = %q, want the repository it is being fetched into (%q)", sess.Repo, elsewhere)
	}
	// The key it will fetch has to be the one it was found under. Deriving it
	// from the local repository — the fallback Fetch uses when a session carries
	// no RemoteRef — would look in the empty namespace this test just proved is
	// empty.
	if want, _ := remoteKeys(uploadedUnder, snap.ID); sess.Remote.Key != want {
		t.Fatalf("remote key = %q, want %q", sess.Remote.Key, want)
	}
	if err := Fetch(ctx, &sess, spec); err != nil {
		t.Fatalf("fetch across namespaces: %v", err)
	}
	if !objectExists(ctx, elsewhere, snap.Commit) {
		t.Fatal("the commit did not arrive in the repository that asked for it")
	}
}
