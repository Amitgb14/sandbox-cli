package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

const testToken = "studio-test-token"

// studioUnderTest is a Studio in front of a real sandboxd handler on the fake
// backend, which requires its own token — so a request that reaches it proves
// the proxy swapped tokens.
func studioUnderTest(t *testing.T) (*Server, *httptest.Server, *api.Client) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	sd := httptest.NewServer((&server.Server{Backend: fake.New(api.CapEgressAllowlist), Policy: spec.DefaultPolicy(), Token: "sandboxd-token"}).Handler())
	t.Cleanup(sd.Close)
	c, err := api.NewClient(sd.URL, "sandboxd-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Client: c, Context: "t", Token: testToken, ReposFile: filepath.Join(t.TempDir(), "repos.json")}
	st := httptest.NewServer(s.Handler())
	t.Cleanup(st.Close)
	return s, st, c
}

func call(t *testing.T, base, method, path, token, origin string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func TestGuard(t *testing.T) {
	_, st, _ := studioUnderTest(t)
	if r, _ := call(t, st.URL, "GET", "/api/info", "", "", nil); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d", r.StatusCode)
	}
	if r, _ := call(t, st.URL, "GET", "/api/info", "wrong", "", nil); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", r.StatusCode)
	}
	if r, _ := call(t, st.URL, "GET", "/api/info", testToken, "http://evil.example", nil); r.StatusCode != http.StatusForbidden {
		t.Errorf("cross origin: %d", r.StatusCode)
	}
	if r, _ := call(t, st.URL, "GET", "/api/info", testToken, "http://"+strings.TrimPrefix(st.URL, "http://"), nil); r.StatusCode != http.StatusOK {
		t.Errorf("same origin: %d", r.StatusCode)
	}
	// DNS rebinding: a page whose own name resolves to 127.0.0.1.
	req, _ := http.NewRequest("GET", st.URL+"/", nil)
	req.Host = "rebind.example:80"
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host: %v %v", r.StatusCode, err)
	}
	// A body that is not JSON, the shape of a cross-origin simple request.
	req, _ = http.NewRequest("POST", st.URL+"/api/repos", strings.NewReader("path=/"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+testToken)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain body: %d", r.StatusCode)
	}
	// The UI is served without a token, and without a build says how to make one.
	r, _ := http.Get(st.URL + "/")
	if r.StatusCode != http.StatusOK || r.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("ui: %d %q", r.StatusCode, r.Header.Get("X-Frame-Options"))
	}
}

// The browser holds Studio's token; sandboxd gets its own. A request that
// reached sandboxd at all proves the swap, since sandboxd refuses Studio's.
func TestProxyCarriesTheContextsToken(t *testing.T) {
	_, st, c := studioUnderTest(t)
	sb, err := c.CreateSandbox(context.Background(), api.CreateSandboxRequest{Labels: map[string]string{"via": "cli"}})
	if err != nil {
		t.Fatal(err)
	}
	r, body := call(t, st.URL, "GET", "/api/v1/sandboxes", testToken, "", nil)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("proxied list: %d %v", r.StatusCode, body)
	}
	list, _ := body["sandboxes"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != sb.ID {
		t.Errorf("list %v", body)
	}
	req, _ := http.NewRequest("GET", st.URL+"/api/v1/sandboxes/"+sb.ID+"/processes/1/attach", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Upgrade", "sbx-stream/1")
	req.Header.Set("Connection", "Upgrade")
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != http.StatusBadRequest {
		t.Errorf("an upgrade through the proxy: %d", r.StatusCode)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@x", "commit", "-q", "--allow-empty", "-m", "base"}} {
		cmd := exec.Command("git", a...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git: %v %s", err, out)
		}
	}
	return dir
}

// A path is checked once, when added; everything else names an id.
func TestReposAreAddedByPathAndUsedById(t *testing.T) {
	_, st, _ := studioUnderTest(t)
	if r, _ := call(t, st.URL, "POST", "/api/repos", testToken, "", map[string]string{"path": t.TempDir()}); r.StatusCode != http.StatusBadRequest {
		t.Errorf("not a repository: %d", r.StatusCode)
	}
	if r, _ := call(t, st.URL, "POST", "/api/repos", testToken, "", map[string]string{"path": "relative/dir"}); r.StatusCode != http.StatusBadRequest {
		t.Errorf("relative path: %d", r.StatusCode)
	}
	repo := gitRepo(t)
	r, body := call(t, st.URL, "POST", "/api/repos", testToken, "", map[string]string{"path": filepath.Join(repo, ".")})
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("add: %d %v", r.StatusCode, body)
	}
	id := body["id"].(string)
	if r, _ := call(t, st.URL, "GET", "/api/repos/"+id+"/refs", testToken, "", nil); r.StatusCode != http.StatusOK {
		t.Errorf("refs by id: %d", r.StatusCode)
	}
	if r, _ := call(t, st.URL, "GET", "/api/repos/000000000000/refs", testToken, "", nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("an unregistered id: %d", r.StatusCode)
	}
	for _, bad := range []string{"HEAD", "refs/heads/main", "refs/sandbox/../heads/main", "refs/sandbox/--output=/tmp/x", "refs/sandbox/a b"} {
		if r, _ := call(t, st.URL, "GET", "/api/repos/"+id+"/diff?ref="+strings.ReplaceAll(bad, " ", "%20"), testToken, "", nil); r.StatusCode != http.StatusBadRequest {
			t.Errorf("diff of %q: %d", bad, r.StatusCode)
		}
	}
	os.RemoveAll(repo)
	if r, _ := call(t, st.URL, "GET", "/api/repos/"+id+"/refs", testToken, "", nil); r.StatusCode != http.StatusGone {
		t.Errorf("a repository gone from disk: %d", r.StatusCode)
	}
}

func TestLaunchValidates(t *testing.T) {
	s, st, _ := studioUnderTest(t)
	repo := gitRepo(t)
	rp, ok := s.RegisterRepo(repo)
	if !ok {
		t.Fatal("register")
	}
	launched := 0
	s.Launch = func(context.Context, string, LaunchRequest) (LaunchResult, error) {
		launched++
		return LaunchResult{Sandbox: "sbx_x"}, nil
	}
	for name, req := range map[string]LaunchRequest{
		"neither":               {Repo: rp.ID},
		"both":                  {Repo: rp.ID, Agent: "claude", Prompt: "x", Command: []string{"true"}},
		"console without agent": {Repo: rp.ID, Console: true, Command: []string{"true"}},
		"headless, no prompt":   {Repo: rp.ID, Agent: "claude"},
		"unverified headless":   {Repo: rp.ID, Agent: "aider", Prompt: "x"},
		"unknown agent":         {Repo: rp.ID, Agent: "nope", Prompt: "x"},
		"unknown repo":          {Repo: "000000000000", Command: []string{"true"}},
	} {
		if r, body := call(t, st.URL, "POST", "/api/runs", testToken, "", req); r.StatusCode < 400 {
			t.Errorf("%s: %d %v", name, r.StatusCode, body)
		}
	}
	if launched != 0 {
		t.Fatalf("an invalid request launched")
	}
	if r, body := call(t, st.URL, "POST", "/api/runs", testToken, "", LaunchRequest{Repo: rp.ID, Agent: "aider", Console: true}); r.StatusCode != http.StatusCreated {
		t.Errorf("a console run of an interactive-only agent: %d %v", r.StatusCode, body)
	}
}

// The terminal bridge: keystrokes in, output out, the exit last.
func TestAttachBridge(t *testing.T) {
	_, st, c := studioUnderTest(t)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimPrefix(st.URL, "http://")
	if _, resp, _ := dialWS(t, addr, fmt.Sprintf("/api/ws/attach?sandbox=%s&pid=%d", sb.ID, p.PID)); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a WebSocket without the token: %d", resp.StatusCode)
	}
	conn, resp, br := dialWS(t, addr, fmt.Sprintf("/api/ws/attach?sandbox=%s&pid=%d&token=%s", sb.ID, p.PID, testToken))
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake: %d", resp.StatusCode)
	}
	sendMasked := func(v any) {
		b, _ := json.Marshal(v)
		mask := []byte{9, 8, 7, 6}
		for i := range b {
			b[i] ^= mask[i%4]
		}
		head := []byte{0x81}
		if len(b) < 126 {
			head = append(head, byte(0x80|len(b)))
		}
		conn.Write(append(append(head, mask...), b...))
	}
	sendMasked(map[string]string{"type": "input", "data": "hello studio\n"})
	var out strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "hello studio") && time.Now().Before(deadline) {
		op, payload := readServerFrame(t, br)
		if op != wsOpText {
			continue
		}
		var m map[string]any
		_ = json.Unmarshal(payload, &m)
		if m["type"] == "output" {
			out.WriteString(m["data"].(string))
		}
	}
	if !strings.Contains(out.String(), "hello studio") {
		t.Fatalf("output %q", out.String())
	}
	sendMasked(map[string]string{"type": "signal", "signal": "INT"})
	for time.Now().Before(deadline) {
		op, payload := readServerFrame(t, br)
		var m map[string]any
		if op == wsOpText && json.Unmarshal(payload, &m) == nil && m["type"] == "exit" {
			return
		}
	}
	t.Fatal("no exit message")
}
