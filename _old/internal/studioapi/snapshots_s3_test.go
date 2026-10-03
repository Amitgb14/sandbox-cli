package studioapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// bucketServer is a minimal S3 for the handler tests: it records what was PUT
// and can be told to refuse, which is the only distinction these assertions
// need.
type bucketServer struct {
	mu      sync.Mutex
	puts    []string
	objects map[string][]byte
	fail    bool
}

// It stores what it is given, which most of these tests do not need and one
// does: a restore that has to fetch the bundle back is the only assertion that
// proves the objects are reachable again, and a bucket that forgets them can
// only ever prove the request was made.
func (b *bucketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>no</Message></Error>`)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/snaps/")
	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		if b.objects == nil {
			b.objects = map[string][]byte{}
		}
		b.objects[key] = body
		b.puts = append(b.puts, r.URL.Path)
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

func (b *bucketServer) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.puts)
}

// withBucket points the server's configuration at a local fake and returns it.
func withBucket(t *testing.T, s *Server) *bucketServer {
	t.Helper()
	b := &bucketServer{}
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	s.Session.Cfg.Snapshot.S3 = &config.S3Spec{
		Bucket:    "snaps",
		Endpoint:  srv.URL,
		PathStyle: true,
	}
	return b
}

// A capture with a bucket configured mirrors on the way out, and the response
// says so — a client that had to make a second call to find out whether its
// checkpoint left the machine would mostly not make it.
func TestCaptureMirrorsAndReportsIt(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	bucket := withBucket(t, s)
	h := s.Handler()

	write(t, s.Project+"/work.txt", "risky\n")
	rec := asBrowser(t, h, "POST", "/v1/snapshots", SnapshotCreateRequest{Label: "before"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body)
	}
	var got SnapshotInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Remote == nil || !got.Remote.Uploaded {
		t.Fatalf("the response does not report the mirror: %+v", got.Remote)
	}
	if got.Remote.Bucket != "snaps" || got.Remote.Key == "" {
		t.Errorf("remote = %+v, want the bucket and key it went to", got.Remote)
	}
	// The bundle and the manifest beside it.
	if n := bucket.count(); n != 2 {
		t.Errorf("%d objects uploaded, want 2 (bundle + manifest)", n)
	}
}

// A refused bucket must not lose the snapshot. It was taken, it is real, and it
// is local-only — so the id comes back with the reason attached, rather than an
// error that discards a checkpoint that exists.
func TestCaptureSurvivesARefusedBucket(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	bucket := withBucket(t, s)
	bucket.fail = true
	h := s.Handler()

	write(t, s.Project+"/work.txt", "risky\n")
	rec := asBrowser(t, h, "POST", "/v1/snapshots", SnapshotCreateRequest{Label: "before"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201 with the snapshot that was taken: %s", rec.Code, rec.Body)
	}
	var got SnapshotInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.Commit == "" {
		t.Fatalf("the snapshot itself was lost: %+v", got)
	}
	if got.Remote == nil || got.Remote.Uploaded {
		t.Fatalf("a failed upload must not read as uploaded: %+v", got.Remote)
	}
	if !strings.Contains(got.Remote.Error, "AccessDenied") {
		t.Errorf("the reason should say what the bucket said, got %q", got.Remote.Error)
	}

	// And the listing agrees, which is where somebody looks tomorrow.
	list := asBrowser(t, h, "GET", "/v1/snapshots", nil)
	var resp SnapshotListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Snapshots) == 0 || resp.Snapshots[0].Remote == nil || resp.Snapshots[0].Remote.Uploaded {
		t.Fatalf("the listing does not show this one as local-only: %+v", resp.Snapshots)
	}
}

// With no bucket configured nothing is uploaded and nothing is claimed — which
// is the default, and must not read as a failure anywhere.
func TestCaptureWithoutABucketSaysNothingAboutOne(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	h := s.Handler()

	write(t, s.Project+"/work.txt", "x\n")
	rec := asBrowser(t, h, "POST", "/v1/snapshots", SnapshotCreateRequest{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body)
	}
	var got SnapshotInfo
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Remote != nil {
		t.Fatalf("a snapshot with no bucket configured claimed a remote: %+v", got.Remote)
	}
}

// The settings endpoint reports names and never values, and answers whether the
// credential actually resolves — the only question about it a screen needs.
func TestSettingsReportCredentialNamesAndNeverValues(t *testing.T) {
	s, _ := newTestServer(t)
	withBucket(t, s)
	s.Session.Cfg.Snapshot.S3.AccessKeyEnv = "MY_KEY"
	s.Session.Cfg.Snapshot.S3.SecretKeyEnv = "MY_SECRET"
	h := s.Handler()

	// Unset: the daemon says so, and names what to set.
	rec := asBrowser(t, h, "GET", "/v1/snapshots/settings", nil)
	var got SnapshotSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.S3 == nil {
		t.Fatal("settings did not report the bucket")
	}
	if got.S3.CredentialsResolved {
		t.Error("credentials reported as resolved when the variables are unset")
	}
	if !strings.Contains(got.S3.CredentialsError, "MY_KEY") {
		t.Errorf("the error should name the variable to set, got %q", got.S3.CredentialsError)
	}

	t.Setenv("MY_KEY", "AKIAsecretlooking")
	t.Setenv("MY_SECRET", "shhh")
	rec = asBrowser(t, h, "GET", "/v1/snapshots/settings", nil)
	body := rec.Body.String()
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if !got.S3.CredentialsResolved {
		t.Error("credentials are set but reported as unresolved")
	}
	// The values themselves must not be anywhere in the response. This is the
	// assertion the whole names-not-values design exists for.
	for _, secret := range []string{"AKIAsecretlooking", "shhh"} {
		if strings.Contains(body, secret) {
			t.Fatalf("a credential value crossed the wire: %s", body)
		}
	}
}

// Studio's file is a layer under config.yaml, so a bucket typed by hand outranks
// this screen — and the write is refused rather than accepted and silently
// ignored at the next restart.
func TestSettingsRefuseWritingABucketConfigYamlOwns(t *testing.T) {
	s, _ := newTestServer(t)
	s.Session.Cfg.Snapshot.S3 = &config.S3Spec{Bucket: "from-config-yaml"}
	h := s.Handler()

	rec := asBrowser(t, h, "GET", "/v1/snapshots/settings", nil)
	var got SnapshotSettings
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.S3 == nil || !got.S3.ConfigManaged {
		t.Fatalf("a bucket from config.yaml must be flagged as managed: %+v", got.S3)
	}

	rec = asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettingsUpdate{
		Retention: strPtr("336h"), ManualRetention: strPtr("168h"),
		S3: &SnapshotS3Settings{Bucket: "somewhere-else"},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("write = %d, want 409 for a bucket config.yaml owns: %s", rec.Code, rec.Body)
	}
	if s.Session.Cfg.Snapshot.S3.Bucket != "from-config-yaml" {
		t.Fatalf("the running config was changed anyway: %s", s.Session.Cfg.Snapshot.S3.Bucket)
	}
}

// A settings write that only meant to change a retention window must not clear
// somebody's bucket by omitting a field it does not know about.
func TestSettingsWriteWithoutAnS3BlockLeavesTheBucketAlone(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()

	set := asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettingsUpdate{
		Retention: strPtr("336h"), ManualRetention: strPtr("168h"),
		S3: &SnapshotS3Settings{Bucket: "keep-me", Region: "eu-west-1"},
	})
	if set.Code != http.StatusOK {
		t.Fatalf("first write = %d: %s", set.Code, set.Body)
	}

	// A second write carrying only the windows.
	set = asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettingsUpdate{
		Retention: strPtr("720h"), ManualRetention: strPtr("24h"),
	})
	if set.Code != http.StatusOK {
		t.Fatalf("second write = %d: %s", set.Code, set.Body)
	}
	var got SnapshotSettings
	json.Unmarshal(set.Body.Bytes(), &got)
	if got.S3 == nil || got.S3.Bucket != "keep-me" {
		t.Fatalf("the bucket was cleared by a write that never mentioned it: %+v", got.S3)
	}
	// Reported as the resolved duration ("720h0m0s") rather than echoed back:
	// this field is what is in force, not what was typed.
	if got.Retention != "720h0m0s" {
		t.Errorf("retention = %q, want the value just written, resolved", got.Retention)
	}
}

// Clearing the bucket is how mirroring is turned off, and it must not leave an
// endpoint and a prefix behind pointing at nothing.
func TestAnEmptyBucketTurnsMirroringOff(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()

	asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettingsUpdate{
		S3: &SnapshotS3Settings{Bucket: "b", Endpoint: "https://minio.local", Prefix: "team"},
	})
	rec := asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettingsUpdate{
		S3: &SnapshotS3Settings{Bucket: ""},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear = %d: %s", rec.Code, rec.Body)
	}
	var got SnapshotSettings
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.S3 != nil {
		t.Fatalf("clearing the bucket left configuration behind: %+v", got.S3)
	}
}

// The check endpoint asks about what is configured on the daemon, never about
// what the caller sent — a Test button that dialled a host from the request body
// would be a server-side request forgery with a friendly label on it.
func TestCheckUsesTheConfiguredBucketAndNotTheRequest(t *testing.T) {
	s, _ := newTestServer(t)
	bucket := withBucket(t, s)
	h := s.Handler()

	rec := asBrowser(t, h, "POST", "/v1/snapshots/s3/check", map[string]string{
		"bucket":   "attacker-bucket",
		"endpoint": "http://169.254.169.254",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("check = %d: %s", rec.Code, rec.Body)
	}
	var got SnapshotS3CheckResponse
	json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.Ok {
		t.Fatalf("check failed against the configured bucket: %+v", got)
	}
	if got.Bucket != "snaps" {
		t.Errorf("check reported bucket %q, want the configured one", got.Bucket)
	}
	if bucket.count() != 0 {
		t.Error("a connectivity check wrote to the bucket; it must only read")
	}
}

// A bucket that refuses is a 200 with ok:false, not a 4xx: the request was well
// formed and the daemon answered it correctly. What failed is the bucket, which
// is the result being asked for.
func TestCheckReportsAFailureAsAResultNotAnError(t *testing.T) {
	s, _ := newTestServer(t)
	bucket := withBucket(t, s)
	bucket.fail = true
	h := s.Handler()

	rec := asBrowser(t, h, "POST", "/v1/snapshots/s3/check", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("check = %d, want 200 carrying the failure", rec.Code)
	}
	var got SnapshotS3CheckResponse
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Ok || !strings.Contains(got.Error, "AccessDenied") {
		t.Fatalf("want ok:false with the bucket's reason, got %+v", got)
	}
}

// With no bucket at all the check says so rather than erroring — the honest
// answer to "is my storage working" when none is configured.
func TestCheckWithNoBucketSaysSo(t *testing.T) {
	s, _ := newTestServer(t)
	rec := asBrowser(t, s.Handler(), "POST", "/v1/snapshots/s3/check", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("check = %d", rec.Code)
	}
	var got SnapshotS3CheckResponse
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Ok || got.Error == "" {
		t.Fatalf("want a stated absence, got %+v", got)
	}
}

// Uploading after the fact is what makes a failed mirror recoverable, and a
// snapshot taken before a bucket existed mirrorable at all.
func TestUploadMirrorsAnExistingSnapshot(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	h := s.Handler()

	write(t, s.Project+"/work.txt", "x\n")
	rec := asBrowser(t, h, "POST", "/v1/snapshots", SnapshotCreateRequest{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body)
	}
	var snap SnapshotInfo
	json.Unmarshal(rec.Body.Bytes(), &snap)
	if snap.Remote != nil {
		t.Fatalf("no bucket was configured yet: %+v", snap.Remote)
	}

	// The bucket arrives afterwards.
	bucket := withBucket(t, s)
	rec = asBrowser(t, h, "POST", "/v1/snapshots/"+snap.ID+"/upload", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body)
	}
	var got SnapshotInfo
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Remote == nil || !got.Remote.Uploaded {
		t.Fatalf("upload did not report a mirror: %+v", got.Remote)
	}
	if bucket.count() != 2 {
		t.Errorf("%d objects uploaded, want 2", bucket.count())
	}
}

// Uploading with nothing configured is the caller's mistake and says which
// setting is missing, rather than failing somewhere inside the S3 client.
func TestUploadWithoutABucketNamesTheSetting(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	rec := asBrowser(t, s.Handler(), "POST", "/v1/snapshots/anything/upload", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("upload = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "snapshot.s3.bucket") {
		t.Errorf("the refusal should name the setting, got %s", rec.Body)
	}
}

// strPtr is the write type's "this field was sent" — see SnapshotSettingsUpdate,
// where absence is a distinct request from an empty string.
func strPtr(v string) *string { return &v }

// The whole point of mirroring, over HTTP: a snapshot whose objects are gone
// from the repository is restored by fetching the bundle back, not refused.
//
// This is a regression test for a defect that made the entire S3 restore path
// unreachable. rescue.Find returns its error *and* a populated snapshot when the
// objects have been collected, and this handler read only the error — so the
// branch below it, the one that fetches, could never run, and a snapshot with a
// perfectly good copy in the bucket answered 404.
func TestRestoreFetchesASnapshotThatIsOnlyInTheBucket(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	withBucket(t, s)
	h := s.Handler()

	write(t, s.Project+"/work.txt", "the risky migration\n")
	rec := asBrowser(t, h, "POST", "/v1/snapshots", SnapshotCreateRequest{Label: "before"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body)
	}
	var snap SnapshotInfo
	json.Unmarshal(rec.Body.Bytes(), &snap)
	if snap.Remote == nil || !snap.Remote.Uploaded {
		t.Fatalf("the snapshot was not mirrored: %+v", snap.Remote)
	}

	// Erase it locally: the ref, then the objects it was keeping alive. Only the
	// manifest and the copy in the bucket are left.
	gitIn(t, s.Project, "update-ref", "-d", "refs/sandbox/snapshots/"+snap.ID)
	gitIn(t, s.Project, "reflog", "expire", "--expire=now", "--all")
	gitIn(t, s.Project, "gc", "--prune=now", "--quiet")

	rec = asBrowser(t, h, "POST", "/v1/snapshots/"+snap.ID+"/restore", SnapshotRestoreRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore of a bucket-only snapshot = %d: %s", rec.Code, rec.Body)
	}
	var res RunRecoverResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Branch == "" {
		t.Fatalf("restore produced no branch: %s", rec.Body)
	}
	if out := gitIn(t, s.Project, "show", res.Branch+":work.txt"); !strings.Contains(out, "the risky migration") {
		t.Fatalf("the restored branch does not hold the work: %q", out)
	}

	// And the manifest now says the objects are here again, so the listing does
	// not keep offering a snapshot that has already come home.
	rec = asBrowser(t, h, "GET", "/v1/snapshots", nil)
	var list SnapshotListResponse
	json.Unmarshal(rec.Body.Bytes(), &list)
	for _, got := range list.Snapshots {
		if got.ID == snap.ID && !got.Reachable {
			t.Error("after a fetch the snapshot is still listed as unreachable")
		}
	}
}

// The other half of the same distinction: gone *and* never mirrored is the one
// shape nothing can undo, and it must not be reported as a fetch failure or a
// 404 that suggests the id was wrong.
func TestRestoreOfAGoneSnapshotWithNoCopySaysWhatHappened(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	h := s.Handler()

	write(t, s.Project+"/work.txt", "x\n")
	rec := asBrowser(t, h, "POST", "/v1/snapshots", SnapshotCreateRequest{})
	var snap SnapshotInfo
	json.Unmarshal(rec.Body.Bytes(), &snap)

	gitIn(t, s.Project, "update-ref", "-d", "refs/sandbox/snapshots/"+snap.ID)
	gitIn(t, s.Project, "reflog", "expire", "--expire=now", "--all")
	gitIn(t, s.Project, "gc", "--prune=now", "--quiet")

	rec = asBrowser(t, h, "POST", "/v1/snapshots/"+snap.ID+"/restore", SnapshotRestoreRequest{})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("restore = %d, want 422: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "no longer in the repository") {
		t.Errorf("the refusal does not say what happened to it: %s", rec.Body)
	}
}

// Studio's storage screen knows nothing about retention and must not write it.
// It used to spread the read back into the write, which copied the *resolved*
// windows — config.yaml's values, or the built-in defaults — into this daemon's
// own override file, where they would outlive the line they came from.
func TestASettingsWriteLeavesTheWindowsItDidNotSendAlone(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()

	set := asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettingsUpdate{
		Retention: strPtr("720h"), ManualRetention: strPtr("48h"),
	})
	if set.Code != http.StatusOK {
		t.Fatalf("first write = %d: %s", set.Code, set.Body)
	}

	// A write about the bucket only.
	set = asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettingsUpdate{
		S3: &SnapshotS3Settings{Bucket: "somewhere"},
	})
	if set.Code != http.StatusOK {
		t.Fatalf("bucket write = %d: %s", set.Code, set.Body)
	}
	var got SnapshotSettings
	json.Unmarshal(set.Body.Bytes(), &got)
	if got.Retention != "720h0m0s" || got.ManualRetention != "48h0m0s" {
		t.Fatalf("a bucket write moved the windows: run=%q manual=%q", got.Retention, got.ManualRetention)
	}
	if s.Session.Cfg.Snapshot.Retention != "720h" {
		t.Errorf("the running config's window was rewritten: %q", s.Session.Cfg.Snapshot.Retention)
	}
}

// A window config.yaml sets outranks this screen, and the screen is told so —
// SnapshotSettings.ConfigRetention is that promise. Applying the write to the
// running daemon anyway broke it in the direction that hides: the value silently
// reverted to the built-in default for the life of the process, and the next
// read, finding nothing left to attribute to config.yaml, un-pinned the field.
func TestASettingsWriteCannotOverrideWhatConfigYamlPins(t *testing.T) {
	s, _ := newTestServer(t)
	s.Session.Cfg.Snapshot.Retention = "720h"
	h := s.Handler()

	set := asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettingsUpdate{
		Retention: strPtr(""), ManualRetention: strPtr("24h"),
	})
	if set.Code != http.StatusOK {
		t.Fatalf("write = %d: %s", set.Code, set.Body)
	}
	if s.Session.Cfg.Snapshot.Retention != "720h" {
		t.Fatalf("config.yaml's window was overwritten in the running daemon: %q", s.Session.Cfg.Snapshot.Retention)
	}
	var got SnapshotSettings
	json.Unmarshal(set.Body.Bytes(), &got)
	if got.ConfigRetention != "720h" {
		t.Errorf("the field un-pinned itself: configRetention = %q", got.ConfigRetention)
	}
	if got.ManualRetention != "24h0m0s" {
		t.Errorf("the window that was not pinned did not take: %q", got.ManualRetention)
	}
}

// The daemon must not refuse a restore on the strength of a manifest that
// records no upload, for the same reason `recover fetch` must not: `Mirror` puts
// the objects and *then* records them, so a process that dies in between leaves
// the bucket holding the snapshot and the manifest denying it.
//
// Fixing only the CLI moved the disagreement rather than closing it — from "two
// halves of one command" to "the CLI recovers it and the daemon returns 422".
func TestRestoreFetchesBackWhenTheManifestRecordsNoUpload(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	withBucket(t, s)
	h := s.Handler()

	write(t, s.Project+"/work.txt", "the work\n")
	rec := asBrowser(t, h, "POST", "/v1/snapshots", SnapshotCreateRequest{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body)
	}
	var snap SnapshotInfo
	json.Unmarshal(rec.Body.Bytes(), &snap)
	if snap.Remote == nil || !snap.Remote.Uploaded {
		t.Fatalf("setup: not mirrored: %+v", snap.Remote)
	}

	// The manifest forgets the upload — an interrupted mirror — and the objects
	// go, which is the state a restore has to survive.
	found, err := rescue.Find(s.Project, snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	sess := found.Session
	sess.Remote = nil
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	gitIn(t, s.Project, "update-ref", "-d", "refs/sandbox/snapshots/"+snap.ID)
	gitIn(t, s.Project, "reflog", "expire", "--expire=now", "--all")
	gitIn(t, s.Project, "gc", "--prune=now", "--quiet")

	rec = asBrowser(t, h, "POST", "/v1/snapshots/"+snap.ID+"/restore", SnapshotRestoreRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore = %d, want 200: %s", rec.Code, rec.Body)
	}
	var res RunRecoverResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Branch == "" {
		t.Fatalf("restore produced no branch: %s", rec.Body)
	}
	if out := gitIn(t, s.Project, "show", res.Branch+":work.txt"); !strings.Contains(out, "the work") {
		t.Errorf("the restored branch does not hold the work: %q", out)
	}
}
