package cli

import (
	"bytes"
	"context"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// update changes only what a flag names: labels merge into what the
// sandbox has, a rename takes, the idle timeout is set, and an allowlist
// built with --no-baseline keeps an agent's API.
func TestUpdateChangesOnlyWhatIsNamed(t *testing.T) {
	be := fake.New(api.CapEgressAllowlist, api.CapNetworkPolicyUpdate)
	srv := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}).Handler())
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web", Labels: map[string]string{"agent": "claude", "team": "a", "ticket": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	useEndpoint(t, srv.URL)
	run := func(args ...string) error {
		root := NewRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append([]string{"update"}, args...))
		return root.Execute()
	}

	if err := run("web", "--name", "api", "--label", "team=b", "--label", "ticket-", "--idle", "2h"); err != nil {
		t.Fatal(err)
	}
	got, _ := c.Sandbox(ctx, sb.ID)
	if got.Name != "api" || got.IdleTimeoutSecs != 7200 {
		t.Errorf("name %q idle %d", got.Name, got.IdleTimeoutSecs)
	}
	if want := map[string]string{"agent": "claude", "team": "b"}; len(got.Labels) != 2 || got.Labels["agent"] != "claude" || got.Labels["team"] != "b" {
		t.Errorf("labels %v, want %v", got.Labels, want)
	}

	if err := run("api", "--network", "allowlist", "--allow", "proxy.golang.org", "--no-baseline", "--deny", "evil.example.com"); err != nil {
		t.Fatal(err)
	}
	got, _ = c.Sandbox(ctx, sb.ID)
	slices.Sort(got.Network.Allow)
	if got.Network.Mode != api.NetworkAllowlist || !slices.Equal(got.Network.Allow, []string{"api.anthropic.com", "proxy.golang.org"}) {
		t.Errorf("network %+v; want proxy.golang.org and the agent's API alone", got.Network)
	}
	if !slices.Equal(got.Network.Deny, []string{"evil.example.com"}) {
		t.Errorf("deny %v", got.Network.Deny)
	}

	for name, args := range map[string][]string{
		"no flag":                      {"api"},
		"--allow without network":      {"api", "--allow", "x.example.com"},
		"a bad label":                  {"api", "--label", "nokey"},
		"a fractional idle":            {"api", "--idle", "1.5s"},
		"a lifetime to move from none": {"api", "--lifetime", "+15m"},
		"a lifetime that is not one":   {"api", "--lifetime", "soon"},
	} {
		if err := run(args...); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// --lifetime sets a lifetime, or moves the one a sandbox has.
func TestUpdateLifetime(t *testing.T) {
	be := fake.New(api.CapEgressAllowlist)
	srv := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}).Handler())
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web", LifetimeSecs: 1800})
	if err != nil {
		t.Fatal(err)
	}
	useEndpoint(t, srv.URL)
	run := func(args ...string) error {
		root := NewRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append([]string{"update", "web"}, args...))
		return root.Execute()
	}
	for _, step := range []struct {
		arg  string
		want int
	}{{"+15m", 2700}, {"-10m", 2100}, {"1h", 3600}} {
		if err := run("--lifetime", step.arg); err != nil {
			t.Fatalf("--lifetime %s: %v", step.arg, err)
		}
		if got, _ := c.Sandbox(ctx, sb.ID); got.LifetimeSecs != step.want {
			t.Errorf("--lifetime %s: %d, want %d", step.arg, got.LifetimeSecs, step.want)
		}
	}
	for _, bad := range []string{"-1h", "0s", "1.5s"} {
		if err := run("--lifetime", bad); err == nil {
			t.Errorf("--lifetime %s was accepted", bad)
		}
	}
}

func TestEditLabels(t *testing.T) {
	got, err := editLabels(map[string]string{"a": "1", "b": "2", "gateway.owner": "alice"}, []string{"a=9", "c=3", "b-"}, []string{"c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["a"] != "9" || got["gateway.owner"] != "alice" {
		t.Errorf("labels %v", got)
	}
	// A value may end in a dash; only a bare key- removes.
	got, _ = editLabels(nil, []string{"range=1-"}, nil)
	if got["range"] != "1-" {
		t.Errorf("labels %v", got)
	}
}
