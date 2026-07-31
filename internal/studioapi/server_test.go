package studioapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// fakeRuntime is an in-memory stand-in for the docker CLI backend: enough of
// runtime.Runtime/Inspector/Controller to exercise every handler without a
// daemon. Start records the spec it was given and creates a matching
// ContainerInfo, the same way `docker run --detach` would.
type fakeRuntime struct {
	mu         sync.Mutex
	containers []runtime.ContainerInfo
	started    []runtime.RunSpec
	stopped    []string
	removed    []string
	killed     []string
	availErr   error
	startErr   error
}

func (f *fakeRuntime) Available(ctx context.Context) error { return f.availErr }
func (f *fakeRuntime) EnsureImage(ctx context.Context, ref string, forceBuild bool) error {
	return nil
}
func (f *fakeRuntime) EnsureNetwork(ctx context.Context, name string) error { return nil }
func (f *fakeRuntime) Run(ctx context.Context, spec runtime.RunSpec) (int, error) {
	return 0, fmt.Errorf("not used by studioapi")
}

func (f *fakeRuntime) Start(ctx context.Context, spec runtime.RunSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return "", f.startErr
	}
	f.started = append(f.started, spec)
	id := fmt.Sprintf("%040x", len(f.containers)+1)
	f.containers = append(f.containers, runtime.ContainerInfo{
		ID:        id,
		Name:      spec.Name,
		Labels:    spec.Labels,
		State:     "running",
		CreatedAt: time.Now(),
		StartedAt: time.Now(),
	})
	return spec.Name, nil
}

func (f *fakeRuntime) Containers(ctx context.Context, labels map[string]string) ([]runtime.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []runtime.ContainerInfo
	for _, c := range f.containers {
		match := true
		for k, v := range labels {
			if c.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeRuntime) Logs(ctx context.Context, id string, follow bool, stdout, stderr io.Writer) error {
	fmt.Fprintln(stdout, "hello from stdout")
	fmt.Fprintln(stderr, "hello from stderr")
	return nil
}

func (f *fakeRuntime) setState(id, state string) {
	for i := range f.containers {
		if f.containers[i].ID == id {
			f.containers[i].State = state
			f.containers[i].FinishedAt = time.Now()
		}
	}
}

func (f *fakeRuntime) Stop(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	f.setState(id, "exited")
	return nil
}

func (f *fakeRuntime) Kill(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, id)
	f.setState(id, "exited")
	return nil
}

func (f *fakeRuntime) Remove(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return nil
}

// newTestServer builds a Server backed by fakeRuntime, with the persisted-auth
// and rescue/audit directories redirected into a scratch dir so tests never
// touch a real ~/.config/sandbox.
func newTestServer(t *testing.T) (*Server, *fakeRuntime) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	project := t.TempDir()

	cfg := config.Default()
	cfg.Profile = config.ProfileDev
	fr := &fakeRuntime{}
	sess := &sandbox.Session{Cfg: cfg, Runtime: fr, Audit: audit.NopSink{}}
	return &Server{
		Session: sess,
		RT:      fr,
		Project: project,
		RepoID:  "testrepo",
		Engine:  "docker",
	}, fr
}

func doRequest(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding response body %q: %v", rec.Body.String(), err)
	}
	return v
}

func TestHandleHealth(t *testing.T) {
	s, _ := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodGet, "/v1/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeBody[HealthResponse](t, rec)
	if got.Status != "ok" || got.Engine != "docker" || got.Profile != "dev" {
		t.Errorf("unexpected health response: %+v", got)
	}
}

func TestHandleAgentsOnlyListsHeadlessCapable(t *testing.T) {
	s, _ := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodGet, "/v1/agents", nil)
	got := decodeBody[AgentsResponse](t, rec)
	if len(got.Agents) == 0 {
		t.Fatal("expected at least one agent")
	}
	for _, a := range got.Agents {
		if a.Name == "" {
			t.Errorf("agent with empty name: %+v", a)
		}
	}
}

func TestCreateRunWithPlainCommand(t *testing.T) {
	s, fr := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Command: []string{"echo", "hi"},
		Branch:  "feature-x",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	got := decodeBody[Run](t, rec)
	if got.Branch != "feature-x" || got.State != RunStateRunning || got.Kind != RunKindInteractive {
		t.Errorf("unexpected run: %+v", got)
	}
	if len(fr.started) != 1 {
		t.Fatalf("started %d containers, want 1", len(fr.started))
	}
	if !fr.started[0].Detach {
		t.Error("a Studio run must always be detached")
	}
}

func TestCreateRunRequiresAgentOrCommand(t *testing.T) {
	s, _ := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{Branch: "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRunWithAgentBuildsAutonomousArgv(t *testing.T) {
	s, fr := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent:  "claude",
		Prompt: "fix the bug",
		Branch: "feature-y",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	got := decodeBody[Run](t, rec)
	if got.Agent != "claude" {
		t.Errorf("agent = %q, want claude", got.Agent)
	}
	if len(fr.started) != 1 {
		t.Fatalf("started %d containers, want 1", len(fr.started))
	}
	argv := strings.Join(fr.started[0].Command, " ")
	if !strings.Contains(argv, "fix the bug") {
		t.Errorf("command %v does not carry the prompt", fr.started[0].Command)
	}
}

func TestListGetAndStopRun(t *testing.T) {
	s, fr := newTestServer(t)
	create := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Command: []string{"sleep", "100"},
		Branch:  "feature-z",
	})
	run := decodeBody[Run](t, create)

	list := doRequest(t, s.Handler(), http.MethodGet, "/v1/runs", nil)
	runs := decodeBody[RunsResponse](t, list)
	if len(runs.Runs) != 1 {
		t.Fatalf("listed %d runs, want 1", len(runs.Runs))
	}

	get := doRequest(t, s.Handler(), http.MethodGet, "/v1/runs/"+run.ID, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET /runs/%s status = %d: %s", run.ID, get.Code, get.Body.String())
	}

	stop := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs/"+run.ID+"/stop", RunStopRequest{})
	stopped := decodeBody[Run](t, stop)
	if stopped.State != RunStateExited {
		t.Errorf("state after stop = %q, want exited", stopped.State)
	}
	if len(fr.stopped) != 1 {
		t.Errorf("Stop called %d times, want 1", len(fr.stopped))
	}

	// Finished runs drop out of the default (live-only) listing.
	list2 := doRequest(t, s.Handler(), http.MethodGet, "/v1/runs", nil)
	runs2 := decodeBody[RunsResponse](t, list2)
	if len(runs2.Runs) != 0 {
		t.Errorf("listed %d live runs after stop, want 0", len(runs2.Runs))
	}
	listAll := doRequest(t, s.Handler(), http.MethodGet, "/v1/runs?all=1", nil)
	runsAll := decodeBody[RunsResponse](t, listAll)
	if len(runsAll.Runs) != 1 {
		t.Errorf("listed %d runs with all=1, want 1", len(runsAll.Runs))
	}
}

func TestGetRunNotFound(t *testing.T) {
	s, _ := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodGet, "/v1/runs/doesnotexist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRunLogsStreamsBothChannels(t *testing.T) {
	s, _ := newTestServer(t)
	create := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Command: []string{"true"},
		Branch:  "feature-logs",
	})
	run := decodeBody[Run](t, create)

	// Without follow the log is a document, and comes back as JSON. Framing a
	// finished container's output as an event stream forced every client to
	// implement a parser to read something that had already stopped changing.
	rec := doRequest(t, s.Handler(), http.MethodGet, "/v1/runs/"+run.ID+"/logs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	lines := decodeBody[[]LogLine](t, rec)
	if len(lines) != 2 {
		t.Fatalf("want two lines, got %+v", lines)
	}
	var sawOut, sawErr bool
	for _, l := range lines {
		if l.Stream == "stdout" && l.Text == "hello from stdout" {
			sawOut = true
		}
		// Which stream a line came from is kept, because that is how a reader
		// separates the agent's output from the proxy's DENY lines beside it.
		if l.Stream == "stderr" && l.Text == "hello from stderr" {
			sawErr = true
		}
	}
	if !sawOut || !sawErr {
		t.Errorf("both streams must survive, got %+v", lines)
	}

	// follow=1 is still SSE: there the connection is the point.
	stream := doRequest(t, s.Handler(), http.MethodGet, "/v1/runs/"+run.ID+"/logs?follow=1", nil)
	if !strings.Contains(stream.Body.String(), "event: log") {
		t.Errorf("follow=1 must stay an event stream: %s", stream.Body.String())
	}
}

func TestCORSOnlyReflectsConfiguredOrigins(t *testing.T) {
	s, _ := newTestServer(t)
	s.CORSOrigins = []string{"http://localhost:3000"}
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("allowed origin got no CORS header, got %q", got)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req2.Header.Set("Origin", "http://evil.example")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if got := rec2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("unconfigured origin got a CORS header: %q", got)
	}
}

func TestTokenRequiredExceptHealth(t *testing.T) {
	s, _ := newTestServer(t)
	s.Token = "secret"
	h := s.Handler()

	if rec := doRequest(t, h, http.MethodGet, "/v1/health", nil); rec.Code != http.StatusOK {
		t.Errorf("/health without token = %d, want 200", rec.Code)
	}
	if rec := doRequest(t, h, http.MethodGet, "/v1/runs", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("/runs without token = %d, want 401", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/runs", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/runs with valid token = %d, want 200", rec.Code)
	}
}

func TestWorktreeLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	s, _ := newTestServer(t)
	initGitRepo(t, s.Project)

	create := doRequest(t, s.Handler(), http.MethodPost, "/v1/worktrees", WorktreeCreateRequest{Branch: "feature-a"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", create.Code, create.Body.String())
	}
	wt := decodeBody[Worktree](t, create)
	if wt.Branch != "feature-a" || wt.Path == "" {
		t.Errorf("unexpected worktree: %+v", wt)
	}

	list := doRequest(t, s.Handler(), http.MethodGet, "/v1/worktrees", nil)
	wts := decodeBody[WorktreesResponse](t, list)
	if len(wts.Worktrees) != 1 {
		t.Fatalf("listed %d worktrees, want 1", len(wts.Worktrees))
	}

	get := doRequest(t, s.Handler(), http.MethodGet, "/v1/worktrees/feature-a", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", get.Code, get.Body.String())
	}

	del := doRequest(t, s.Handler(), http.MethodDelete, "/v1/worktrees/feature-a", nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", del.Code, del.Body.String())
	}

	getAfter := doRequest(t, s.Handler(), http.MethodGet, "/v1/worktrees/feature-a", nil)
	if getAfter.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d, want 404", getAfter.Code)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "--allow-empty", "-q", "-m", "init")
}

// The prefix is a contract with a separate codebase — studio/src/lib/api builds
// every request as `${API_BASE}/v1/...`, and its daemon probe decides between
// live and fixture data on the strength of one call to /v1/health. A 404 there
// does not surface as an error: the UI silently renders mock data that looks
// entirely plausible. That is how the two halves shipped unable to talk to each
// other, so the prefix is pinned rather than left to the route list.
func TestRoutesAreServedUnderTheV1Prefix(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()

	if rec := doRequest(t, h, http.MethodGet, "/v1/health", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /v1/health = %d, want 200 — the UI's probe path", rec.Code)
	}
	// Unprefixed must not answer, or a UI pointed at the wrong base would work
	// in development and fail wherever the prefix is enforced.
	for _, p := range []string{"/health", "/agents", "/runs", "/stats", "/worktrees"} {
		if rec := doRequest(t, h, http.MethodGet, p, nil); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 — routes live under /v1 only", p, rec.Code)
		}
	}
}

// Empty lists must marshal as `[]`, never `null` or absent.
//
// Worktree.Dirty carried `omitempty`, so a *clean* worktree — the common case —
// sent no `dirty` key at all, and the UI's `w.dirty.length` threw on it. A
// list-valued field that disappears when empty makes the ordinary case the one
// every client has to guard, and the crash arrives in production rather than in
// the fixture that was used to build the screen.
func TestEmptyListsMarshalAsArraysNotNull(t *testing.T) {
	body, err := json.Marshal(WorktreesResponse{Worktrees: []Worktree{{
		Branch: "feature-a",
		Path:   "/tmp/feature-a",
		Dirty:  []string{},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); !strings.Contains(got, `"dirty":[]`) {
		t.Errorf("a clean worktree must send an empty dirty list, got %s", got)
	}

	// And the enclosing envelopes, for the same reason.
	for _, tc := range []struct {
		name, want string
		v          any
	}{
		{"worktrees", `"worktrees":[]`, WorktreesResponse{Worktrees: []Worktree{}}},
		{"runs", `"runs":[]`, RunsResponse{Runs: []Run{}}},
		{"agents", `"agents":[]`, AgentsResponse{Agents: []AgentInfo{}}},
	} {
		b, err := json.Marshal(tc.v)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), tc.want) {
			t.Errorf("%s: want %s in %s", tc.name, tc.want, b)
		}
	}
}

// A launch refused because the branch's container name is taken must say so in
// this tool's words, and as 409 rather than 502.
//
// Docker's own message is `Conflict. The container name "/sandbox-<repo>-<branch>"
// is already in use by container "<64 hex chars>"` — which names neither the
// branch nor a run id, and forwarded as 502 claims the daemon misbehaved when it
// did exactly its job. The name *is* the enforcement: it refuses duplicates
// atomically, which is what stops two agents landing in one checkout.
func TestCreateRunExplainsAContainerNameConflict(t *testing.T) {
	s, fr := newTestServer(t)

	// First launch succeeds and leaves a container holding the branch's name.
	first := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Command: []string{"echo", "hi"},
		Branch:  "feature-x",
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first launch = %d, want 201: %s", first.Code, first.Body.String())
	}

	// The engine refuses the second, exactly as docker does.
	fr.mu.Lock()
	fr.startErr = fmt.Errorf(`Conflict. The container name "/sandbox-repo-feature-x" is already in use`)
	fr.mu.Unlock()

	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Command: []string{"echo", "hi"},
		Branch:  "feature-x",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second launch = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"feature-x", "already running"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal must mention %q, got %s", want, body)
		}
	}
	// And it must not be docker's raw text.
	if strings.Contains(body, "Conflict. The container name") {
		t.Errorf("docker's own message was forwarded verbatim: %s", body)
	}
}

// An engine failure that is *not* a name conflict must still surface as one.
func TestCreateRunStillReportsOtherEngineFailures(t *testing.T) {
	s, fr := newTestServer(t)
	fr.mu.Lock()
	fr.startErr = fmt.Errorf("no space left on device")
	fr.mu.Unlock()

	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Command: []string{"echo", "hi"},
		Branch:  "feature-y",
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no space left") {
		t.Errorf("the engine's error must survive: %s", rec.Body.String())
	}
}

// `land` refuses a branch that never passed its verify, so "nothing checked
// this" and "this failed" are different answers and the wire has to keep them
// apart. Null is not false.
func TestWorktreeVerifiedDistinguishesUncheckedFromFailed(t *testing.T) {
	withVerify := func(state string, code int) runtime.ContainerInfo {
		return runtime.ContainerInfo{
			State:    state,
			ExitCode: code,
			Labels:   map[string]string{sandbox.LabelVerify: "go test ./..."},
		}
	}
	noVerify := runtime.ContainerInfo{State: "exited", Labels: map[string]string{}}

	cases := []struct {
		name string
		in   []runtime.ContainerInfo
		want *bool
	}{
		{"no container to ask", nil, nil},
		{"ran, declared no verify", []runtime.ContainerInfo{noVerify}, nil},
		{"passed", []runtime.ContainerInfo{withVerify("exited", 0)}, boolPtr(true)},
		{"failed its verify", []runtime.ContainerInfo{withVerify("exited", 90)}, boolPtr(false)},
		{"died before its verify", []runtime.ContainerInfo{withVerify("exited", 137)}, boolPtr(false)},
		{"still running", []runtime.ContainerInfo{withVerify("running", 0)}, nil},
		// Newest first: an older passing run must not speak for a newer failure.
		{"newest wins", []runtime.ContainerInfo{withVerify("exited", 90), withVerify("exited", 0)}, boolPtr(false)},
	}
	for _, tc := range cases {
		got := verifiedByLastRun(tc.in)
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("%s: got %v, want null", tc.name, *got)
		case tc.want != nil && got == nil:
			t.Errorf("%s: got null, want %v", tc.name, *tc.want)
		case tc.want != nil && got != nil && *tc.want != *got:
			t.Errorf("%s: got %v, want %v", tc.name, *got, *tc.want)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

// The run detail screen reads run.network.allow.length and run.security.*, so
// these shapes are a contract rather than an implementation detail — the UI's
// NetworkPosture and SecurityPosture, filled from the container itself.
//
// The posture is read back rather than taken from config on purpose: config says
// what was asked for, and a run reviewed later was confined by what it actually
// got.
func TestRunPostureIsReadBackFromTheContainer(t *testing.T) {
	c := runtime.ContainerInfo{
		ID:          "abcdef0123456789",
		State:       "exited",
		NetworkMode: "sandbox-cli",
		User:        "1001:1001",
		Env: []string{
			"SANDBOX_EGRESS_ALLOW=api.anthropic.com,registry.npmjs.org,internal.example.com",
			"SANDBOX_INGRESS_PORTS=3000,8787",
			"HOME=/sandbox/home",
		},
		Mounts: []runtime.MountInfo{
			{Source: "/host/proj", Destination: "/workspace", ReadWrite: true},
			{Source: "/host/agents/claude", Destination: "/sandbox/home", ReadWrite: true},
		},
		Security: runtime.SecurityInfo{
			CapDrop:     []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"},
			PidsLimit:   1024,
			MemoryBytes: 4 * 1024 * 1024 * 1024,
			NanoCPUs:    2e9,
		},
	}
	got := toRun(c, "docker")

	if got.Network.Mode != "allowlist" {
		t.Errorf("network.mode = %q, want allowlist — the control variable is set", got.Network.Mode)
	}
	if len(got.Network.Allow) != 3 {
		t.Errorf("network.allow = %v, want the three resolved domains", got.Network.Allow)
	}
	if !got.Network.Baseline {
		t.Error("network.baseline: api.anthropic.com is in the list, so the baseline is part of it")
	}
	if got.Network.Enforcement == nil || *got.Network.Enforcement != "name" {
		t.Errorf("network.enforcement = %v, want name — the proxy decides on the hostname", got.Network.Enforcement)
	}
	if len(got.Network.IngressPorts) != 2 {
		t.Errorf("ingressPorts = %v, want two", got.Network.IngressPorts)
	}

	if !got.Security.NoNewPrivileges || !got.Security.Hardening {
		t.Errorf("security: cap-drop ALL plus no-new-privileges is hardened, got %+v", got.Security)
	}
	if got.Security.Memory != "4096m" || got.Security.CPUs != "2" {
		t.Errorf("security limits = %q/%q, want 4096m/2", got.Security.Memory, got.Security.CPUs)
	}
	if got.Security.User != "1001:1001" {
		t.Errorf("security.user = %q", got.Security.User)
	}

	if len(got.Mounts) != 2 || got.Mounts[0].Host != "/host/proj" || got.Mounts[0].Mode != "rw" {
		t.Fatalf("mounts = %+v", got.Mounts)
	}
	if got.Mounts[0].Origin != "workspace" || got.Mounts[1].Origin != "persisted-home" {
		t.Errorf("mount origins = %q/%q", got.Mounts[0].Origin, got.Mounts[1].Origin)
	}
}

// No allowlist means no enforcement, and null is the honest answer rather than
// a string naming a mechanism that is not running.
func TestRunWithNoAllowlistReportsNoEnforcement(t *testing.T) {
	got := toRun(runtime.ContainerInfo{NetworkMode: "bridge"}, "docker")
	if got.Network.Mode != "default" || got.Network.Enforcement != nil {
		t.Errorf("network = %+v, want mode default and no enforcement", got.Network)
	}
	if got.Network.Allow == nil {
		t.Error("allow must be an empty list, not null: clients iterate it")
	}
}

// The API could create runs and not remove them, which left a client stuck the
// moment a branch's container name was taken: the launch refusal could only be
// acted on by leaving Studio for a terminal.
func TestDeleteRunReapsAFinishedContainer(t *testing.T) {
	s, fr := newTestServer(t)
	create := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Command: []string{"true"},
		Branch:  "feature-reap",
	})
	run := decodeBody[Run](t, create)

	// A running container is refused. stop and remove are different acts and the
	// difference is an agent's unsaved work, so the caller has to say which.
	rec := doRequest(t, s.Handler(), http.MethodDelete, "/v1/runs/"+run.ID, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("deleting a running run = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "stop it first") {
		t.Errorf("the refusal must say what to do instead: %s", rec.Body.String())
	}
	if len(fr.removed) != 0 {
		t.Fatalf("a running container was removed: %v", fr.removed)
	}

	fr.mu.Lock()
	for i := range fr.containers {
		fr.containers[i].State = "exited"
	}
	fr.mu.Unlock()

	rec = doRequest(t, s.Handler(), http.MethodDelete, "/v1/runs/"+run.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deleting a finished run = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if len(fr.removed) != 1 {
		t.Errorf("expected the container reaped, removed %v", fr.removed)
	}
}
