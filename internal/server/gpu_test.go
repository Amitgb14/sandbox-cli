package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

var testGPUs = []string{"0000:01:00.0", "0000:41:00.0"}

func gpuServer(t *testing.T, gpus []string, caps ...string) (*fake.Backend, *api.Client, string) {
	t.Helper()
	be := fake.New(caps...)
	events := filepath.Join(t.TempDir(), "events.jsonl")
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), Capacity: testCapacity, GPUs: gpus,
		Audit: audit.NewLog(events)}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c, _ := api.NewClient(ts.URL, "")
	return be, c, events
}

// A GPU is one sandbox's at a time: given at create, in the order sandboxd
// was given them, held until the sandbox is terminated, and refused as
// unavailable, not queued, when none is free.
func TestGPUsAreGivenWholeAndGivenBack(t *testing.T) {
	be, c, _ := gpuServer(t, testGPUs, api.CapGPU)
	ctx := context.Background()
	a, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.GPUs, testGPUs[:1]) || !slices.Equal(b.GPUs, testGPUs[1:]) {
		t.Fatalf("given %v and %v, want one each of %v", a.GPUs, b.GPUs, testGPUs)
	}
	if got := be.GPUs(a.ID); !slices.Equal(got, a.GPUs) {
		t.Fatalf("the backend was given %v, the sandbox says %v", got, a.GPUs)
	}
	plain, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil || len(plain.GPUs) != 0 || len(be.GPUs(plain.ID)) != 0 {
		t.Fatalf("a sandbox that asked for none: %v, %v", plain.GPUs, err)
	}
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: 1}); !api.IsCode(err, api.CodeUnavailable) {
		t.Fatalf("a third with two GPUs held: %v, want unavailable", err)
	}
	node, err := c.Node(ctx)
	if err != nil || node.Capacity.GPUs != 2 || node.Free.GPUs != 0 {
		t.Fatalf("the node with both held: capacity %d, free %d, %v", node.Capacity.GPUs, node.Free.GPUs, err)
	}
	if err := c.TerminateSandbox(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if node, _ := c.Node(ctx); node.Free.GPUs != 1 {
		t.Fatalf("after a terminate: %d free", node.Free.GPUs)
	}
	again, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: 1})
	if err != nil || !slices.Equal(again.GPUs, testGPUs[:1]) {
		t.Fatalf("after a terminate: %v, %v; want %s back", again.GPUs, err, testGPUs[0])
	}
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: 2}); !api.IsCode(err, api.CodeUnavailable) {
		t.Fatalf("two with one free: %v", err)
	}
}

// Concurrent creates cannot be given one GPU: it is chosen under the lock
// that registers the sandbox holding it.
func TestConcurrentCreatesShareNoGPU(t *testing.T) {
	_, c, _ := gpuServer(t, testGPUs, api.CapGPU)
	ctx := context.Background()
	var mu sync.Mutex
	var given []string
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: 1})
			if err != nil {
				if !api.IsCode(err, api.CodeUnavailable) {
					t.Error(err)
				}
				return
			}
			mu.Lock()
			given = append(given, sb.GPUs...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	slices.Sort(given)
	if !slices.Equal(given, testGPUs) {
		t.Fatalf("ten creates for two GPUs were given %v", given)
	}
}

// What a passed-through device rules out is refused, before anything is made
// or touched, and the GPU is named in the created event.
func TestGPUSandboxRefusals(t *testing.T) {
	_, c, events := gpuServer(t, testGPUs, api.CapGPU, api.CapSuspend, api.CapMemorySnapshot, api.CapResize)
	ctx := context.Background()
	for name, req := range map[string]api.CreateSandboxRequest{
		"negative":          {GPUs: -1},
		"over the limit":    {GPUs: 9},
		"idle suspend":      {GPUs: 1, IdleAction: api.IdleSuspend},
		"snapshot schedule": {GPUs: 1, SnapshotEverySecs: 3600},
	} {
		if _, err := c.CreateSandbox(ctx, req); !api.IsCode(err, api.CodeInvalidRequest) {
			t.Errorf("%s: %v, want invalid_request", name, err)
		}
	}
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Suspend(ctx, sb.ID); !api.IsCode(err, api.CodeUnsupported) {
		t.Errorf("suspend: %v", err)
	}
	if _, err := c.CreateSnapshot(ctx, sb.ID); !api.IsCode(err, api.CodeUnsupported) {
		t.Errorf("snapshot: %v", err)
	}
	if _, err := c.SetSnapshotSchedule(ctx, sb.ID, api.SnapshotSchedule{EverySecs: 3600}); !api.IsCode(err, api.CodeUnsupported) {
		t.Errorf("snapshot schedule: %v", err)
	}
	if _, err := c.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 2}); !api.IsCode(err, api.CodeUnsupported) {
		t.Errorf("resize: %v", err)
	}
	suspend := api.IdleSuspend
	if _, err := c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{IdleAction: &suspend}); !api.IsCode(err, api.CodeUnsupported) {
		t.Errorf("idle_action suspend: %v", err)
	}
	found := false
	audit.Lines(events, func(line []byte) {
		var e api.Event
		if json.Unmarshal(line, &e) == nil && e.Type == api.EventSandboxCreated && e.Sandbox == sb.ID {
			found = slices.Equal(e.GPUs, sb.GPUs)
		}
	})
	if !found {
		t.Error("the created event does not name the GPU")
	}
}

// An endpoint with no GPUs says so: unsupported, and a limit of 0.
func TestNoGPUs(t *testing.T) {
	_, c, _ := gpuServer(t, nil)
	ctx := context.Background()
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: 1}); !api.IsCode(err, api.CodeUnsupported) {
		t.Fatalf("gpus on an endpoint without: %v", err)
	}
	caps, err := c.Capabilities(ctx)
	if err != nil || caps.Has(api.CapGPU) || caps.Limits.MaxGPUs != 0 {
		t.Fatalf("capabilities: gpu %v, max_gpus %d, %v", caps.Has(api.CapGPU), caps.Limits.MaxGPUs, err)
	}
}

// A restart that keeps sandboxes keeps their GPUs held: the record names
// them, and what is free is read from the records.
func TestKeptSandboxesKeepTheirGPUs(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist, api.CapGPU)
	dir := newKeepDir(t)
	serve := func() http.Handler {
		s := &Server{Backend: b, Policy: spec.DefaultPolicyFor(b.Capabilities()), RecordDir: dir, GPUs: testGPUs[:1]}
		if _, err := s.Restore(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for id := range b.Kept() {
				_ = b.Terminate(context.Background(), id)
			}
			s.watchers.Wait()
		})
		return s.Handler()
	}
	h1 := serve()
	sb := mustCreate(t, h1, api.CreateSandboxRequest{GPUs: 1})
	b.Detach()
	h2 := serve()
	if r := call(t, h2, "POST", "/v1/sandboxes", api.CreateSandboxRequest{GPUs: 1}); r.Code != http.StatusServiceUnavailable {
		t.Fatalf("a create for the GPU a kept sandbox holds: %d %s", r.Code, r.Body)
	}
	var got api.Sandbox
	_ = json.Unmarshal(call(t, h2, "GET", "/v1/sandboxes/"+sb.ID, nil).Body.Bytes(), &got)
	if !slices.Equal(got.GPUs, sb.GPUs) {
		t.Fatalf("taken back with %v, was %v", got.GPUs, sb.GPUs)
	}
}

// A GPU sandbox never takes a pooled VM's place: the pool's VMs have none.
func TestPoolKeyRefusesGPUs(t *testing.T) {
	if k := poolKey(backend.Spec{Image: "x", GPUs: []string{"0000:01:00.0"}}); k != "" {
		t.Fatalf("a GPU spec has pool key %q", k)
	}
}

// A node's max_gpus is at most the GPUs it has: a request for more than that
// is invalid, not unavailable, since no wait gives the node a third GPU.
func TestMaxGPUsIsTheNodesCount(t *testing.T) {
	_, c, _ := gpuServer(t, testGPUs, api.CapGPU)
	ctx := context.Background()
	caps, err := c.Capabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if caps.Limits.MaxGPUs != len(testGPUs) {
		t.Errorf("max_gpus %d on a node with %d", caps.Limits.MaxGPUs, len(testGPUs))
	}
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: len(testGPUs) + 1}); !api.IsCode(err, api.CodeInvalidRequest) {
		t.Errorf("more GPUs than the node has: %v, want invalid_request", err)
	}
}
