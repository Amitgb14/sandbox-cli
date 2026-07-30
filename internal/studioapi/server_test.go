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

func (f *fakeRuntime) Remove(ctx context.Context, id string) error { return nil }

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
	rec := doRequest(t, s.Handler(), http.MethodGet, "/health", nil)
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
	rec := doRequest(t, s.Handler(), http.MethodGet, "/agents", nil)
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
	rec := doRequest(t, s.Handler(), http.MethodPost, "/runs", RunCreateRequest{
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
	rec := doRequest(t, s.Handler(), http.MethodPost, "/runs", RunCreateRequest{Branch: "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRunWithAgentBuildsAutonomousArgv(t *testing.T) {
	s, fr := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodPost, "/runs", RunCreateRequest{
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
	create := doRequest(t, s.Handler(), http.MethodPost, "/runs", RunCreateRequest{
		Command: []string{"sleep", "100"},
		Branch:  "feature-z",
	})
	run := decodeBody[Run](t, create)

	list := doRequest(t, s.Handler(), http.MethodGet, "/runs", nil)
	runs := decodeBody[RunsResponse](t, list)
	if len(runs.Runs) != 1 {
		t.Fatalf("listed %d runs, want 1", len(runs.Runs))
	}

	get := doRequest(t, s.Handler(), http.MethodGet, "/runs/"+run.ID, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET /runs/%s status = %d: %s", run.ID, get.Code, get.Body.String())
	}

	stop := doRequest(t, s.Handler(), http.MethodPost, "/runs/"+run.ID+"/stop", RunStopRequest{})
	stopped := decodeBody[Run](t, stop)
	if stopped.State != RunStateExited {
		t.Errorf("state after stop = %q, want exited", stopped.State)
	}
	if len(fr.stopped) != 1 {
		t.Errorf("Stop called %d times, want 1", len(fr.stopped))
	}

	// Finished runs drop out of the default (live-only) listing.
	list2 := doRequest(t, s.Handler(), http.MethodGet, "/runs", nil)
	runs2 := decodeBody[RunsResponse](t, list2)
	if len(runs2.Runs) != 0 {
		t.Errorf("listed %d live runs after stop, want 0", len(runs2.Runs))
	}
	listAll := doRequest(t, s.Handler(), http.MethodGet, "/runs?all=1", nil)
	runsAll := decodeBody[RunsResponse](t, listAll)
	if len(runsAll.Runs) != 1 {
		t.Errorf("listed %d runs with all=1, want 1", len(runsAll.Runs))
	}
}

func TestGetRunNotFound(t *testing.T) {
	s, _ := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodGet, "/runs/doesnotexist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRunLogsStreamsBothChannels(t *testing.T) {
	s, _ := newTestServer(t)
	create := doRequest(t, s.Handler(), http.MethodPost, "/runs", RunCreateRequest{
		Command: []string{"true"},
		Branch:  "feature-logs",
	})
	run := decodeBody[Run](t, create)

	rec := doRequest(t, s.Handler(), http.MethodGet, "/runs/"+run.ID+"/logs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hello from stdout") || !strings.Contains(body, "hello from stderr") {
		t.Errorf("log stream missing expected lines: %s", body)
	}
	if !strings.Contains(body, "event: log") {
		t.Errorf("log stream missing SSE event framing: %s", body)
	}
}

func TestCORSOnlyReflectsConfiguredOrigins(t *testing.T) {
	s, _ := newTestServer(t)
	s.CORSOrigins = []string{"http://localhost:3000"}
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("allowed origin got no CORS header, got %q", got)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/health", nil)
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

	if rec := doRequest(t, h, http.MethodGet, "/health", nil); rec.Code != http.StatusOK {
		t.Errorf("/health without token = %d, want 200", rec.Code)
	}
	if rec := doRequest(t, h, http.MethodGet, "/runs", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("/runs without token = %d, want 401", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/runs", nil)
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

	create := doRequest(t, s.Handler(), http.MethodPost, "/worktrees", WorktreeCreateRequest{Branch: "feature-a"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", create.Code, create.Body.String())
	}
	wt := decodeBody[Worktree](t, create)
	if wt.Branch != "feature-a" || wt.Path == "" {
		t.Errorf("unexpected worktree: %+v", wt)
	}

	list := doRequest(t, s.Handler(), http.MethodGet, "/worktrees", nil)
	wts := decodeBody[WorktreesResponse](t, list)
	if len(wts.Worktrees) != 1 {
		t.Fatalf("listed %d worktrees, want 1", len(wts.Worktrees))
	}

	get := doRequest(t, s.Handler(), http.MethodGet, "/worktrees/feature-a", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", get.Code, get.Body.String())
	}

	del := doRequest(t, s.Handler(), http.MethodDelete, "/worktrees/feature-a", nil)
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", del.Code, del.Body.String())
	}

	getAfter := doRequest(t, s.Handler(), http.MethodGet, "/worktrees/feature-a", nil)
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
