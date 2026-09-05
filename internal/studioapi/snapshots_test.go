package studioapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// snapshotRepo turns the test server's project directory into a git repository
// with one commit, which is what every snapshot assertion needs underneath it.
func snapshotRepo(t *testing.T, s *Server) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	write(t, filepath.Join(s.Project, "tracked.txt"), "hello\n")
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-qm", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = s.Project
		cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// asBrowser replays a request with an Origin header, which is what makes it
// Studio rather than a program as far as this API is concerned.
func asBrowser(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := newTestRequest(t, method, path, body)
	req.Header.Set("Origin", "http://"+testHost)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The feature in one test: capture, find it in the listing, put it back.
func TestSnapshotCaptureListRestore(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	h := s.Handler()

	write(t, filepath.Join(s.Project, "work.txt"), "in progress\n")
	rec := doRequest(t, h, http.MethodPost, "/v1/snapshots", SnapshotCreateRequest{Label: "before the refactor"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	created := decodeBody[SnapshotInfo](t, rec)
	if created.ID == "" || created.Commit == "" {
		t.Fatalf("create returned nothing usable: %+v", created)
	}
	if created.Label != "before the refactor" {
		t.Errorf("label = %q", created.Label)
	}
	if created.RepoID != s.RepoID {
		t.Errorf("repoId = %q, want %q", created.RepoID, s.RepoID)
	}
	// A request with no Origin is a program, so the snapshot is the SDK's.
	if created.Source != SnapshotSourceSDK {
		t.Errorf("source = %q, want %q", created.Source, SnapshotSourceSDK)
	}
	if created.RetentionEffective == "" {
		t.Error("no effective retention reported; a client would have to reimplement the fallback chain")
	}

	rec = doRequest(t, h, http.MethodGet, "/v1/snapshots", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	list := decodeBody[SnapshotListResponse](t, rec)
	if len(list.Snapshots) != 1 || list.Snapshots[0].ID != created.ID {
		t.Fatalf("listing did not contain the snapshot: %+v", list.Snapshots)
	}

	rec = doRequest(t, h, http.MethodPost, "/v1/snapshots/"+created.ID+"/restore", SnapshotRestoreRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	restored := decodeBody[RunRecoverResponse](t, rec)
	if restored.Branch == "" {
		t.Error("branch-mode restore reported no branch")
	}
	if restored.SessionID != created.ID {
		t.Errorf("restored %q, asked for %q", restored.SessionID, created.ID)
	}
}

// An unchanged workspace is a 422 and not an empty success: a caller handed an
// id pointing at no commit would believe it had a checkpoint it does not have.
func TestSnapshotRefusesAnUnchangedWorkspace(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)

	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/snapshots", SnapshotCreateRequest{})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create on a clean tree: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "not changed") {
		t.Errorf("the refusal does not say why: %s", rec.Body)
	}
}

// The provenance split, from both directions. It is a scoping rule rather than a
// boundary, but it is the rule the Studio screen is built on, so it has to hold
// on the server and not only on the button.
func TestSnapshotProvenanceDecidesWhoMayRestore(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	h := s.Handler()

	write(t, filepath.Join(s.Project, "sdk.txt"), "from a script\n")
	fromSDK := decodeBody[SnapshotInfo](t, doRequest(t, h, http.MethodPost, "/v1/snapshots", SnapshotCreateRequest{}))

	write(t, filepath.Join(s.Project, "studio.txt"), "from a tab\n")
	rec := asBrowser(t, h, http.MethodPost, "/v1/snapshots", SnapshotCreateRequest{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("browser create: %d %s", rec.Code, rec.Body)
	}
	fromStudio := decodeBody[SnapshotInfo](t, rec)
	if fromStudio.Source != SnapshotSourceRun {
		t.Fatalf("a browser's snapshot has source %q, want %q", fromStudio.Source, SnapshotSourceRun)
	}

	// Studio may not restore what the SDK took.
	rec = asBrowser(t, h, http.MethodPost, "/v1/snapshots/"+fromSDK.ID+"/restore", SnapshotRestoreRequest{})
	if rec.Code != http.StatusForbidden {
		t.Errorf("Studio restoring an SDK snapshot: %d %s, want 403", rec.Code, rec.Body)
	}

	// Studio may restore its own.
	rec = asBrowser(t, h, http.MethodPost, "/v1/snapshots/"+fromStudio.ID+"/restore", SnapshotRestoreRequest{})
	if rec.Code != http.StatusOK {
		t.Errorf("Studio restoring its own snapshot: %d %s", rec.Code, rec.Body)
	}

	// And the SDK may restore anything, including what Studio took: the split
	// exists to stop a tab undoing a script's work, not the other way round.
	rec = doRequest(t, h, http.MethodPost, "/v1/snapshots/"+fromSDK.ID+"/restore", SnapshotRestoreRequest{})
	if rec.Code != http.StatusOK {
		t.Errorf("SDK restoring its own snapshot: %d %s", rec.Code, rec.Body)
	}
}

// Source is derived from the request and never read off the body — a caller able
// to label its own snapshots would be choosing which surface may restore them.
// decodeJSON rejects unknown fields, so the attempt does not even parse.
func TestSnapshotSourceCannotBeClaimed(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	write(t, filepath.Join(s.Project, "work.txt"), "work\n")

	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/snapshots",
		map[string]string{"label": "mine", "source": "run"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a body claiming a source: %d %s, want 400", rec.Code, rec.Body)
	}
}

// A repository is named by id, never by path — the rule projects.go keeps, and
// the reason POST /v1/projects is the one endpoint that validates a host path.
func TestSnapshotRefusesAnUnknownRepository(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	h := s.Handler()

	for _, repo := range []string{"no-such-repo", s.Project, "/etc"} {
		rec := doRequest(t, h, http.MethodPost, "/v1/snapshots", SnapshotCreateRequest{Repo: repo})
		if rec.Code != http.StatusNotFound {
			t.Errorf("repo=%q: %d %s, want 404", repo, rec.Code, rec.Body)
		}
	}
}

func TestSnapshotRetentionRoundTrips(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	h := s.Handler()
	write(t, filepath.Join(s.Project, "work.txt"), "work\n")
	created := decodeBody[SnapshotInfo](t, doRequest(t, h, http.MethodPost, "/v1/snapshots", SnapshotCreateRequest{}))

	rec := doRequest(t, h, http.MethodPost, "/v1/snapshots/"+created.ID+"/retention",
		SnapshotRetentionRequest{Retention: "72h"})
	if rec.Code != http.StatusOK {
		t.Fatalf("set retention: %d %s", rec.Code, rec.Body)
	}
	got := decodeBody[SnapshotInfo](t, rec)
	// Retention is stored verbatim (it is the user's own string), while
	// RetentionEffective is the resolved duration — normalised, because it is
	// what a client renders "kept until" from.
	if got.Retention != "72h" {
		t.Errorf("retention = %q, want %q", got.Retention, "72h")
	}
	if got.RetentionEffective != "72h0m0s" {
		t.Errorf("effective retention = %q, want %q", got.RetentionEffective, "72h0m0s")
	}

	rec = doRequest(t, h, http.MethodPost, "/v1/snapshots/"+created.ID+"/retention",
		SnapshotRetentionRequest{Retention: "next tuesday"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a value that is not a duration: %d, want 422", rec.Code)
	}
}

// The settings endpoint reports the value in force *and* where it came from,
// because a screen that writes one of them needs both: this daemon's own file is
// a layer under config.yaml, so an edit to a hand-typed value would not survive
// a restart and the screen must be able to say so.
func TestSnapshotSettingsSaysWhichLayerIsInForce(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()

	got := decodeBody[SnapshotSettings](t, doRequest(t, h, http.MethodGet, "/v1/snapshots/settings", nil))
	if got.ManualRetention != "168h0m0s" {
		t.Errorf("default manual retention = %q, want 168h0m0s", got.ManualRetention)
	}
	if got.ConfigManualRetention != "" {
		t.Errorf("nothing was typed by hand, but ConfigManualRetention = %q", got.ConfigManualRetention)
	}
	if !got.Writable {
		t.Error("settings reported unwritable under a temp XDG_CONFIG_HOME")
	}

	rec := doRequest(t, h, http.MethodPost, "/v1/snapshots/settings",
		SnapshotSettings{Retention: "336h", ManualRetention: "24h"})
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	saved := decodeBody[SnapshotSettings](t, rec)
	if saved.ManualRetention != "24h0m0s" {
		t.Errorf("after saving, manual retention = %q", saved.ManualRetention)
	}
	// Applied to the running daemon, not only to the file: a later read must
	// report the window just chosen rather than the one this process started up
	// with.
	got = decodeBody[SnapshotSettings](t, doRequest(t, h, http.MethodGet, "/v1/snapshots/settings", nil))
	if got.ManualRetention != "24h0m0s" {
		t.Errorf("re-read manual retention = %q, want 24h0m0s", got.ManualRetention)
	}

	// It writes durations and nothing else.
	rec = doRequest(t, h, http.MethodPost, "/v1/snapshots/settings", SnapshotSettings{ManualRetention: "soon"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a value that is not a duration: %d, want 422", rec.Code)
	}
}

// A baseline is the before-image the daemon records at launch. It is not a
// recovery point, and the listing must not offer it as one — restoring it hands
// back the state the agent started from, which looks like success.
func TestSnapshotListingHidesBaselines(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)

	write(t, filepath.Join(s.Project, "before.txt"), "before\n")
	if commit := baselineFor(s.Project, "claude"); commit == "" {
		t.Skip("no baseline recorded in this environment")
	}

	list := decodeBody[SnapshotListResponse](t, doRequest(t, s.Handler(), http.MethodGet, "/v1/snapshots", nil))
	for _, snap := range list.Snapshots {
		if snap.Status != "snapshot" {
			t.Errorf("listing offered a %s session as a snapshot: %+v", snap.Status, snap)
		}
	}
}
