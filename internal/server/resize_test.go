package server

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

func resizeServer(t *testing.T, caps ...string) (*fake.Backend, *api.Client) {
	t.Helper()
	be := fake.New(caps...)
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), Capacity: api.NodeResources{CPUs: 16, MemoryMB: 32768, DiskMB: 1 << 20},
		Audit: audit.NewLog(filepath.Join(t.TempDir(), "events.jsonl"))}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c, _ := api.NewClient(ts.URL, "")
	return be, c
}

func TestResizeChangesWhatTheSandboxIsGiven(t *testing.T) {
	be, c := resizeServer(t, api.CapResize)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 1, MemoryMB: 1024, DiskMB: 2048})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 4, DiskMB: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUs != 4 || got.MemoryMB != 1024 || got.DiskMB != 4096 {
		t.Fatalf("resized to %v CPUs, %d MB, disk %d; want 4, 1024 (kept), 4096", got.CPUs, got.MemoryMB, got.DiskMB)
	}
	if size, _ := be.Size(sb.ID); size != (api.NodeResources{CPUs: 4, MemoryMB: 1024, DiskMB: 4096}) {
		t.Fatalf("the backend has %+v", size)
	}
	if again, _ := c.Sandbox(ctx, sb.ID); again.CPUs != 4 || again.DiskMB != 4096 {
		t.Fatalf("the listing says %v CPUs, disk %d", again.CPUs, again.DiskMB)
	}
	node, err := c.Node(ctx)
	if err != nil || node.Free.CPUs != 12 {
		t.Fatalf("the node's free CPUs: %v, %v; want 12", node.Free.CPUs, err)
	}
	evs, err := c.Events(ctx, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs.Events {
		found = found || (e.Type == api.EventSandboxResized && strings.Contains(e.Reason, "-> 4 CPUs"))
	}
	if !found {
		t.Fatalf("no resize event: %+v", evs.Events)
	}

	// Refused: a smaller disk, more than the limits, a size of nothing.
	for name, req := range map[string]api.ResizeSandboxRequest{
		"a smaller disk":   {DiskMB: 1024},
		"too many CPUs":    {CPUs: 1000},
		"too much memory":  {MemoryMB: 1 << 30},
		"too little":       {MemoryMB: 64},
		"negative CPUs":    {CPUs: -1},
		"a too large disk": {DiskMB: 1 << 30},
	} {
		if _, err := c.Resize(ctx, sb.ID, req); !api.IsCode(err, api.CodeInvalidRequest) {
			t.Errorf("%s: %v, want invalid_request", name, err)
		}
	}
}

// A resize restarts the sandbox, so it is refused while a process runs.
func TestResizeWaitsForProcesses(t *testing.T) {
	_, c := resizeServer(t, api.CapResize)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"sleep", "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 2}); !api.IsCode(err, api.CodeConflict) {
		t.Fatalf("resize with %d running: %v, want conflict", p.PID, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := c.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 2}); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("after the process ended: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestResizeIsRefusedWhereItCannotBeDone(t *testing.T) {
	_, c := resizeServer(t)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 2}); !api.IsCode(err, api.CodeUnsupported) {
		t.Fatalf("%v, want unsupported", err)
	}
}
