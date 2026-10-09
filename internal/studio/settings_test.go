package studio

import (
	"context"
	"fmt"
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

func TestEgressRulesAndGroupsAreValidated(t *testing.T) {
	_, st, _ := studioUnderTest(t)
	for name, rules := range map[string][]EgressRule{
		"a URL":          {{Host: "https://example.com/x", Action: "deny"}},
		"a port":         {{Host: "example.com:443", Action: "deny"}},
		"an allow rule":  {{Host: "example.com", Action: "allow"}},
		"a host twice":   {{Host: "example.com", Action: "deny"}, {Host: "EXAMPLE.com.", Action: "deny"}},
		"a bad wildcard": {{Host: "a.*.example.com", Action: "deny"}},
	} {
		if r, body := call(t, st.URL, "PUT", "/api/egress", testToken, "", map[string]any{"rules": rules}); r.StatusCode != http.StatusBadRequest {
			t.Errorf("rules, %s: %d %v", name, r.StatusCode, body)
		}
	}
	for name, tc := range map[string]struct {
		path string
		g    EgressGroup
	}{
		"a bad name": {"/api/egress/groups/Go_Mods", EgressGroup{Hosts: []string{"proxy.golang.org"}}},
		"a URL":      {"/api/egress/groups/go", EgressGroup{Hosts: []string{"https://proxy.golang.org"}}},
	} {
		if r, body := call(t, st.URL, "PUT", tc.path, testToken, "", tc.g); r.StatusCode != http.StatusBadRequest {
			t.Errorf("group, %s: %d %v", name, r.StatusCode, body)
		}
	}
	if r, body := call(t, st.URL, "PUT", "/api/egress/groups/go", testToken, "", EgressGroup{Hosts: []string{" Proxy.Golang.org. ", "proxy.golang.org", "sum.golang.org"}}); r.StatusCode != http.StatusOK {
		t.Fatalf("save a group: %d %v", r.StatusCode, body)
	}
	_, body := call(t, st.URL, "GET", "/api/egress", testToken, "", nil)
	groups := body["groups"].([]any)
	if len(groups) != 1 || fmt.Sprint(groups[0].(map[string]any)["hosts"]) != "[proxy.golang.org sum.golang.org]" {
		t.Errorf("saved %v", groups)
	}
	if r, _ := call(t, st.URL, "DELETE", "/api/egress/groups/go", testToken, "", nil); r.StatusCode != http.StatusNoContent {
		t.Errorf("delete: %d", r.StatusCode)
	}
	if r, _ := call(t, st.URL, "DELETE", "/api/egress/groups/go", testToken, "", nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("deleting what is gone: %d", r.StatusCode)
	}
}

// An allow rule saved before there were groups is not lost: it is read into
// a default group, and a switched-off one, which allowed nothing, is dropped.
func TestAllowRulesBecomeADefaultGroup(t *testing.T) {
	_, st, _ := studioUnderTest(t)
	old := `{"egress":[{"host":"a.example.com","action":"allow","enabled":true},` +
		`{"host":"off.example.com","action":"allow","enabled":false},` +
		`{"host":"evil.example.com","action":"deny","enabled":true}]}`
	if err := os.MkdirAll(agenthome.ConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agenthome.ConfigDir(), "studio.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	_, body := call(t, st.URL, "GET", "/api/egress", testToken, "", nil)
	if got := fmt.Sprint(body["groups"]); got != "[map[default:true description:Hosts allowed before there were groups hosts:[a.example.com] name:default]]" {
		t.Errorf("groups %s", got)
	}
	if got := fmt.Sprint(body["rules"]); !strings.Contains(got, "evil.example.com") || strings.Contains(got, "a.example.com") {
		t.Errorf("rules %s", got)
	}
}

// A launch gets the groups it picks, or the default ones when it picks none
// and is an allowlist; the deny rules always; and never an open run turned
// into an allowlist.
func TestEgressGroupsApplyToLaunches(t *testing.T) {
	s, st, c := studioUnderTest(t)
	var last LaunchRequest
	s.Launch = func(_ context.Context, req LaunchRequest) (LaunchResult, error) {
		last = req
		return LaunchResult{Sandbox: "sbx_x"}, nil
	}
	call(t, st.URL, "PUT", "/api/egress/groups/go", testToken, "", EgressGroup{Hosts: []string{"proxy.golang.org"}, Default: true})
	call(t, st.URL, "PUT", "/api/egress/groups/npm", testToken, "", EgressGroup{Hosts: []string{"registry.npmjs.org"}})
	call(t, st.URL, "PUT", "/api/egress", testToken, "", map[string]any{"rules": []EgressRule{
		{Host: "evil.example.com", Action: "deny", Enabled: true},
		{Host: "off.example.com", Action: "deny", Enabled: false},
	}})
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defaultIsAllowlist := caps.Network.Default.Mode == api.NetworkAllowlist
	cmd := []string{"true"}

	for name, tc := range map[string]struct {
		req         LaunchRequest
		wantNetwork string
		wantAllow   []string
		wantDeny    []string
	}{
		"an allowlist, picking none":    {LaunchRequest{Command: cmd, Network: "allowlist"}, "allowlist", []string{"proxy.golang.org"}, []string{"evil.example.com"}},
		"an allowlist, picking npm":     {LaunchRequest{Command: cmd, Network: "allowlist", EgressGroups: []string{"npm"}}, "allowlist", []string{"registry.npmjs.org"}, []string{"evil.example.com"}},
		"groups imply an allowlist":     {LaunchRequest{Command: cmd, EgressGroups: []string{"go", "npm"}}, "allowlist", []string{"proxy.golang.org", "registry.npmjs.org"}, []string{"evil.example.com"}},
		"an allowlist, picking nothing": {LaunchRequest{Command: cmd, Network: "allowlist", EgressGroups: []string{}}, "allowlist", nil, []string{"evil.example.com"}},
		"open":                          {LaunchRequest{Command: cmd, Network: "open"}, "open", nil, []string{"evil.example.com"}},
		"the server's default":          {LaunchRequest{Command: cmd}, "", map[bool][]string{true: {"proxy.golang.org"}}[defaultIsAllowlist], []string{"evil.example.com"}},
	} {
		if r, body := call(t, st.URL, "POST", "/api/runs", testToken, "", tc.req); r.StatusCode != http.StatusCreated {
			t.Fatalf("%s: %d %v", name, r.StatusCode, body)
		}
		if last.Network != tc.wantNetwork || !slices.Equal(last.Allow, tc.wantAllow) || !slices.Equal(last.Deny, tc.wantDeny) {
			t.Errorf("%s: network %q allow %v deny %v; want %q %v %v", name, last.Network, last.Allow, last.Deny, tc.wantNetwork, tc.wantAllow, tc.wantDeny)
		}
	}

	for name, req := range map[string]LaunchRequest{
		"an unknown group":   {Command: cmd, EgressGroups: []string{"nope"}},
		"groups beside open": {Command: cmd, Network: "open", EgressGroups: []string{"go"}},
		"groups beside none": {Command: cmd, Network: "none", EgressGroups: []string{"go"}},
	} {
		if r, body := call(t, st.URL, "POST", "/api/runs", testToken, "", req); r.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %v", name, r.StatusCode, body)
		}
	}

	// --no-baseline and a template's size reach the launcher as asked.
	call(t, st.URL, "POST", "/api/runs", testToken, "", LaunchRequest{Command: cmd, EgressGroups: []string{"go"}, NoBaseline: true, CPUs: 4, MemoryMB: 8192, DiskMB: 2048})
	if !last.NoBaseline || last.CPUs != 4 || last.MemoryMB != 8192 || last.DiskMB != 2048 {
		t.Errorf("launched %+v", last)
	}
	if r, _ := call(t, st.URL, "POST", "/api/runs", testToken, "", LaunchRequest{Command: cmd, MemoryMB: -1}); r.StatusCode != http.StatusBadRequest {
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
