package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// jobsStandIn answers the job and secret endpoints and records what it was
// sent.
type jobsStandIn struct {
	mu      sync.Mutex
	specs   []api.JobSpec
	runs    []api.AgentRunRequest
	secrets map[string]string
	exit    int
}

func (g *jobsStandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	done := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	code := g.exit
	state := api.RunSucceeded
	if code != 0 {
		state = api.RunFailed
	}
	job := api.Job{ID: "job_0123456789abcdef", State: api.JobSucceeded, Finished: &done,
		Runs: []api.JobRun{{N: 0, State: state, ExitCode: &code, Attempts: 1, Sandbox: "sbx_n1_0123456789abcdef"}}}
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/whoami":
		_ = json.NewEncoder(w).Encode(api.Whoami{User: "ana"})
	case r.Method == "POST" && r.URL.Path == "/v1/jobs":
		var s api.JobSpec
		_ = json.NewDecoder(r.Body).Decode(&s)
		g.specs = append(g.specs, s)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(job)
	case r.Method == "POST" && r.URL.Path == "/v1/agent-runs":
		var a api.AgentRunRequest
		_ = json.NewDecoder(r.Body).Decode(&a)
		g.runs = append(g.runs, a)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(job)
	case r.Method == "GET" && r.URL.Path == "/v1/jobs/job_0123456789abcdef":
		_ = json.NewEncoder(w).Encode(job)
	case r.Method == "GET" && r.URL.Path == "/v1/jobs/job_0123456789abcdef/runs/0/output":
		_ = json.NewEncoder(w).Encode(api.JobOutput{Stdout: []byte("agent says hi\n")})
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/secrets/"):
		var req api.SecretRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		g.secrets[strings.TrimPrefix(r.URL.Path, "/v1/secrets/")] = req.Value
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "GET" && r.URL.Path == "/v1/secrets":
		_ = json.NewEncoder(w).Encode(api.SecretList{Secrets: []api.SecretInfo{{Name: "ANTHROPIC_API_KEY", Updated: done}}})
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such endpoint"}}`))
	}
}

func TestJobRunReadsTheYAMLSpec(t *testing.T) {
	g := &jobsStandIn{secrets: map[string]string{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	useEndpoint(t, srv.URL)
	dir := t.TempDir()
	spec := filepath.Join(dir, "job.yaml")
	os.WriteFile(spec, []byte(`name: nightly
agent: claude
prompts: ["fix a", "fix b"]
parallelism: 2
retries: 1
timeout_secs: 900
secrets: [ANTHROPIC_API_KEY]
keep:
  output: true
  files: [/sandbox/home/report.md]
notify: https://hooks.example.com/x
`), 0o600)
	out, err := runCLI(t, "job", "run", "-f", spec, "--wait")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.Contains(out, "job_0123456789abcdef") || !strings.Contains(out, "succeeded") {
		t.Fatalf("output: %s", out)
	}
	s := g.specs[0]
	if s.Name != "nightly" || s.Agent != "claude" || len(s.Prompts) != 2 || s.Parallelism != 2 || s.Retries != 1 ||
		s.TimeoutSecs != 900 || s.Secrets[0] != "ANTHROPIC_API_KEY" || s.Keep == nil || s.Keep.Files[0] != "/sandbox/home/report.md" ||
		s.Keep.Output == nil || !*s.Keep.Output || s.Notify == "" {
		t.Fatalf("spec sent: %+v", s)
	}
	os.WriteFile(spec, []byte("command: [\"true\"]\ntimeout: 5\n"), 0o600)
	if _, err := runCLI(t, "job", "run", "-f", spec); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("an unknown key: %v", err)
	}
}

func TestAgentRunWaitsAndMirrorsTheExitCode(t *testing.T) {
	g := &jobsStandIn{secrets: map[string]string{}, exit: 3}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	useEndpoint(t, srv.URL)
	old := jobPoll
	jobPoll = 10 * time.Millisecond
	t.Cleanup(func() { jobPoll = old })
	out, err := runCLI(t, "agent-run", "claude", "fix it", "--secret", "ANTHROPIC_API_KEY", "-e", "MODE=fast", "--wait")
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 3 {
		t.Fatalf("err = %v; want exit 3: %s", err, out)
	}
	if !strings.Contains(out, "agent says hi") {
		t.Fatalf("output: %s", out)
	}
	a := g.runs[0]
	if a.Agent != "claude" || a.Prompt != "fix it" || a.Secrets[0] != "ANTHROPIC_API_KEY" || a.Env["MODE"] != "fast" {
		t.Fatalf("request: %+v", a)
	}
	if _, err := runCLI(t, "agent-run", "nope", "x"); err == nil {
		t.Fatal("an unknown agent was sent")
	}
	if _, err := runCLI(t, "agent-run", "claude", "x", "-e", "SANDBOX_RUN_AS=0"); err == nil {
		t.Fatal("a reserved variable was sent")
	}
}

func TestSecretSetReadsStdin(t *testing.T) {
	g := &jobsStandIn{secrets: map[string]string{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	useEndpoint(t, srv.URL)
	root := NewRootCmd()
	root.SetIn(strings.NewReader("sk-value\n"))
	root.SetOut(io.Discard)
	root.SetArgs([]string{"secret", "set", "ANTHROPIC_API_KEY"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if g.secrets["ANTHROPIC_API_KEY"] != "sk-value" {
		t.Fatalf("sent %q", g.secrets["ANTHROPIC_API_KEY"])
	}
	root = NewRootCmd()
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"secret", "set", "X"})
	if err := root.Execute(); err == nil {
		t.Fatal("an empty value was sent")
	}
	out, err := runCLI(t, "secret", "ls")
	if err != nil || !strings.Contains(out, "ANTHROPIC_API_KEY") {
		t.Fatalf("ls: %v %s", err, out)
	}
}
