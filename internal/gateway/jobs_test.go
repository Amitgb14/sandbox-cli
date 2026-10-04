package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

var testSecretsKey = bytes.Repeat([]byte{7}, 32)

func withKey(c *Config) { c.SecretsKey = testSecretsKey }

// jobUser is a user who may run jobs and set secrets.
func (tg *testGateway) jobUser(name string) *api.Client {
	return tg.client(name, "", ScopeRead, ScopeCreate, ScopeDelete, ScopeSecretsWrite)
}

func waitJob(t *testing.T, c *api.Client, id string, what string, cond func(api.Job) bool) api.Job {
	t.Helper()
	var j api.Job
	deadline := time.Now().Add(20 * time.Second)
	for {
		var err error
		j, err = c.Job(context.Background(), id)
		if err != nil {
			t.Fatalf("job %s: %v", id, err)
		}
		if cond(j) {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; job is %+v", what, j)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func finished(j api.Job) bool { return j.State != api.JobRunning }

func TestSecretsAreSealedAndNeverReturned(t *testing.T) {
	tg := startGateway(t, withKey, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	const value = "sk-the-value-0123456789-do-not-leak"
	if err := ana.SetSecret(ctx, "PROVIDER_KEY", value); err != nil {
		t.Fatal(err)
	}
	list, err := ana.Secrets(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "PROVIDER_KEY" || list[0].Updated.IsZero() {
		t.Fatalf("secrets = %+v, %v", list, err)
	}
	// Nowhere on the wire, nowhere in the state file.
	resp := tg.raw(http.MethodGet, "/v1/secrets", "", "")
	resp.Body.Close()
	sec, _, _ := tg.store.CreateKey("ana", "", []string{ScopeRead})
	resp = tg.raw(http.MethodGet, "/v1/secrets", sec, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), value) {
		t.Fatalf("the listing holds the value: %s", body)
	}
	state, err := os.ReadFile(tg.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(state, []byte(value)) || bytes.Contains(state, []byte(strings.ToUpper(value))) {
		t.Fatal("the state file holds a secret's plaintext")
	}
	if !bytes.Contains(state, []byte("PROVIDER_KEY")) {
		t.Fatal("the state file does not hold the secret at all")
	}
	// Sealed to its tenant and name: moved to another name, it does not open.
	sealed, _ := tg.store.SealedSecret("", "PROVIDER_KEY")
	if err := tg.store.PutSecret("", "OTHER", sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := tg.g.secretValues("", []string{"OTHER"}); err == nil || strings.Contains(err.Error(), value) {
		t.Fatalf("a sealed value moved to another name: %v", err)
	}

	// Reserved and malformed names are refused; so is a key without the scope.
	for _, name := range []string{"SANDBOX_RUN_AS", "1BAD", "has-dash"} {
		if err := ana.SetSecret(ctx, name, "x"); err == nil {
			t.Errorf("secret %s was accepted", name)
		}
	}
	reader := tg.client("ana", "", ScopeRead, ScopeCreate)
	wantCode(t, reader.SetSecret(ctx, "X", "y"), api.CodeRefused)
	wantCode(t, reader.DeleteSecret(ctx, "PROVIDER_KEY"), api.CodeRefused)

	// Per tenant: another tenant does not see it.
	other := tg.client("bo", "t2", ScopeRead, ScopeSecretsWrite)
	if l, _ := other.Secrets(ctx); len(l) != 0 {
		t.Fatalf("another tenant sees %+v", l)
	}
	wantCode(t, other.DeleteSecret(ctx, "PROVIDER_KEY"), api.CodeNotFound)

	if err := ana.DeleteSecret(ctx, "PROVIDER_KEY"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, ana.DeleteSecret(ctx, "PROVIDER_KEY"), api.CodeNotFound)
}

func TestSecretsNeedAKey(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	wantCode(t, ana.SetSecret(ctx, "K", "v"), api.CodeUnsupported)
	_, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"true"}, Secrets: []string{"K"}})
	wantCode(t, err, api.CodeUnsupported)
}

func TestLoadSecretsKey(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw")
	os.WriteFile(raw, testSecretsKey, 0o600)
	hexf := filepath.Join(dir, "hex")
	os.WriteFile(hexf, []byte(strings.Repeat("ab", 32)+"\n"), 0o600)
	short := filepath.Join(dir, "short")
	os.WriteFile(short, []byte("short"), 0o600)
	open := filepath.Join(dir, "open")
	os.WriteFile(open, testSecretsKey, 0o644)
	if k, err := LoadSecretsKey(raw); err != nil || !bytes.Equal(k, testSecretsKey) {
		t.Fatalf("raw: %v", err)
	}
	if k, err := LoadSecretsKey(hexf); err != nil || len(k) != 32 || k[0] != 0xab {
		t.Fatalf("hex: %v", err)
	}
	if _, err := LoadSecretsKey(short); err == nil {
		t.Fatal("a short key was accepted")
	}
	if _, err := LoadSecretsKey(open); err == nil {
		t.Fatal("a key others can read was accepted")
	}
}

func TestJobRunsCommandAndKeepsOutput(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, withKey, n1)
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	j, err := ana.CreateJob(ctx, api.JobSpec{Name: "hello", Command: []string{"echo", "hello", "fleet"}, Completions: 3, Parallelism: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !jobIDRE.MatchString(j.ID) || len(j.Runs) != 3 || j.State != api.JobRunning {
		t.Fatalf("created %+v", j)
	}
	j = waitJob(t, ana, j.ID, "the job to end", finished)
	if j.State != api.JobSucceeded || j.Succeeded != 3 || j.Finished == nil || j.Expires == nil {
		t.Fatalf("job = %+v", j)
	}
	for _, r := range j.Runs {
		if r.State != api.RunSucceeded || r.ExitCode == nil || *r.ExitCode != 0 || r.Attempts != 1 || r.Sandbox == "" {
			t.Fatalf("run = %+v", r)
		}
		out, err := ana.JobOutput(ctx, j.ID, r.N)
		if err != nil || string(out.Stdout) != "hello fleet\n" {
			t.Fatalf("output of run %d = %q, %v", r.N, out.Stdout, err)
		}
		// Its sandbox carried the job's label, and is gone.
		if _, ok := tg.store.OwnerOf(r.Sandbox); ok {
			t.Errorf("run %d's sandbox is still recorded", r.N)
		}
	}
	if n1.count() != 0 {
		t.Fatalf("%d sandboxes left on the node", n1.count())
	}
	if l, err := ana.Jobs(ctx); err != nil || len(l) != 1 || l[0].Runs != nil || l[0].Succeeded != 3 {
		t.Fatalf("list = %+v, %v", l, err)
	}
	if _, err := ana.JobOutput(ctx, j.ID, 3); !api.IsCode(err, api.CodeNotFound) {
		t.Fatalf("run 3: %v", err)
	}
}

func TestJobLabelsItsSandboxes(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "1"}})
	if err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, ana, j.ID, "the run to start", func(j api.Job) bool { return j.Runs[0].PID != 0 })
	sb, err := ana.Sandbox(ctx, j.Runs[0].Sandbox)
	if err != nil {
		t.Fatal(err)
	}
	if sb.Labels[LabelJob] != j.ID || sb.Labels[LabelJobRun] != "0" || sb.Labels[LabelOwner] != "ana" {
		t.Fatalf("labels = %v", sb.Labels)
	}
	// A client cannot set them.
	_, err = ana.CreateSandbox(ctx, api.CreateSandboxRequest{Labels: map[string]string{LabelJob: j.ID}})
	wantCode(t, err, api.CodeInvalidRequest)
	waitJob(t, ana, j.ID, "the job to end", finished)
}

func TestJobParallelismIsRespected(t *testing.T) {
	n1, n2 := startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...)
	tg := startGateway(t, nil, n1, n2)
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	var peak, now atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			c := int64(n1.count() + n2.count())
			now.Store(c)
			for {
				p := peak.Load()
				if c <= p || peak.CompareAndSwap(p, c) {
					break
				}
			}
		}
	}()
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "0.3"}, Completions: 6, Parallelism: 2})
	if err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, ana, j.ID, "the job to end", finished)
	close(stop)
	wg.Wait()
	if j.State != api.JobSucceeded || j.Succeeded != 6 {
		t.Fatalf("job = %+v", j)
	}
	if p := peak.Load(); p > 2 || p < 2 {
		t.Fatalf("peak sandboxes = %d; want 2 (parallelism)", p)
	}
}

func TestJobRetriesAFailingRun(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"false"}, Retries: 2})
	if err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, ana, j.ID, "the job to end", finished)
	r := j.Runs[0]
	if j.State != api.JobFailed || r.State != api.RunFailed || r.Attempts != 3 || r.ExitCode == nil || *r.ExitCode != 1 {
		t.Fatalf("job = %+v run = %+v", j, r)
	}
	// A run that succeeds is not retried.
	j, _ = ana.CreateJob(ctx, api.JobSpec{Command: []string{"true"}, Retries: 5})
	j = waitJob(t, ana, j.ID, "the job to end", finished)
	if j.State != api.JobSucceeded || j.Runs[0].Attempts != 1 {
		t.Fatalf("job = %+v", j)
	}
}

func TestJobTimeoutKillsTheRun(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	start := time.Now()
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "30"}, TimeoutSecs: 1})
	if err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, ana, j.ID, "the job to end", finished)
	if el := time.Since(start); el > 10*time.Second {
		t.Fatalf("took %v", el)
	}
	r := j.Runs[0]
	if j.State != api.JobFailed || r.State != api.RunTimedOut || r.ExitCode != nil {
		t.Fatalf("run = %+v", r)
	}
	if n1.count() != 0 {
		t.Fatal("the timed-out run's sandbox is still there")
	}
}

func TestCancelTerminatesRunningSandboxes(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "60"}, Completions: 4, Parallelism: 2})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, ana, j.ID, "two runs to start", func(j api.Job) bool {
		return j.Runs[0].PID != 0 && j.Runs[1].PID != 0
	})
	if n1.count() != 2 {
		t.Fatalf("%d sandboxes; want 2", n1.count())
	}
	// Another user cannot.
	_, err = tg.jobUser("bob").CancelJob(ctx, j.ID)
	wantCode(t, err, api.CodeNotFound)

	j, err = ana.CancelJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != api.JobCancelled {
		t.Fatalf("job = %+v", j)
	}
	for _, r := range j.Runs {
		if r.State != api.RunCancelled {
			t.Fatalf("run = %+v", r)
		}
	}
	if n1.count() != 0 {
		t.Fatalf("%d sandboxes left after the cancel", n1.count())
	}
	// Again: nothing changes.
	if j2, err := ana.CancelJob(ctx, j.ID); err != nil || j2.State != api.JobCancelled {
		t.Fatalf("second cancel: %+v %v", j2, err)
	}
}

func TestJobKeepsFiles(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "0.5"},
		Keep: &api.JobKeep{Files: []string{"/sandbox/home/report.txt", "/sandbox/home/missing"}}})
	if err != nil {
		t.Fatal(err)
	}
	// The fake's builtins cannot write a file; the test writes it into the
	// run's sandbox through the node, as the command would have.
	j = waitJob(t, ana, j.ID, "the run to start", func(j api.Job) bool { return j.Runs[0].PID != 0 })
	node := api.NewClientWithHTTP(n1.ts.URL, n1.token, n1.ts.Client())
	if err := node.WriteFile(ctx, j.Runs[0].Sandbox, "/sandbox/home/report.txt", []byte("the report")); err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, ana, j.ID, "the job to end", finished)
	files := j.Runs[0].Files
	if len(files) != 2 || files[0].Size != 10 || files[0].Error != "" || files[1].Error == "" {
		t.Fatalf("files = %+v", files)
	}
	data, err := ana.JobFile(ctx, j.ID, 0, "/sandbox/home/report.txt")
	if err != nil || string(data) != "the report" {
		t.Fatalf("file = %q, %v", data, err)
	}
	if _, err := ana.JobFile(ctx, j.ID, 0, "/sandbox/home/missing"); !api.IsCode(err, api.CodeNotFound) {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := ana.JobFile(ctx, j.ID, 0, "/etc/passwd"); !api.IsCode(err, api.CodeNotFound) {
		t.Fatalf("a file not kept: %v", err)
	}
	// keep.output false keeps none.
	off := false
	j, _ = ana.CreateJob(ctx, api.JobSpec{Command: []string{"echo", "x"}, Keep: &api.JobKeep{Output: &off}})
	waitJob(t, ana, j.ID, "the job to end", finished)
	if _, err := ana.JobOutput(ctx, j.ID, 0); !api.IsCode(err, api.CodeNotFound) {
		t.Fatalf("output kept against keep.output=false: %v", err)
	}
}

func TestJobsBelongToTheirCreator(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana, bob := tg.jobUser("ana"), tg.jobUser("bob")
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"echo", "mine"}})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, ana, j.ID, "the job to end", finished)
	_, err = bob.Job(ctx, j.ID)
	wantCode(t, err, api.CodeNotFound)
	_, err = bob.JobOutput(ctx, j.ID, 0)
	wantCode(t, err, api.CodeNotFound)
	if l, _ := bob.Jobs(ctx); len(l) != 0 {
		t.Fatalf("bob lists %+v", l)
	}
	// Scopes: create needs sandbox:create; reading needs sandbox:read.
	ro := tg.client("ana", "", ScopeRead)
	_, err = ro.CreateJob(ctx, api.JobSpec{Command: []string{"true"}})
	wantCode(t, err, api.CodeRefused)
	_, err = ro.CancelJob(ctx, j.ID)
	wantCode(t, err, api.CodeRefused)
	if _, err := ro.Job(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	wo := tg.client("ana", "", ScopeCreate)
	_, err = wo.Jobs(ctx)
	wantCode(t, err, api.CodeRefused)
	// An admin may look.
	if _, err := tg.admin.Job(ctx, j.ID); err != nil {
		t.Fatalf("admin: %v", err)
	}
}

func TestJobSpecIsChecked(t *testing.T) {
	tg := startGateway(t, withKey, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	for name, s := range map[string]api.JobSpec{
		"nothing":          {},
		"both":             {Command: []string{"true"}, Agent: "claude", Prompt: "x"},
		"unknown agent":    {Agent: "nope", Prompt: "x"},
		"agent no prompt":  {Agent: "claude"},
		"prompt and batch": {Agent: "claude", Prompt: "x", Prompts: []string{"y"}},
		"command prompts":  {Command: []string{"true"}, Prompts: []string{"y"}},
		"batch mismatch":   {Agent: "claude", Prompts: []string{"a", "b"}, Completions: 3},
		"parallelism":      {Command: []string{"true"}, Parallelism: -1},
		"too many":         {Command: []string{"true"}, Completions: maxJobRuns + 1},
		"retries":          {Command: []string{"true"}, Retries: -1},
		"timeout":          {Command: []string{"true"}, TimeoutSecs: -1},
		"env name":         {Command: []string{"true"}, Env: map[string]string{"A=B": "c"}},
		"no secret":        {Command: []string{"true"}, Secrets: []string{"MISSING"}},
		"secret and env":   {Command: []string{"true"}, Env: map[string]string{"K": "v"}, Secrets: []string{"K"}},
		"relative file":    {Command: []string{"true"}, Keep: &api.JobKeep{Files: []string{"report.txt"}}},
		"unclean file":     {Command: []string{"true"}, Keep: &api.JobKeep{Files: []string{"/a/../b"}}},
		"http notify":      {Command: []string{"true"}, Notify: "http://example.com/hook"},
		"ftp notify":       {Command: []string{"true"}, Notify: "ftp://example.com/hook"},
		"name":             {Name: "Not A Name", Command: []string{"true"}},
	} {
		if name == "secret and env" {
			if err := ana.SetSecret(ctx, "K", "v"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := ana.CreateJob(ctx, s); !api.IsCode(err, api.CodeInvalidRequest) {
			t.Errorf("%s: %v; want invalid_request", name, err)
		}
	}
	_, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"true"}, Env: map[string]string{"SANDBOX_RUN_AS": "0"}})
	wantCode(t, err, api.CodeRefused)
	for _, u := range []string{"https://hooks.example.com/x", "http://127.0.0.1:9/x", "http://localhost/x", "http://[::1]:8/x"} {
		if err := checkNotifyURL(u, true); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	// Without the operator's leave, nothing but a public address.
	if err := checkNotifyURL("https://hooks.example.com/x", false); err != nil {
		t.Error(err)
	}
	for _, u := range []string{"http://127.0.0.1:9/x", "http://localhost/x", "https://localhost./x", "https://app.localhost/x",
		"https://127.0.0.1/x", "https://[::1]/x", "https://10.0.0.5/x", "https://192.168.1.1/x", "https://172.16.0.1/x",
		"https://169.254.169.254/x", "https://[fe80::1]/x", "https://[fd00::1]/x", "https://100.64.0.1/x",
		"https://0.0.0.0/x", "https://[::ffff:127.0.0.1]/x", "https://224.0.0.1/x"} {
		if err := checkNotifyURL(u, false); err == nil {
			t.Errorf("%s was accepted without --notify-allow-private", u)
		}
	}
	// A gateway.* label cannot be smuggled through the create path.
	if _, f := tg.g.create(ctx, Principal{User: "ana", Scopes: []string{ScopeCreate}}, api.CreateSandboxRequest{},
		map[string]string{LabelJob: "job_0000000000000000"}); f.status != http.StatusCreated {
		t.Fatalf("internal create: %d %s", f.status, f.body)
	}
}

// Secrets and the job's environment reach the run, and nothing else: not
// the job's record, not a response, not the state file.
func TestJobSecretsReachTheRunOnly(t *testing.T) {
	tg := startGateway(t, withKey, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	const secret, plain = "secret-value-7f3a9", "env-value-2b8c1"
	if err := ana.SetSecret(ctx, "API_TOKEN", secret); err != nil {
		t.Fatal(err)
	}
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"printenv"}, Secrets: []string{"API_TOKEN"},
		Env: map[string]string{"PLAIN": plain}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(j)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), plain) {
		t.Fatalf("the job's record holds a value: %s", raw)
	}
	if !slices.Equal(j.EnvNames, []string{"PLAIN"}) {
		t.Fatalf("env names = %v", j.EnvNames)
	}
	j = waitJob(t, ana, j.ID, "the job to end", finished)
	out, err := ana.JobOutput(ctx, j.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"API_TOKEN=" + secret, "PLAIN=" + plain, "GIT_AUTHOR_NAME=sandbox"} {
		if !strings.Contains(string(out.Stdout), want+"\n") {
			t.Errorf("the run's environment lacks %s:\n%s", want, out.Stdout)
		}
	}
	state, _ := os.ReadFile(tg.store.path)
	if bytes.Contains(state, []byte(secret)) || bytes.Contains(state, []byte(plain)) {
		t.Fatal("the state file holds a value")
	}
	raw, _ = json.Marshal(waitJob(t, ana, j.ID, "", finished))
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), plain) {
		t.Fatalf("GET /v1/jobs/{id} holds a value: %s", raw)
	}
}

func TestNotifyPostsStatesOnly(t *testing.T) {
	tg := startGateway(t, func(c *Config) { withKey(c); c.NotifyAllowPrivate = true }, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	var mu sync.Mutex
	var got []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, string(b))
		mu.Unlock()
	}))
	t.Cleanup(hook.Close)
	const secret, plain, output = "secret-9d1e", "plain-4c2a", "OUTPUT-MARK-5e6f"
	if err := ana.SetSecret(ctx, "TOKEN", secret); err != nil {
		t.Fatal(err)
	}
	j, err := ana.CreateJob(ctx, api.JobSpec{Name: "hooked", Command: []string{"echo", output}, Completions: 2,
		Secrets: []string{"TOKEN"}, Env: map[string]string{"PLAIN": plain}, Notify: hook.URL + "/hook"})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, ana, j.ID, "the job to end", finished)
	waitFor(t, "three notifications", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 3 })
	mu.Lock()
	defer mu.Unlock()
	events := map[string]int{}
	for _, b := range got {
		for _, bad := range []string{secret, plain, output} {
			if strings.Contains(b, bad) {
				t.Fatalf("a notification holds %q: %s", bad, b)
			}
		}
		var n api.JobNotification
		if err := json.Unmarshal([]byte(b), &n); err != nil || n.Job != j.ID || n.Name != "hooked" {
			t.Fatalf("notification %s: %v", b, err)
		}
		events[n.Event]++
		if n.Event == "run.finished" && (n.Run == nil || n.State != api.RunSucceeded) {
			t.Fatalf("run notification %s", b)
		}
		if n.Event == "job.finished" && n.JobState != api.JobSucceeded {
			t.Fatalf("job notification %s", b)
		}
	}
	if events["run.finished"] != 2 || events["job.finished"] != 1 {
		t.Fatalf("events = %v", events)
	}
}

// A notify URL is the job owner's text, and the gateway sits on the
// operator's network. Once, a job naming https://127.0.0.1:PORT had the
// gateway connect there when a run ended: any user could make it reach its
// own loopback, the nodes' network or a metadata address. Now the address is
// refused when the job is submitted, and a name is checked again as it is
// dialled, after resolving.
func TestNotifyReachesOnlyPublicAddresses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var accepted atomic.Int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"true"}, Notify: fmt.Sprintf("https://127.0.0.1:%d/hook", port)})
	if err == nil {
		waitJob(t, ana, j.ID, "the job to end", finished)
		time.Sleep(200 * time.Millisecond)
	}
	if !api.IsCode(err, api.CodeInvalidRequest) {
		t.Errorf("a job notifying a loopback address: %v; want invalid_request", err)
	}
	if n := accepted.Load(); n != 0 {
		t.Errorf("the gateway connected to a loopback address %d times for a job's notify", n)
	}

	// A name that resolves to loopback is refused as it is dialled.
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://localhost:%d/hook", port), strings.NewReader("{}"))
	if resp, err := newNotifyClient(false).Do(req); err == nil {
		resp.Body.Close()
		t.Error("the notify client posted to a name resolving to loopback")
	} else if !strings.Contains(err.Error(), "not a public address") {
		t.Errorf("refused for another reason: %v", err)
	}
	if n := accepted.Load(); n != 0 {
		t.Errorf("the notify client connected to loopback %d times", n)
	}
	// The operator's leave lets it through: the trap was armed.
	req, _ = http.NewRequest(http.MethodPost, fmt.Sprintf("http://localhost:%d/hook", port), strings.NewReader("{}"))
	if resp, err := newNotifyClient(true).Do(req); err == nil {
		resp.Body.Close()
	}
	waitFor(t, "a connection with --notify-allow-private", func() bool { return accepted.Load() > 0 })
}

func TestABatchOf200RunsCompletes(t *testing.T) {
	n1, n2 := startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...)
	tg := startGateway(t, nil, n1, n2)
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"echo", "ok"}, Completions: 200, Parallelism: 25})
	if err != nil {
		t.Fatal(err)
	}
	var done api.Job
	deadline := time.Now().Add(2 * time.Minute)
	for {
		done, err = ana.Job(ctx, j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if finished(done) || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if done.State != api.JobSucceeded || done.Succeeded != 200 {
		t.Fatalf("job: %s, %d succeeded, %d failed", done.State, done.Succeeded, done.Failed)
	}
	if n1.count()+n2.count() != 0 {
		t.Fatal("sandboxes left behind")
	}
}

// A job wider than the tenant's quota queues rather than failing.
func TestJobWaitsForQuota(t *testing.T) {
	tg := startGateway(t, func(c *Config) { c.Quota.Sandboxes = 1 }, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "0.2"}, Completions: 3, Parallelism: 3})
	if err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, ana, j.ID, "the job to end", finished)
	if j.State != api.JobSucceeded {
		t.Fatalf("job = %+v", j)
	}
	for _, r := range j.Runs {
		if r.Attempts != 1 {
			t.Fatalf("run %d spent %d attempts waiting for quota", r.N, r.Attempts)
		}
	}
}

func TestAgentRunIsAOneRunJob(t *testing.T) {
	tg := startGateway(t, withKey, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	if err := ana.SetSecret(ctx, "ANTHROPIC_API_KEY", "sk-test"); err != nil {
		t.Fatal(err)
	}
	j, err := ana.AgentRun(ctx, api.AgentRunRequest{Agent: "claude", Prompt: "fix the tests", Secrets: []string{"ANTHROPIC_API_KEY"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Runs) != 1 || j.Spec.Agent != "claude" || j.Spec.Prompt != "fix the tests" || j.Spec.Completions != 1 {
		t.Fatalf("job = %+v", j)
	}
	// The fake runs no agent: the start is refused and the run fails.
	j = waitJob(t, ana, j.ID, "the job to end", finished)
	if j.State != api.JobFailed || !strings.Contains(j.Runs[0].Error, "starting the command") {
		t.Fatalf("run = %+v", j.Runs[0])
	}
	_, err = ana.AgentRun(ctx, api.AgentRunRequest{Agent: "claude"})
	wantCode(t, err, api.CodeInvalidRequest)

	// What a run is made of: the agent's verified headless argv in the
	// guest's home, the secret by its name, the provider on the allowlist.
	rec, _ := tg.store.Job(j.ID)
	rec.Spec.Network = &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com"}}
	req, argv, cwd := tg.g.runRequest(rec, 0, map[string]string{"A": "b"}, map[string]string{"ANTHROPIC_API_KEY": "sk-test"})
	d, _ := agents.Lookup("claude")
	if !slices.Equal(argv, d.Autonomous("fix the tests", nil)) || cwd != agenthome.GuestHome {
		t.Fatalf("argv %q cwd %q", argv, cwd)
	}
	if req.Env["ANTHROPIC_API_KEY"] != "sk-test" || req.Env["A"] != "b" {
		t.Fatalf("env names %v", slices.Sorted(mapsKeys(req.Env)))
	}
	if !slices.Equal(req.Network.Allow, []string{"github.com", "api.anthropic.com"}) {
		t.Fatalf("allow = %v", req.Network.Allow)
	}
	// A batch gives each run its own prompt.
	rec.Spec.Prompt, rec.Spec.Prompts = "", []string{"one", "two"}
	_, argv, _ = tg.g.runRequest(rec, 1, nil, nil)
	if !slices.Equal(argv, d.Autonomous("two", nil)) {
		t.Fatalf("batch argv %q", argv)
	}
}

func mapsKeys(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// A gateway that stops while a run's command runs picks it up again where
// it runs; the job finishes as if nothing had happened.
func TestJobResumesAfterARestart(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	st, err := OpenFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := Config{Store: st, PollInterval: 100 * time.Millisecond, FailAfter: 2, Logf: t.Logf,
		StaticNodes: []NodeConfig{n1.config()}, SecretsKey: testSecretsKey}
	g1, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	g1.Start(context.Background())
	ts := httptest.NewServer(g1.Handler())
	secret, _, _ := st.CreateKey("ana", "", []string{ScopeRead, ScopeCreate})
	c := api.NewClientWithHTTP(ts.URL, secret, ts.Client())
	ctx := ctxT(t)
	j, err := c.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "1"}, Completions: 2, Parallelism: 1,
		Env: map[string]string{"KEPT": "sealed"}})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, c, j.ID, "the first run to start", func(j api.Job) bool { return j.Runs[0].PID != 0 })
	g1.Close()
	ts.Close()
	rec, _ := st.Job(j.ID)
	if rec.State != api.JobRunning || rec.Runs[0].State != api.RunRunning || rec.Runs[1].State != api.RunQueued {
		t.Fatalf("after the stop: %+v", rec)
	}
	if n1.count() != 1 {
		t.Fatalf("%d sandboxes; the run's should still be there", n1.count())
	}

	g2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	g2.Start(context.Background())
	t.Cleanup(g2.Close)
	ts2 := httptest.NewServer(g2.Handler())
	t.Cleanup(ts2.Close)
	c = api.NewClientWithHTTP(ts2.URL, secret, ts2.Client())
	j = waitJob(t, c, j.ID, "the job to end", finished)
	if j.State != api.JobSucceeded || j.Runs[0].Attempts != 1 || j.Runs[1].Attempts != 1 {
		t.Fatalf("job = %+v", j)
	}
	if n1.count() != 0 {
		t.Fatal("sandboxes left behind")
	}
}

// Without a secrets key a job's environment lives in memory only, and a
// restarted gateway says so rather than running without it.
func TestUnsealedEnvIsLostOnRestart(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	st, err := OpenFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rec := &jobRecord{ID: newID("job_"), User: "ana", Spec: api.JobSpec{Command: []string{"true"}, Completions: 1,
		Parallelism: 1, TimeoutSecs: 10}, EnvNames: []string{"X"}, State: api.JobRunning, Created: time.Now(),
		Runs: []api.JobRun{{N: 0, State: api.RunQueued}}}
	if err := st.PutJob(rec); err != nil {
		t.Fatal(err)
	}
	g2, err := New(Config{Store: st, PollInterval: 100 * time.Millisecond, Logf: t.Logf, StaticNodes: []NodeConfig{n1.config()}})
	if err != nil {
		t.Fatal(err)
	}
	g2.Start(context.Background())
	t.Cleanup(g2.Close)
	waitFor(t, "the job to fail", func() bool { r, _ := st.Job(rec.ID); return r.State != api.JobRunning })
	r, _ := st.Job(rec.ID)
	if r.State != api.JobFailed || r.Runs[0].Attempts != 1 || !strings.Contains(r.Runs[0].Error, "lost when the gateway restarted") {
		t.Fatalf("job = %+v", r)
	}
}

func TestFinishedJobsAreForgotten(t *testing.T) {
	tg := startGateway(t, func(c *Config) { c.JobRetention = 300 * time.Millisecond }, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"echo", "x"}})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, ana, j.ID, "the job to end", finished)
	dir := filepath.Join(tg.g.cfg.JobsDir, j.ID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("nothing kept: %v", err)
	}
	waitFor(t, "the job to be forgotten", func() bool { _, err := ana.Job(ctx, j.ID); return api.IsCode(err, api.CodeNotFound) })
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("what it kept is still there: %v", err)
	}
}

func TestCappedKeepsTheStart(t *testing.T) {
	c := capped{max: 5}
	c.Write([]byte("abc"))
	c.Write([]byte("defgh"))
	c.Write([]byte("ij"))
	if c.buf.String() != "abcde" || !c.cut {
		t.Fatalf("%q %v", c.buf.String(), c.cut)
	}
}

// A hook's URL often carries its credential (.../hooks/T0K3N, ?token=...). A
// notification that failed once logged the URL whole, as the HTTP client's
// error quotes it, putting the job owner's credential in the operator's log.
func TestNotifyFailureLogsNoURL(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens: every try fails to connect
	var logs syncBuf
	tg := startGateway(t, func(c *Config) { c.NotifyAllowPrivate = true; c.Logf = logs.logf }, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.jobUser("ana")
	const cred = "hookcred-5f1e9a"
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"true"}, Notify: "http://" + addr + "/hooks/" + cred + "?token=" + cred})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, ana, j.ID, "the job to end", finished)
	waitFor(t, "a failed notification to be logged", func() bool { return strings.Contains(logs.String(), "notify (job.finished)") })
	if strings.Contains(logs.String(), cred) {
		t.Fatalf("the log holds the notify URL's credential:\n%s", logs.String())
	}
}
