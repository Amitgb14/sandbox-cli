package server

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

func poolServer(t *testing.T, size int) (*api.Client, *fake.Backend) {
	t.Helper()
	be := fake.New(api.CapEgressAllowlist, api.CapVolumes)
	pol := spec.DefaultPolicyFor(be.Capabilities())
	pol.Pools = []spec.Pool{{Size: size}}
	ts := httptest.NewServer((&Server{Backend: be, Policy: pol}).Handler())
	t.Cleanup(ts.Close)
	c, err := api.NewClient(ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	return c, be
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A request in the pool's shape gets a sandbox already booted, with its own
// environment, name and labels; the pool refills; pooled sandboxes are not
// listed until claimed.
func TestPoolHandsOutABootedSandbox(t *testing.T) {
	c, be := poolServer(t, 2)
	ctx := context.Background()
	waitFor(t, "the pool to fill", func() bool { return be.Count() == 2 })
	if list, _ := c.Sandboxes(ctx); len(list) != 0 {
		t.Fatalf("pooled sandboxes are listed: %d", len(list))
	}
	before := be.IDs()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "pooled", Env: map[string]string{"GREETING": "hi"},
		Labels: map[string]string{"k": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	if !before[sb.ID] {
		t.Fatalf("%s was booted for the request, not taken from the pool", sb.ID)
	}
	res, err := c.Run(ctx, "pooled", api.RunRequest{Argv: []string{"printenv", "GREETING"}})
	if err != nil || string(res.Stdout) != "hi\n" {
		t.Fatalf("the request's env did not reach the pooled sandbox: %q %v", res.Stdout, err)
	}
	waitFor(t, "the pool to refill", func() bool { return be.Count() == 3 })
	if list, _ := c.Sandboxes(ctx); len(list) != 1 || list[0].Labels["k"] != "v" {
		t.Fatalf("listing after a claim: %+v", list)
	}
}

// Anything fixed at boot that differs from the pool's shape boots fresh.
func TestPoolOnlyServesItsShape(t *testing.T) {
	c, be := poolServer(t, 1)
	ctx := context.Background()
	waitFor(t, "the pool to fill", func() bool { return be.Count() == 1 })
	before := be.IDs()
	if _, err := c.CreateVolume(ctx, api.CreateVolumeRequest{Name: "v", SizeMB: 8}); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]api.CreateSandboxRequest{
		"cpus":    {CPUs: 2},
		"network": {Network: &api.NetworkPolicy{Mode: api.NetworkNone}},
		"volumes": {Volumes: []api.VolumeMount{{Name: "v", Path: "/data"}}},
	} {
		sb, err := c.CreateSandbox(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if before[sb.ID] {
			t.Errorf("%s: a request of another shape was given a pooled sandbox", name)
		}
		_ = c.TerminateSandbox(ctx, sb.ID)
	}
}
