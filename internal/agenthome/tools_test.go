package agenthome

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Which runs get an agent's tools volume, read-only: one that exists and is
// free or only read; not one an installer holds, not an agent the image
// carries, not an endpoint without volumes. An install that fails leaves no
// volume behind to be skipped forever.
func TestAgentTools(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer((&server.Server{Backend: fake.New(api.CapEgressAllowlist, api.CapVolumes), Policy: spec.DefaultPolicyFor(fake.New(api.CapEgressAllowlist, api.CapVolumes).Capabilities())}).Handler())
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	caps, err := c.Capabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	quiet := func(string, ...any) {}
	gemini, _ := agents.LookupInteractive("gemini")
	claude, _ := agents.LookupInteractive("claude")
	tl, _ := gemini.Tools()
	name := tl.Volume("gemini")

	if AgentTools(ctx, c, caps, claude, quiet) != nil {
		t.Error("an agent in the image was given a tools volume")
	}
	noVols := caps
	noVols.Capabilities = map[string]bool{}
	if AgentTools(ctx, c, noVols, gemini, quiet) != nil {
		t.Error("an endpoint without volumes was asked for one")
	}
	noNet := caps
	noNet.Network.Default.Mode = api.NetworkNone
	if AgentTools(ctx, c, noNet, gemini, quiet) != nil {
		t.Error("an endpoint whose sandboxes cannot reach a registry was asked to install")
	}
	if vols, _ := c.Volumes(ctx); len(vols) != 0 {
		t.Errorf("an install with no network made %+v", vols)
	}

	// The fake runs no shell, so the install fails: the volume goes with it.
	if AgentTools(ctx, c, caps, gemini, quiet) != nil {
		t.Error("a failed install was mounted")
	}
	if vols, _ := c.Volumes(ctx); len(vols) != 0 {
		t.Errorf("a failed install left %+v", vols)
	}

	if _, err := c.CreateVolume(ctx, api.CreateVolumeRequest{Name: name, SizeMB: 64}); err != nil {
		t.Fatal(err)
	}
	m := AgentTools(ctx, c, caps, gemini, quiet)
	if m == nil || m.Name != name || m.Path != agents.ToolsDir || !m.ReadOnly {
		t.Fatalf("an installed volume: %+v", m)
	}
	reader, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Volumes: []api.VolumeMount{*m}})
	if err != nil {
		t.Fatal(err)
	}
	if AgentTools(ctx, c, caps, gemini, quiet) == nil {
		t.Error("a volume only read was not shared")
	}
	c.TerminateSandbox(ctx, reader.ID)

	inst, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Labels: map[string]string{ToolsInstallLabel: "gemini"},
		Volumes: []api.VolumeMount{{Name: name, Path: agents.ToolsDir}}})
	if err != nil {
		t.Fatal(err)
	}
	if AgentTools(ctx, c, caps, gemini, quiet) != nil {
		t.Error("a volume being installed into was mounted")
	}
	c.TerminateSandbox(ctx, inst.ID)
}

// A new pin names the volumes of the agent's earlier installs, and nothing
// else: not another agent's whose name starts the same, not a volume that
// only looks similar.
func TestSupersededTools(t *testing.T) {
	vols := []api.Volume{
		{Name: "agent-qwen-cfca24e1"}, {Name: "agent-qwen-00aa11bb"}, {Name: "agent-qwen-code-00aa11bb"},
		{Name: "agent-qwen-notahash"}, {Name: "agent-qwen-00aa11bbc"}, {Name: "cache"},
	}
	got := supersededTools(vols, "qwen", "agent-qwen-cfca24e1")
	if len(got) != 1 || got[0] != "agent-qwen-00aa11bb" {
		t.Errorf("got %v", got)
	}
}
