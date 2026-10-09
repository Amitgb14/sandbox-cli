package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
	"github.com/Amitgb14/sandbox-cli/internal/studio"
)

// recordingSandboxd is a fake-backend sandboxd that keeps every request's
// method, path and body, so a test can say what a launcher did and did not
// send.
type recordingSandboxd struct {
	mu   sync.Mutex
	reqs []recorded
	next http.Handler
}

type recorded struct {
	method, path string
	body         []byte
}

func (s *recordingSandboxd) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(b))
	s.mu.Lock()
	s.reqs = append(s.reqs, recorded{r.Method, r.URL.Path, b})
	s.mu.Unlock()
	s.next.ServeHTTP(w, r)
}

func (s *recordingSandboxd) all() []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recorded(nil), s.reqs...)
}

// The hosted launcher acts for someone who is not this machine's user, so it
// takes nothing from this machine: not the agent's API key from the
// environment, not a saved agent login, not the user's config.
func TestHostedLauncherTakesNothingFromTheHost(t *testing.T) {
	const leak = "sk-host-secret-do-not-forward"
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", leak)
	// A user config that, read, would add an env value and an image.
	cfgPath := policy.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("image: example.com/host-config-image:1\nenv:\n  FROM_HOST_CONFIG: "+leak+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A saved login that, restored, would be written into the sandbox.
	d, _ := agents.LookupInteractive("claude")
	for _, rel := range d.AuthPaths {
		p := filepath.Join(agenthome.LoginDir(d), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(leak), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	be := fake.New(api.CapEgressAllowlist)
	rec := &recordingSandboxd{next: (&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}).Handler()}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	launch := hostedLauncher()
	ctx := context.Background()

	// The fake backend has no claude to start, so the launch fails after the
	// create — which is what is inspected — and must not leave the sandbox.
	_, err := launch(ctx, c, studio.LaunchRequest{Agent: "claude", Console: true, Labels: map[string]string{"team": "a"}})
	if err == nil || !strings.Contains(err.Error(), "no such command") {
		t.Fatalf("console launch on the fake backend: %v", err)
	}
	list, _ := c.Sandboxes(ctx)
	for _, sb := range list {
		if sb.State != api.StateTerminated {
			t.Errorf("a failed start left %s %s", sb.ID, sb.State)
		}
	}
	var create api.CreateSandboxRequest
	for _, r := range rec.all() {
		if strings.Contains(string(r.body), leak) {
			t.Errorf("%s %s carried a value from the host", r.method, r.path)
		}
		if r.method == http.MethodPost && r.path == "/v1/sandboxes" {
			if err := json.Unmarshal(r.body, &create); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Contains(r.path, "/files") && r.method != http.MethodGet {
			t.Errorf("%s %s: wrote into the sandbox (a login restore?)", r.method, r.path)
		}
	}
	if _, ok := create.Env["ANTHROPIC_API_KEY"]; ok {
		t.Error("the agent's API key name was forwarded from the host")
	}
	if _, ok := create.Env["FROM_HOST_CONFIG"]; ok || create.Image != "" {
		t.Errorf("the host's config was applied: env %v image %q", create.Env, create.Image)
	}
	if create.Labels["studio"] != "1" || create.Labels["agent"] != "claude" || create.Labels["team"] != "a" {
		t.Errorf("labels: %v", create.Labels)
	}
	if create.Env["GIT_AUTHOR_NAME"] != "sandbox" {
		t.Errorf("no neutral git identity: %v", create.Env)
	}
	if n := create.Network; n != nil && (n.Mode != api.NetworkAllowlist || !slices.Contains(n.Allow, "api.anthropic.com")) {
		t.Errorf("network %+v: an allowlist must include the agent's API", n)
	}
}

func TestHostedLauncherNetworkAndRefusals(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	be := fake.New(api.CapEgressAllowlist)
	srv := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}).Handler())
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	launch := hostedLauncher()
	ctx := context.Background()

	res, err := launch(ctx, c, studio.LaunchRequest{Command: []string{"true"}, Network: api.NetworkAllowlist, Allow: []string{"example.org"}})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.Sandbox(ctx, res.Sandbox)
	if err != nil {
		t.Fatal(err)
	}
	if sb.Network.Mode != api.NetworkAllowlist || !slices.Contains(sb.Network.Allow, "example.org") {
		t.Errorf("network: %+v", sb.Network)
	}
	for _, tc := range []struct {
		req  studio.LaunchRequest
		want string
	}{
		{studio.LaunchRequest{Agent: "claude", Prompt: "hi"}, "job"},
		{studio.LaunchRequest{Command: []string{"true"}, Profile: "dev"}, "profile"},
		{studio.LaunchRequest{Command: []string{"true"}, Network: "wide"}, "network"},
	} {
		if _, err := launch(ctx, c, tc.req); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want an error about %s", tc.req, err, tc.want)
		}
	}
	if _, err := launch(ctx, nil, studio.LaunchRequest{Command: []string{"true"}}); err == nil {
		t.Error("launched without a client")
	}
}
