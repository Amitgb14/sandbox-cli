package studioapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// bucketServer is a minimal S3 for the handler tests: it records what was PUT
// and can be told to refuse, which is the only distinction these assertions
// need.
type bucketServer struct {
	mu   sync.Mutex
	puts []string
	fail bool
}

func (b *bucketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>no</Message></Error>`)
		return
	}
	switch r.Method {
	case http.MethodPut:
		b.puts = append(b.puts, r.URL.Path)
	case http.MethodGet:
		fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
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

	rec = asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettings{
		Retention: "336h", ManualRetention: "168h",
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

	set := asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettings{
		Retention: "336h", ManualRetention: "168h",
		S3: &SnapshotS3Settings{Bucket: "keep-me", Region: "eu-west-1"},
	})
	if set.Code != http.StatusOK {
		t.Fatalf("first write = %d: %s", set.Code, set.Body)
	}

	// A second write carrying only the windows.
	set = asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettings{
		Retention: "720h", ManualRetention: "24h",
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

	asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettings{
		S3: &SnapshotS3Settings{Bucket: "b", Endpoint: "https://minio.local", Prefix: "team"},
	})
	rec := asBrowser(t, h, "POST", "/v1/snapshots/settings", SnapshotSettings{
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
