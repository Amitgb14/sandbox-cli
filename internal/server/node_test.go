package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

var testCapacity = api.NodeResources{CPUs: 8, MemoryMB: 16384, DiskMB: 100000}

func nodeServer(t *testing.T, nodeID string, poolSize int) (*api.Client, *fake.Backend, *Server) {
	t.Helper()
	be := fake.New(api.CapEgressAllowlist)
	pol := spec.DefaultPolicyFor(be.Capabilities())
	if poolSize > 0 {
		pol.Pools = []spec.Pool{{Size: poolSize}}
	}
	s := &Server{Backend: be, Policy: pol, NodeID: nodeID, Capacity: testCapacity,
		NodeLabels: map[string]string{"region": "west"}}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c, err := api.NewClient(ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	return c, be, s
}

// Free is what sandboxes have not been given: every one not terminated holds
// its allocation, and a terminated one gives it back.
func TestNodeFreeIsCapacityLessAllocations(t *testing.T) {
	c, _, _ := nodeServer(t, "n1", 0)
	ctx := context.Background()
	st, err := c.Node(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Node != "n1" || st.Capacity != testCapacity || st.Free != testCapacity || st.Running != 0 ||
		st.Labels["region"] != "west" || st.Cordoned || st.Version == "" || st.Capabilities.Backend != "fake" {
		t.Fatalf("an empty node: %+v", st)
	}
	a, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 2, MemoryMB: 4096, DiskMB: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 0.5, MemoryMB: 512, DiskMB: 1000}); err != nil {
		t.Fatal(err)
	}
	st, _ = c.Node(ctx)
	if want := (api.NodeResources{CPUs: 5.5, MemoryMB: 11776, DiskMB: 79000}); st.Free != want || st.Running != 2 {
		t.Fatalf("free %+v running %d, want %+v and 2", st.Free, st.Running, want)
	}
	if err := c.TerminateSandbox(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	st, _ = c.Node(ctx)
	if want := (api.NodeResources{CPUs: 7.5, MemoryMB: 15872, DiskMB: 99000}); st.Free != want || st.Running != 1 {
		t.Fatalf("after a terminate: free %+v running %d, want %+v and 1", st.Free, st.Running, want)
	}
	if len(st.Images) != 1 || st.Images[0] != spec.DefaultPolicy().DefaultImage {
		t.Fatalf("cached images: %v", st.Images)
	}
}

// A node configured with less than it has given out is full, not negative.
func TestNodeFreeIsNeverNegative(t *testing.T) {
	c, _, s := nodeServer(t, "", 0)
	s.Capacity = api.NodeResources{CPUs: 1, MemoryMB: 100, DiskMB: 100}
	ctx := context.Background()
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 2, MemoryMB: 4096, DiskMB: 20000}); err != nil {
		t.Fatal(err)
	}
	st, _ := c.Node(ctx)
	if st.Free != (api.NodeResources{}) {
		t.Fatalf("free %+v, want zero", st.Free)
	}
}

// Pooled sandboxes are booted VMs no client sees: they are counted by image
// and hold their resources.
func TestNodeCountsPooledSandboxes(t *testing.T) {
	c, be, _ := nodeServer(t, "n1", 2)
	ctx := context.Background()
	waitFor(t, "the pool to fill", func() bool { return be.Count() == 2 })
	st, err := c.Node(ctx)
	if err != nil {
		t.Fatal(err)
	}
	img := spec.DefaultPolicy().DefaultImage
	if st.Pooled[img] != 2 || len(st.Pooled) != 1 || st.Running != 0 {
		t.Fatalf("pooled %v running %d", st.Pooled, st.Running)
	}
	def := spec.DefaultPolicy()
	want := api.NodeResources{CPUs: testCapacity.CPUs - 2*def.DefaultCPUs,
		MemoryMB: testCapacity.MemoryMB - 2*def.DefaultMemoryMB, DiskMB: testCapacity.DiskMB - 2*def.DefaultDiskMB}
	if st.Free != want {
		t.Fatalf("free %+v, want %+v", st.Free, want)
	}
	// Pooled sandboxes name the node too: a claimed one keeps its id.
	for id := range be.IDs() {
		if n, ok := api.NodeOfID(id); !ok || n != "n1" {
			t.Errorf("pooled id %s does not name the node", id)
		}
	}
}

// A cordoned node refuses new sandboxes — a pooled one included — with a code
// a gateway reads as "try another node", and leaves what runs there alone.
// Uncordoning takes creates again.
func TestCordonRefusesCreatesAndSparesRunningSandboxes(t *testing.T) {
	c, be, _ := nodeServer(t, "n1", 1)
	ctx := context.Background()
	waitFor(t, "the pool to fill", func() bool { return be.Count() == 1 })
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pool to refill", func() bool { return be.Count() == 2 })

	st, err := c.Cordon(ctx, true)
	if err != nil || !st.Cordoned {
		t.Fatalf("cordon: %+v %v", st, err)
	}
	_, err = c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Status != http.StatusServiceUnavailable || ae.Code != api.CodeUnavailable ||
		!strings.Contains(ae.Message, "cordoned") {
		t.Fatalf("a create on a cordoned node: %v", err)
	}
	if be.Count() != 2 {
		t.Fatalf("a refused create still touched the backend or the pool: %d sandboxes", be.Count())
	}
	if list, _ := c.Sandboxes(ctx); len(list) != 1 {
		t.Fatalf("a refused create was listed: %d", len(list))
	}
	res, err := c.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"echo", "still here"}})
	if err != nil || string(res.Stdout) != "still here\n" {
		t.Fatalf("a running sandbox on a cordoned node: %q %v", res.Stdout, err)
	}
	if got, _ := c.Sandbox(ctx, sb.ID); got.State != api.StateRunning {
		t.Fatalf("state %s", got.State)
	}
	if st, _ := c.Node(ctx); !st.Cordoned {
		t.Fatal("GET /v1/node does not say cordoned")
	}

	if st, err := c.Cordon(ctx, false); err != nil || st.Cordoned {
		t.Fatalf("uncordon: %+v %v", st, err)
	}
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{}); err != nil {
		t.Fatalf("a create after uncordoning: %v", err)
	}
}

// With a node id every id the server makes names the node, so a gateway
// routes by the id alone; without one ids are as they always were.
func TestSandboxIDsNameTheNode(t *testing.T) {
	ctx := context.Background()
	c, _, _ := nodeServer(t, "n17", 0)
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := api.NodeOfID(sb.ID); !ok || n != "n17" || !spec.ValidID(sb.ID) {
		t.Fatalf("id %s on node n17", sb.ID)
	}

	c, _, _ = nodeServer(t, "", 0)
	sb, err = c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := api.NodeOfID(sb.ID); ok || !spec.ValidID(sb.ID) || len(sb.ID) != len("sbx_")+16 {
		t.Fatalf("a standalone server's id %s", sb.ID)
	}
	if st, _ := c.Node(ctx); st.Node != "" {
		t.Fatalf("a standalone server names a node: %q", st.Node)
	}
}

func TestCordonRefusesAMalformedBody(t *testing.T) {
	_, _, s := nodeServer(t, "", 0)
	rec := do(t, s.Handler(), "POST", "/v1/node/cordon", "127.0.0.1", "", "", "application/json", []byte(`{"cordon":true}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	if s.cordoned {
		t.Fatal("a refused request cordoned the node")
	}
}
