package rescue

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A manifest with no recorded upload can still be fetched, because the key is
// derivable and the bucket is the authority on what it holds.
//
// Found by running the feature rather than by reading it. `recover fetch` with
// no argument *listed* the snapshot as being in the bucket — that listing builds
// its key from the repository and session ids — while `recover fetch <id>`
// answered "no longer in the repository", because the local manifest carried no
// `remote` block and the CLI refused before trying. Two halves of one command
// disagreeing about the same object.
//
// A manifest legitimately has no remote when the object was uploaded from
// another machine under the same repository id, or when the record was rebuilt
// from the bucket, and neither means there is nothing there. What is *not* given
// up is the strong check: the sha compared against is still the one this machine
// recorded, so a bundle holding somebody else's commit is still refused.
func TestFetchWorksWhenTheManifestNeverRecordedAnUpload(t *testing.T) {
	repo := initRepo(t)
	bucket, spec := newFakeBucket(t)
	ctx := context.Background()

	writeFile(t, filepath.Join(repo, "work.txt"), "the work\n")
	snap, err := Capture(repo, CaptureOptions{S3: spec})
	if err != nil {
		t.Fatal(err)
	}
	if len(bucket.keys()) != 2 {
		t.Fatalf("setup: bucket holds %v", bucket.keys())
	}

	// The object is in the bucket; this machine's record does not say so.
	sess := snap.Session
	sess.Remote = nil
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}

	// And the objects are gone locally, which is the state fetch exists for.
	git(t, repo, "update-ref", "-d", snap.Ref)
	git(t, repo, "reflog", "expire", "--expire=now", "--all")
	git(t, repo, "gc", "--prune=now", "--quiet")
	if objectExists(ctx, repo, snap.Commit) {
		t.Fatal("the commit survived gc; the test is not proving anything")
	}

	reloaded, err := Find(repo, snap.ID)
	if reloaded.Remote.Uploaded() {
		t.Fatal("setup: the manifest still records an upload")
	}
	if err == nil {
		t.Fatal("setup: the snapshot is not reported gone")
	}

	fetched := reloaded.Session
	if err := Fetch(ctx, &fetched, spec); err != nil {
		t.Fatalf("fetch with no recorded upload: %v", err)
	}
	if !objectExists(ctx, repo, snap.Commit) {
		t.Fatal("the commit did not come back")
	}

	// The sha check still applies: it came from this machine's manifest, not
	// from the bundle's companion.
	if fetched.LastSnapshot != snap.Commit {
		t.Errorf("fetched session records %s, want %s", fetched.LastSnapshot, snap.Commit)
	}
}

// And when the bucket genuinely does not have it, the message names the bucket
// and the key rather than repeating "no longer in the repository".
func TestFetchSaysWhenTheBucketDoesNotHaveIt(t *testing.T) {
	repo := initRepo(t)
	_, spec := newFakeBucket(t)

	writeFile(t, filepath.Join(repo, "work.txt"), "x\n")
	snap, err := Capture(repo, CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sess := snap.Session
	err = Fetch(context.Background(), &sess, spec)
	if err == nil {
		t.Fatal("fetching an object that was never uploaded succeeded")
	}
	if !strings.Contains(err.Error(), spec.Bucket) {
		t.Errorf("the refusal does not name the bucket: %v", err)
	}
}
