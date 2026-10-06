package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
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
	sd := httptest.NewServer((&server.Server{Backend: fake.New(api.CapEgressAllowlist), Policy: spec.DefaultPolicyFor(fake.New(api.CapEgressAllowlist).Capabilities()), Token: "sandboxd-token"}).Handler())
	t.Cleanup(sd.Close)
	c, err := api.NewClient(sd.URL, "sandboxd-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Client: c, Context: "t", Token: testToken}
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
	req, _ = http.NewRequest("POST", st.URL+"/api/runs", strings.NewReader("command=true"))
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

func TestLaunchValidates(t *testing.T) {
	s, st, _ := studioUnderTest(t)
	launched := 0
	var last LaunchRequest
	s.Launch = func(_ context.Context, req LaunchRequest) (LaunchResult, error) {
		launched++
		last = req
		return LaunchResult{Sandbox: "sbx_x"}, nil
	}
	for name, req := range map[string]LaunchRequest{
		"neither":               {},
		"both":                  {Agent: "claude", Prompt: "x", Command: []string{"true"}},
		"console without agent": {Console: true, Command: []string{"true"}},
		"headless, no prompt":   {Agent: "claude"},
		"unverified headless":   {Agent: "goose", Prompt: "x"},
		"unverified console":    {Agent: "goose", Console: true},
		"unknown agent":         {Agent: "nope", Prompt: "x"},
		"image and snapshot":    {Command: []string{"true"}, Image: "img:1", Snapshot: "snp_1"},
	} {
		if r, body := call(t, st.URL, "POST", "/api/runs", testToken, "", req); r.StatusCode < 400 {
			t.Errorf("%s: %d %v", name, r.StatusCode, body)
		}
	}
	// A launch that still names a repository, or asks for the commits to be
	// made as the user, is refused rather than run without what it asked for.
	for _, field := range []string{`"repo":"0123456789ab"`, `"git":true`} {
		if r, body := call(t, st.URL, "POST", "/api/runs", testToken, "", json.RawMessage(`{"command":["true"],`+field+`}`)); r.StatusCode != http.StatusBadRequest {
			t.Errorf("a launch with %s: %d %v", field, r.StatusCode, body)
		}
	}
	if launched != 0 {
		t.Fatalf("an invalid request launched")
	}
	for name, req := range map[string]LaunchRequest{
		"a command":                         {Command: []string{"true"}},
		"a console run of a verified agent": {Agent: "claude", Console: true},
		"a command from an image":           {Command: []string{"true"}, Image: "img:1"},
		"a command from a snapshot":         {Command: []string{"true"}, Snapshot: "snp_1"},
	} {
		if r, body := call(t, st.URL, "POST", "/api/runs", testToken, "", req); r.StatusCode != http.StatusCreated {
			t.Errorf("%s: %d %v", name, r.StatusCode, body)
		}
		// What was asked for reaches the launcher, which hands it to sandboxd
		// to decide on, as the CLI's --image and --from-snapshot do.
		if last.Image != req.Image || last.Snapshot != req.Snapshot {
			t.Errorf("%s: launched %+v", name, last)
		}
	}
}

// Studio lists exactly the agents with a verified headless mode; the
// interactive-only wrappers stay in the CLI.
func TestAgentsListsOnlyVerified(t *testing.T) {
	_, st, _ := studioUnderTest(t)
	r, _ := call(t, st.URL, "GET", "/api/agents", testToken, "", nil)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("status %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", st.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct{ Agents []Agent }
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range got.Agents {
		names = append(names, a.Name)
	}
	if want := agents.Names(); !slices.Equal(names, want) {
		t.Errorf("listed %v, want %v", names, want)
	}
	if len(agents.InteractiveNames()) <= len(names) {
		t.Fatalf("no interactive-only agent to leave out; the test proves nothing")
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

// The dashboard's "waiting for you": every live agent sandbox with what its
// agent is doing, decided by the CLI's code, and nothing for a sandbox no
// agent run started. Behind the token, like every /api route.
func TestAgentStatesListsAgentSandboxes(t *testing.T) {
	_, st, c := studioUnderTest(t)
	ctx := context.Background()
	agent, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Labels: map[string]string{"agent": "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	c.StartProcess(ctx, agent.ID, api.RunRequest{Argv: []string{"sleep", "30"}, Tty: true})
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{}); err != nil {
		t.Fatal(err)
	}
	if r, _ := call(t, st.URL, "GET", "/api/agents/state", "", "", nil); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("without the token: %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", st.URL+"/api/agents/state", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got []AgentState
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	// No conversation written yet: unknown, with the reason, rather than a guess.
	if len(got) != 1 || got[0].Sandbox != agent.ID || got[0].Agent != "claude" || got[0].State != "unknown" || got[0].Why == "" {
		t.Errorf("got %+v", got)
	}
}

// Each agent says where its login is kept, which API it always reaches, and
// which of the variables it reads are set where Studio runs — by name, and
// never with a value, which a launch forwards and nothing else may show.
func TestAgentsDescribeTheirLoginWithoutValues(t *testing.T) {
	const secret = "sk-test-do-not-show-0123456789"
	t.Setenv("ANTHROPIC_API_KEY", secret)
	os.Unsetenv("ANTHROPIC_AUTH_TOKEN")
	_, st, _ := studioUnderTest(t)
	req, _ := http.NewRequest("GET", st.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), secret) {
		t.Fatal("an environment value reached the response")
	}
	var got struct{ Agents []Agent }
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	var claude *Agent
	for i := range got.Agents {
		if got.Agents[i].Name == "claude" {
			claude = &got.Agents[i]
		}
	}
	if claude == nil {
		t.Fatal("no claude")
	}
	if claude.ProviderHost != "api.anthropic.com" || len(claude.LoginFiles) == 0 {
		t.Errorf("claude: %+v", claude)
	}
	set := map[string]bool{}
	for _, e := range claude.Env {
		set[e.Name] = e.Set
	}
	if v, ok := set["ANTHROPIC_API_KEY"]; !ok || !v {
		t.Errorf("ANTHROPIC_API_KEY: listed %v, set %v; want listed and set", ok, v)
	}
	if v, ok := set["ANTHROPIC_AUTH_TOKEN"]; !ok || v {
		t.Errorf("ANTHROPIC_AUTH_TOKEN: listed %v, set %v; want listed and unset", ok, v)
	}
}
