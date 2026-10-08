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
	var procs api.ProcessList
	_ = json.Unmarshal(call(t, h2, "GET", "/v1/sandboxes/web/processes", nil).Body.Bytes(), &procs)
	if len(procs.Processes) != 0 {
		t.Fatalf("processes survived the restart: %+v", procs.Processes)
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
