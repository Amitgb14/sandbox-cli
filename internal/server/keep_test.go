package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// A server that keeps records, over a fake that outlives it: the fake plays
// the VMs a stopped sandboxd leaves running, and a second server on the same
// directory plays the sandboxd that takes them back.
func keepServer(t *testing.T, b *fake.Backend, dir string) http.Handler {
	t.Helper()
	s := &Server{Backend: b, Policy: spec.DefaultPolicyFor(b.Capabilities()), RecordDir: dir}
	if _, err := s.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Before the records directory goes (its cleanup was registered first, so
	// runs last): end every process and wait for the record each exit writes.
	// Without this, an exit landing during the removal failed the test with
	// "directory not empty".
	t.Cleanup(func() {
		for id := range b.Kept() {
			_ = b.Terminate(context.Background(), id)
		}
		s.watchers.Wait()
	})
	return s.Handler()
}

func call(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	req.Host = "127.0.0.1"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mustCreate(t *testing.T, h http.Handler, req api.CreateSandboxRequest) api.Sandbox {
	t.Helper()
	r := call(t, h, "POST", "/v1/sandboxes", req)
	if r.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Code, r.Body)
	}
	var sb api.Sandbox
	_ = json.Unmarshal(r.Body.Bytes(), &sb)
	return sb
}

func startProc(t *testing.T, h http.Handler, ref string, argv ...string) api.Process {
	t.Helper()
	r := call(t, h, "POST", "/v1/sandboxes/"+ref+"/processes", api.RunRequest{Argv: argv})
	if r.Code != http.StatusCreated && r.Code != http.StatusOK {
		t.Fatalf("start %v: %d %s", argv, r.Code, r.Body)
	}
	var p api.Process
	_ = json.Unmarshal(r.Body.Bytes(), &p)
	return p
}

func newKeepDir(t *testing.T) string {
	dir := filepath.Join(t.TempDir(), "records")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The whole point: after the restart the sandbox is there, as it was —
// name, labels, policy and environment — with no processes, and the next
// process never reuses a number a client may still hold.
func TestRestoreTakesBackASandboxAsItWas(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	dir := newKeepDir(t)
	h1 := keepServer(t, b, dir)
	sb := mustCreate(t, h1, api.CreateSandboxRequest{Name: "web", Env: map[string]string{"API_KEY": "s3cret"},
		Labels: map[string]string{"team": "a"}})
	startProc(t, h1, "web", "true")
	if p := startProc(t, h1, "web", "sleep", "30"); p.PID != 2 {
		t.Fatalf("second process is %d", p.PID)
	}
	b.Detach() // sandboxd exits; the VM stays, its processes end

	h2 := keepServer(t, b, dir)
	r := call(t, h2, "GET", "/v1/sandboxes/web", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("after restart: %d %s", r.Code, r.Body)
	}
	var got api.Sandbox
	_ = json.Unmarshal(r.Body.Bytes(), &got)
	if got.ID != sb.ID || got.State != api.StateRunning || got.Labels["team"] != "a" || got.Network.Mode != sb.Network.Mode {
		t.Fatalf("taken back as %+v, was %+v", got, sb)
	}
	// The running process came through, under its number; the one that had
	// finished is, if listed at all, listed as finished.
	var procs api.ProcessList
	_ = json.Unmarshal(call(t, h2, "GET", "/v1/sandboxes/web/processes", nil).Body.Bytes(), &procs)
	var sleeping *api.Process
	for i, p := range procs.Processes {
		switch p.PID {
		case 2:
			sleeping = &procs.Processes[i]
		case 1:
			if p.State != api.ProcessExited || p.ExitCode == nil || *p.ExitCode != 0 {
				t.Fatalf("the finished process after the restart: %+v", p)
			}
		default:
			t.Fatalf("an unexpected process after the restart: %+v", p)
		}
	}
	if sleeping == nil || sleeping.State != api.ProcessRunning || strings.Join(sleeping.Argv, " ") != "sleep 30" {
		t.Fatalf("the running process was not taken back: %+v", procs.Processes)
	}
	if r := call(t, h2, "POST", "/v1/sandboxes/web/processes/2/signal", api.SignalRequest{Signal: "KILL"}); r.Code/100 != 2 {
		t.Fatalf("signal to the taken-back process: %d %s", r.Code, r.Body)
	}
	if p := startProc(t, h2, "web", "true"); p.PID != 3 {
		t.Fatalf("first process after the restart is %d, want 3", p.PID)
	}
	run := call(t, h2, "POST", "/v1/sandboxes/web/run", api.RunRequest{Argv: []string{"printenv", "API_KEY"}})
	var res api.RunResult
	_ = json.Unmarshal(run.Body.Bytes(), &res)
	if strings.TrimSpace(string(res.Stdout)) != "s3cret" {
		t.Fatalf("environment after the restart: %d %s", run.Code, run.Body)
	}
}

func TestRestoreKeepsASuspendedSandboxSuspended(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist, api.CapSuspend)
	dir := newKeepDir(t)
	h1 := keepServer(t, b, dir)
	mustCreate(t, h1, api.CreateSandboxRequest{Name: "s"})
	if r := call(t, h1, "POST", "/v1/sandboxes/s/suspend", nil); r.Code != http.StatusOK {
		t.Fatalf("suspend: %d %s", r.Code, r.Body)
	}
	h2 := keepServer(t, b, dir)
	var got api.Sandbox
	_ = json.Unmarshal(call(t, h2, "GET", "/v1/sandboxes/s", nil).Body.Bytes(), &got)
	if got.State != api.StateSuspended {
		t.Fatalf("state after the restart: %q", got.State)
	}
}

// A VM with no record is not served: nothing says whose it is or what it may
// reach. It is terminated.
func TestRestoreTerminatesAVMWithoutARecord(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	plain := &Server{Backend: b, Policy: spec.DefaultPolicyFor(b.Capabilities())} // keeps no records
	mustCreate(t, plain.Handler(), api.CreateSandboxRequest{Name: "orphan"})
	if b.Count() != 1 {
		t.Fatal("precondition: the fake holds the sandbox")
	}
	h := keepServer(t, b, newKeepDir(t))
	if b.Count() != 0 {
		t.Fatalf("a VM without a record was kept")
	}
	if r := call(t, h, "GET", "/v1/sandboxes/orphan", nil); r.Code != http.StatusNotFound {
		t.Fatalf("served a sandbox it has no record of: %d", r.Code)
	}
}

func TestRestoreDropsARecordWhoseVMIsGone(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	dir := newKeepDir(t)
	sb := mustCreate(t, keepServer(t, b, dir), api.CreateSandboxRequest{})
	_ = b.Terminate(context.Background(), sb.ID) // the VM did not survive
	h := keepServer(t, b, dir)
	if _, err := os.Stat(filepath.Join(dir, sb.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("record kept for a VM that is gone: %v", err)
	}
	if r := call(t, h, "GET", "/v1/sandboxes/"+sb.ID, nil); r.Code != http.StatusNotFound {
		t.Fatalf("served a sandbox whose VM is gone: %d", r.Code)
	}
}

// A record must name the sandbox its file is named for; one that names
// another is not used, and the VM it would have described is terminated.
func TestRestoreRefusesARecordNamingAnotherSandbox(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	dir := newKeepDir(t)
	a := mustCreate(t, keepServer(t, b, dir), api.CreateSandboxRequest{Name: "a"})
	other := mustCreate(t, keepServer(t, b, dir), api.CreateSandboxRequest{Name: "b"})
	data, _ := os.ReadFile(filepath.Join(dir, other.ID+".json"))
	if err := os.WriteFile(filepath.Join(dir, a.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	h := keepServer(t, b, dir)
	if r := call(t, h, "GET", "/v1/sandboxes/"+a.ID, nil); r.Code != http.StatusNotFound {
		t.Fatalf("a record naming another sandbox was used: %d", r.Code)
	}
	if r := call(t, h, "GET", "/v1/sandboxes/b", nil); r.Code != http.StatusOK {
		t.Fatalf("the other sandbox: %d", r.Code)
	}
	if b.Count() != 1 {
		t.Fatalf("the VM without a usable record was kept: %d VMs", b.Count())
	}
}

// The records hold environment values: the directory and each file are the
// operator's alone, and a directory others can read is refused at start.
func TestRecordsAreOwnerOnly(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	dir := newKeepDir(t)
	sb := mustCreate(t, keepServer(t, b, dir), api.CreateSandboxRequest{Env: map[string]string{"TOKEN": "x"}})
	fi, err := os.Stat(filepath.Join(dir, sb.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("record mode %v", fi.Mode().Perm())
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{Backend: b, Policy: spec.DefaultPolicyFor(b.Capabilities()), RecordDir: dir}
	if _, err := s.Restore(context.Background()); err == nil || !strings.Contains(err.Error(), "readable by other users") {
		t.Fatalf("a readable records directory was accepted: %v", err)
	}
}

func TestEndingASandboxDeletesItsRecord(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	dir := newKeepDir(t)
	h := keepServer(t, b, dir)
	sb := mustCreate(t, h, api.CreateSandboxRequest{})
	path := filepath.Join(dir, sb.ID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("precondition: no record written: %v", err)
	}
	if r := call(t, h, "DELETE", "/v1/sandboxes/"+sb.ID, nil); r.Code != http.StatusNoContent {
		t.Fatalf("terminate: %d", r.Code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("record outlived its sandbox: %v", err)
	}
}

// Without a records directory nothing is written: the default keeps today's
// behaviour, with environment values in memory only.
func TestNoRecordDirWritesNothing(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	s := &Server{Backend: b, Policy: spec.DefaultPolicyFor(b.Capabilities())}
	if n, err := s.Restore(context.Background()); n != 0 || err != nil {
		t.Fatalf("Restore without a directory: %d, %v", n, err)
	}
	mustCreate(t, s.Handler(), api.CreateSandboxRequest{Env: map[string]string{"TOKEN": "x"}})
}

// A policy tightened while sandboxd was down applies to the sandboxes it left:
// one the new policy would refuse is ended, not served under the old one.
func TestRestoreEndsASandboxTheTightenedPolicyRefuses(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	dir := newKeepDir(t)
	mustCreate(t, keepServer(t, b, dir), api.CreateSandboxRequest{Name: "net",
		Network: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"example.com"}}})
	strict := spec.DefaultPolicyFor(b.Capabilities())
	strict.Network.Ceiling = api.NetworkNone
	strict.Network.Default = api.NetworkPolicy{Mode: api.NetworkNone}
	strict.Network.MayAllow = nil
	s := &Server{Backend: b, Policy: strict, RecordDir: dir}
	if n, err := s.Restore(context.Background()); err != nil || n != 0 {
		t.Fatalf("Restore under a stricter policy: %d, %v", n, err)
	}
	if b.Count() != 0 {
		t.Fatal("a sandbox the new policy refuses was kept")
	}
}

// A process the sandbox no longer holds, or a session the record names that
// was never this sandbox's, is listed as ended (-1), not served: the guest's
// word is not taken for what runs, and a client holding the number learns it
// is gone.
func TestRestoreReportsAProcessThatCannotBeTakenBack(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	dir := newKeepDir(t)
	h1 := keepServer(t, b, dir)
	sb := mustCreate(t, h1, api.CreateSandboxRequest{Name: "p"})
	startProc(t, h1, "p", "sleep", "30")
	b.Detach()
	// The record now names a session the sandbox does not hold.
	path := filepath.Join(dir, sb.ID+".json")
	data, _ := os.ReadFile(path)
	var kr keptRecord
	if err := json.Unmarshal(data, &kr); err != nil || len(kr.Procs) != 1 {
		t.Fatalf("precondition: the record keeps the running process: %+v, %v", kr.Procs, err)
	}
	kr.Procs[0].Session = "pnotthesandboxes"
	data, _ = json.Marshal(kr)
	_ = os.WriteFile(path, data, 0o600)

	h2 := keepServer(t, b, dir)
	var p api.Process
	r := call(t, h2, "GET", "/v1/sandboxes/p/processes/1", nil)
	_ = json.Unmarshal(r.Body.Bytes(), &p)
	if p.State != api.ProcessExited || p.ExitCode == nil || *p.ExitCode != -1 {
		t.Fatalf("a process that could not be taken back: %d %+v", r.Code, p)
	}
}

// Without kept records, processes are not started kept: the guest's old rule
// — a process ends with its connection — holds for everyone else.
func TestProcessesAreKeptOnlyWhenSandboxesAre(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	s := &Server{Backend: b, Policy: spec.DefaultPolicyFor(b.Capabilities())}
	h := s.Handler()
	mustCreate(t, h, api.CreateSandboxRequest{Name: "plain"})
	startProc(t, h, "plain", "sleep", "30")
	b.Detach()
	var p api.Process
	deadline := time.Now().Add(5 * time.Second)
	for p.State != api.ProcessExited && time.Now().Before(deadline) {
		_ = json.Unmarshal(call(t, h, "GET", "/v1/sandboxes/plain/processes/1", nil).Body.Bytes(), &p)
		time.Sleep(10 * time.Millisecond)
	}
	if p.State != api.ProcessExited {
		t.Fatal("a process not started kept outlived its connection")
	}
}

func takeSnap(t *testing.T, h http.Handler, ref string) api.Snapshot {
	t.Helper()
	r := call(t, h, "POST", "/v1/sandboxes/"+ref+"/snapshots", nil)
	if r.Code/100 != 2 {
		t.Fatalf("snapshot: %d %s", r.Code, r.Body)
	}
	var sn api.Snapshot
	_ = json.Unmarshal(r.Body.Bytes(), &sn)
	return sn
}

func listSnaps(t *testing.T, h http.Handler) []api.Snapshot {
	t.Helper()
	var l api.SnapshotList
	_ = json.Unmarshal(call(t, h, "GET", "/v1/snapshots", nil).Body.Bytes(), &l)
	return l.Snapshots
}

// A snapshot outlives a restart like its sandbox: listed, and a sandbox can
// still be started from it.
func TestRestoreTakesBackSnapshots(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist, api.CapMemorySnapshot)
	dir := newKeepDir(t)
	h1 := keepServer(t, b, dir)
	mustCreate(t, h1, api.CreateSandboxRequest{Name: "src", Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
	sn := takeSnap(t, h1, "src")

	h2 := keepServer(t, b, dir)
	got := listSnaps(t, h2)
	if len(got) != 1 || got[0].ID != sn.ID || got[0].Sandbox != sn.Sandbox {
		t.Fatalf("snapshots after the restart: %+v", got)
	}
	mustCreate(t, h2, api.CreateSandboxRequest{Name: "fork", SnapshotID: sn.ID, Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
	// Deleting it deletes its record too: a later restart does not bring it back.
	if r := call(t, h2, "DELETE", "/v1/snapshots/"+sn.ID, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", r.Code)
	}
	if got := listSnaps(t, keepServer(t, b, dir)); len(got) != 0 {
		t.Fatalf("a deleted snapshot came back: %+v", got)
	}
}

// Snapshot files no record names are deleted at the restart: nothing could
// ever use or remove them otherwise. A record whose files are gone is dropped.
func TestRestoreDeletesSnapshotsNoRecordNames(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist, api.CapMemorySnapshot)
	plain := &Server{Backend: b, Policy: spec.DefaultPolicyFor(b.Capabilities())} // keeps no records
	ph := plain.Handler()
	mustCreate(t, ph, api.CreateSandboxRequest{Name: "a", Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
	takeSnap(t, ph, "a")
	if len(b.StoredSnapshots()) != 1 {
		t.Fatal("precondition: the fake holds the snapshot")
	}
	dir := newKeepDir(t)
	keepServer(t, b, dir)
	if n := len(b.StoredSnapshots()); n != 0 {
		t.Fatalf("%d snapshot(s) no record names were kept", n)
	}

	h := keepServer(t, b, dir)
	mustCreate(t, h, api.CreateSandboxRequest{Name: "b", Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
	sn := takeSnap(t, h, "b")
	_ = b.DeleteSnapshot(context.Background(), sn.ID) // its files did not survive
	if got := listSnaps(t, keepServer(t, b, dir)); len(got) != 0 {
		t.Fatalf("a snapshot whose files are gone is listed: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "snapshots", sn.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("its record was kept: %v", err)
	}
}
