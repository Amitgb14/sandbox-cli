package studio

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

func TestTemplates(t *testing.T) {
	_, st, _ := studioUnderTest(t)
	names := func() []string {
		_, body := call(t, st.URL, "GET", "/api/templates", testToken, "", nil)
		var out []string
		for _, x := range body["templates"].([]any) {
			out = append(out, x.(map[string]any)["name"].(string))
		}
		return out
	}
	if got := names(); !slices.Equal(got, []string{"micro", "small", "medium", "large", "xlarge"}) {
		t.Fatalf("built-in templates %v", got)
	}

	if r, body := call(t, st.URL, "PUT", "/api/templates/ci-runner", testToken, "", Template{CPUs: 2, MemoryMB: 3072, DiskMB: 4096, Description: " CI "}); r.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %v", r.StatusCode, body)
	}
	// Saved again: replaced, not listed twice.
	if r, _ := call(t, st.URL, "PUT", "/api/templates/ci-runner", testToken, "", Template{CPUs: 3, MemoryMB: 3072}); r.StatusCode != http.StatusOK {
		t.Fatalf("replace: %d", r.StatusCode)
	}
	if got := names(); len(got) != 6 || got[5] != "ci-runner" {
		t.Fatalf("after saving: %v", got)
	}
	if fi, err := os.Stat(filepath.Join(agenthome.ConfigDir(), "studio.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("settings file: %v %v", fi, err)
	}

	for name, tc := range map[string]struct {
		path string
		t    Template
		want int
	}{
		"a built-in name":    {"/api/templates/large", Template{CPUs: 1, MemoryMB: 512}, http.StatusConflict},
		"a bad name":         {"/api/templates/Big_One", Template{CPUs: 1, MemoryMB: 512}, http.StatusBadRequest},
		"no cpus":            {"/api/templates/x", Template{MemoryMB: 512}, http.StatusBadRequest},
		"a fraction of cpus": {"/api/templates/x", Template{CPUs: 1.5, MemoryMB: 512}, http.StatusBadRequest},
		"too little memory":  {"/api/templates/x", Template{CPUs: 1, MemoryMB: 64}, http.StatusBadRequest},
		"a tiny disk":        {"/api/templates/x", Template{CPUs: 1, MemoryMB: 512, DiskMB: 10}, http.StatusBadRequest},
	} {
		if r, body := call(t, st.URL, "PUT", tc.path, testToken, "", tc.t); r.StatusCode != tc.want {
			t.Errorf("%s: %d %v, want %d", name, r.StatusCode, body, tc.want)
		}
	}

	if r, _ := call(t, st.URL, "DELETE", "/api/templates/medium", testToken, "", nil); r.StatusCode != http.StatusConflict {
		t.Errorf("deleting a built-in: %d", r.StatusCode)
	}
	if r, _ := call(t, st.URL, "DELETE", "/api/templates/ci-runner", testToken, "", nil); r.StatusCode != http.StatusNoContent {
		t.Errorf("delete: %d", r.StatusCode)
	}
	if r, _ := call(t, st.URL, "DELETE", "/api/templates/ci-runner", testToken, "", nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("deleting what is gone: %d", r.StatusCode)
	}
	// The settings are the token's, like everything under /api.
	if r, _ := call(t, st.URL, "PUT", "/api/templates/sneaky", "", "", Template{CPUs: 1, MemoryMB: 512}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("without the token: %d", r.StatusCode)
	}
}

func TestEgressRulesAreValidated(t *testing.T) {
	_, st, _ := studioUnderTest(t)
	for name, rules := range map[string][]EgressRule{
		"a URL":          {{Host: "https://example.com/x", Action: "allow"}},
		"a port":         {{Host: "example.com:443", Action: "allow"}},
		"an odd action":  {{Host: "example.com", Action: "permit"}},
		"a host twice":   {{Host: "example.com", Action: "allow"}, {Host: "EXAMPLE.com.", Action: "deny"}},
		"a bad wildcard": {{Host: "a.*.example.com", Action: "allow"}},
	} {
		if r, body := call(t, st.URL, "PUT", "/api/egress", testToken, "", map[string]any{"rules": rules}); r.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %v", name, r.StatusCode, body)
		}
	}
	r, body := call(t, st.URL, "PUT", "/api/egress", testToken, "", map[string]any{"rules": []EgressRule{
		{Host: " Proxy.Golang.org. ", Action: "allow", Enabled: true},
		{Host: "*.example.com", Action: "deny", Enabled: true, Note: "no"},
	}})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %v", r.StatusCode, body)
	}
	_, body = call(t, st.URL, "GET", "/api/egress", testToken, "", nil)
	rules := body["rules"].([]any)
	if len(rules) != 2 || rules[0].(map[string]any)["host"] != "proxy.golang.org" {
		t.Errorf("saved %v", rules)
	}
}

// The enabled rules reach a launch: a deny always, an allow only into a run
// that is an allowlist, never turning an open run into one.
func TestEgressRulesApplyToLaunches(t *testing.T) {
	s, st, c := studioUnderTest(t)
	var last LaunchRequest
	s.Launch = func(_ context.Context, req LaunchRequest) (LaunchResult, error) {
		last = req
		return LaunchResult{Sandbox: "sbx_x"}, nil
	}
	call(t, st.URL, "PUT", "/api/egress", testToken, "", map[string]any{"rules": []EgressRule{
		{Host: "proxy.golang.org", Action: "allow", Enabled: true},
		{Host: "off.example.com", Action: "allow", Enabled: false},
		{Host: "evil.example.com", Action: "deny", Enabled: true},
	}})
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defaultIsAllowlist := caps.Network.Default.Mode == api.NetworkAllowlist

	for name, tc := range map[string]struct {
		req       LaunchRequest
		wantAllow []string
	}{
		"an allowlist":                     {LaunchRequest{Command: []string{"true"}, Network: "allowlist"}, []string{"proxy.golang.org"}},
		"an allowlist of its own":          {LaunchRequest{Command: []string{"true"}, Allow: []string{"a.example.com"}}, []string{"a.example.com", "proxy.golang.org"}},
		"open":                             {LaunchRequest{Command: []string{"true"}, Network: "open"}, nil},
		"none":                             {LaunchRequest{Command: []string{"true"}, Network: "none"}, nil},
		"the server's default, either way": {LaunchRequest{Command: []string{"true"}}, map[bool][]string{true: {"proxy.golang.org"}}[defaultIsAllowlist]},
	} {
		if r, body := call(t, st.URL, "POST", "/api/runs", testToken, "", tc.req); r.StatusCode != http.StatusCreated {
			t.Fatalf("%s: %d %v", name, r.StatusCode, body)
		}
		if !slices.Equal(last.Allow, tc.wantAllow) {
			t.Errorf("%s: allow %v, want %v", name, last.Allow, tc.wantAllow)
		}
		if !slices.Equal(last.Deny, []string{"evil.example.com"}) {
			t.Errorf("%s: deny %v", name, last.Deny)
		}
	}

	// A template's size reaches the launcher as asked; sandboxd bounds it.
	call(t, st.URL, "POST", "/api/runs", testToken, "", LaunchRequest{Command: []string{"true"}, CPUs: 4, MemoryMB: 8192, DiskMB: 2048})
	if last.CPUs != 4 || last.MemoryMB != 8192 || last.DiskMB != 2048 {
		t.Errorf("size: %+v", last)
	}
	if r, _ := call(t, st.URL, "POST", "/api/runs", testToken, "", LaunchRequest{Command: []string{"true"}, MemoryMB: -1}); r.StatusCode != http.StatusBadRequest {
		t.Errorf("a negative size: %d", r.StatusCode)
	}
}

// A saved key is write-only: listed as saved by name, its value never served.
func TestAgentKeysAreWriteOnly(t *testing.T) {
	const secret = "sk-saved-do-not-show-0123456789"
	os.Unsetenv("ANTHROPIC_API_KEY")
	_, st, _ := studioUnderTest(t)
	if r, body := call(t, st.URL, "PUT", "/api/agents/claude/keys/ANTHROPIC_API_KEY", testToken, "", map[string]string{"value": secret}); r.StatusCode != http.StatusNoContent {
		t.Fatalf("save: %d %v", r.StatusCode, body)
	}
	for name, path := range map[string]string{
		"a variable the agent does not read": "/api/agents/claude/keys/LD_PRELOAD",
		"an unknown agent":                   "/api/agents/nope/keys/ANTHROPIC_API_KEY",
		"an interactive-only agent":          "/api/agents/goose/keys/OPENAI_API_KEY",
	} {
		if r, _ := call(t, st.URL, "PUT", path, testToken, "", map[string]string{"value": "x"}); r.StatusCode < 400 {
			t.Errorf("%s: %d", name, r.StatusCode)
		}
	}
	if r, _ := call(t, st.URL, "PUT", "/api/agents/claude/keys/ANTHROPIC_API_KEY", testToken, "", map[string]string{"value": "  "}); r.StatusCode != http.StatusBadRequest {
		t.Errorf("an empty key: %d", r.StatusCode)
	}

	req, _ := http.NewRequest("GET", st.URL+"/api/agents", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), secret) {
		t.Fatal("a saved key's value reached the response")
	}
	if !strings.Contains(string(b), `"name":"ANTHROPIC_API_KEY","set":false,"saved":true`) {
		t.Errorf("ANTHROPIC_API_KEY not listed as saved: %s", b)
	}

	if r, _ := call(t, st.URL, "DELETE", "/api/agents/claude/keys/ANTHROPIC_API_KEY", testToken, "", nil); r.StatusCode != http.StatusNoContent {
		t.Errorf("delete: %d", r.StatusCode)
	}
	data, _ := os.ReadFile(filepath.Join(agenthome.ConfigDir(), "agent-keys.json"))
	if strings.Contains(string(data), secret) {
		t.Error("a removed key is still in the file")
	}
}
