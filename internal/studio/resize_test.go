package studio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

func studioWith(t *testing.T, caps ...string) (*httptest.Server, *api.Client) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	be := fake.New(caps...)
	sd := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), Token: "sandboxd-token"}).Handler())
	t.Cleanup(sd.Close)
	c, err := api.NewClient(sd.URL, "sandboxd-token")
	if err != nil {
		t.Fatal(err)
	}
	st := httptest.NewServer((&Server{Client: c, Context: "t", Token: testToken}).Handler())
	t.Cleanup(st.Close)
	return st, c
}

// A resize is a copy at the new size that takes the old one's place: its
// files, name, labels, network and idle timeout come across, the old one is
// gone, and the snapshot between them is deleted unless asked to be kept.
func TestResizeReplacesTheSandboxWithACopy(t *testing.T) {
	st, c := studioWith(t, api.CapEgressAllowlist, api.CapDiskSnapshot)
	ctx := context.Background()
	old, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web", CPUs: 1, MemoryMB: 512, IdleTimeoutSecs: 600,
		Labels: map[string]string{"team": "a"}, Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteFile(ctx, old.ID, "/sandbox/home/kept.txt", []byte("still here")); err != nil {
		t.Fatal(err)
	}
	r, body := call(t, st.URL, "POST", "/api/sandboxes/"+old.ID+"/resize", testToken, "", ResizeRequest{CPUs: 4, MemoryMB: 4096})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("resize: %d %v", r.StatusCode, body)
	}
	nsb, err := c.Sandbox(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	if nsb.ID == old.ID || nsb.CPUs != 4 || nsb.MemoryMB != 4096 {
		t.Fatalf("web is %s at %v vCPU, %d MiB", nsb.ID, nsb.CPUs, nsb.MemoryMB)
	}
	if nsb.Labels["team"] != "a" || nsb.Network.Mode != api.NetworkNone || nsb.IdleTimeoutSecs != 600 {
		t.Errorf("the copy lost the old one's records: %+v", nsb)
	}
	if b, err := c.ReadFile(ctx, nsb.ID, "/sandbox/home/kept.txt"); err != nil || string(b) != "still here" {
		t.Errorf("the copy's file: %q, %v", b, err)
	}
	if got, _ := c.Sandbox(ctx, old.ID); got.State != api.StateTerminated {
		t.Errorf("the old one is %s", got.State)
	}
	if snaps, _ := c.Snapshots(ctx); len(snaps) != 0 {
		t.Errorf("%d snapshots left behind", len(snaps))
	}
}

// What a resize cannot carry, it refuses rather than drops quietly: the
// environment (values are never readable back), and any size on an
// endpoint whose copies keep the original's.
func TestResizeRefusesWhatItCannotCarry(t *testing.T) {
	st, c := studioWith(t, api.CapEgressAllowlist, api.CapDiskSnapshot)
	ctx := context.Background()
	withEnv, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Env: map[string]string{"API_KEY": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/sandboxes/" + withEnv.ID + "/resize"
	if r, body := call(t, st.URL, "POST", path, testToken, "", ResizeRequest{CPUs: 2, MemoryMB: 1024}); r.StatusCode != http.StatusConflict {
		t.Fatalf("a sandbox with an environment: %d %v", r.StatusCode, body)
	}
	if got, _ := c.Sandbox(ctx, withEnv.ID); got.State != api.StateRunning {
		t.Fatalf("a refused resize touched it: %s", got.State)
	}
	if r, body := call(t, st.URL, "POST", path, testToken, "", ResizeRequest{CPUs: 2, MemoryMB: 1024, DropEnv: true, KeepSnapshot: true}); r.StatusCode != http.StatusOK || body["snapshot"] == "" {
		t.Fatalf("with drop_env: %d %v", r.StatusCode, body)
	}

	mem, cm := studioWith(t, api.CapEgressAllowlist, api.CapMemorySnapshot)
	sb, err := cm.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := call(t, mem.URL, "POST", "/api/sandboxes/"+sb.ID+"/resize", testToken, "", ResizeRequest{CPUs: 2, MemoryMB: 1024}); r.StatusCode != http.StatusNotImplemented {
		t.Errorf("on memory snapshots: %d", r.StatusCode)
	}
	if snaps, _ := cm.Snapshots(ctx); len(snaps) != 0 {
		t.Errorf("a refused resize took a snapshot")
	}
}
