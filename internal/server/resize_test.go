package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
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

// New CPUs restart the sandbox, so they are refused while a process runs.
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

// A disk that only grows is grown live while a process runs: the backend is
// told it may not restart, and the answer says it was not restarted. New
// CPUs at the same time are still refused.
func TestADiskGrowsLiveWhileAProcessRuns(t *testing.T) {
	be, c := resizeServer(t, api.CapResize)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{DiskMB: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"sleep", "30"}}); err != nil {
		t.Fatal(err)
	}
	got, err := c.Resize(ctx, sb.ID, api.ResizeSandboxRequest{DiskMB: 4096})
	if err != nil || got.Restarted || got.DiskMB != 4096 {
		t.Fatalf("a disk grown with a process running: %+v, %v; want 4096, not restarted", got, err)
	}
	if size, _ := be.Size(sb.ID); size.DiskMB != 4096 {
		t.Fatalf("the backend has %+v", size)
	}
	if _, err := c.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 2, DiskMB: 8192}); !api.IsCode(err, api.CodeConflict) {
		t.Fatalf("new CPUs with the disk, a process running: %v, want conflict", err)
	}
	if size, _ := be.Size(sb.ID); size.DiskMB != 4096 {
		t.Fatalf("a refused resize changed the disk: %+v", size)
	}
}

// noLiveGrow is a backend whose disk cannot be grown live (an old guest
// agent): it needs a restart, and refuses one it is not allowed.
type noLiveGrow struct{ *fake.Backend }

func (b noLiveGrow) ResizeSandbox(ctx context.Context, id string, size api.NodeResources, mayRestart bool) (api.NodeResources, bool, error) {
	if !mayRestart {
		now, _ := b.Size(id)
		return now, false, fmt.Errorf("%w: needs a restart", backend.ErrBusy)
	}
	return b.Backend.ResizeSandbox(ctx, id, size, true)
}

// Where the disk cannot be grown live, a process running refuses it with
// a conflict that says why, rather than a restart that would end it.
func TestADiskThatNeedsARestartWaitsForProcesses(t *testing.T) {
	be := noLiveGrow{fake.New(api.CapResize)}
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), Capacity: api.NodeResources{CPUs: 16, MemoryMB: 32768, DiskMB: 1 << 20}}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	c, _ := api.NewClient(ts.URL, "")
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{DiskMB: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"sleep", "30"}}); err != nil {
		t.Fatal(err)
	}
	_, err = c.Resize(ctx, sb.ID, api.ResizeSandboxRequest{DiskMB: 4096})
	if !api.IsCode(err, api.CodeConflict) || !strings.Contains(err.Error(), "without a restart") {
		t.Fatalf("%v, want a conflict saying the disk needs a restart", err)
	}
}
