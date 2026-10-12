package gateway

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A resize through the gateway reaches the sandbox's node, is recorded, and
// is held to the owner's quota by what it adds, as a create is.
func TestResizeIsHeldToTheQuotaByItsGrowth(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, func(c *Config) { c.Quota = Quota{Sandboxes: 2, CPUs: 4, MemoryMB: 8192} }, n1)
	ctx := ctxT(t)
	ann := tg.client("ann", "acme", ScopeRead, ScopeCreate, ScopeDelete)
	sb, err := ann.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 1, MemoryMB: 1024, DiskMB: 2048})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ann.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 3, MemoryMB: 4096, DiskMB: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUs != 3 || got.MemoryMB != 4096 || got.DiskMB != 4096 {
		t.Fatalf("resized to %+v", got)
	}
	if size, _ := n1.be.Size(sb.ID); size != (api.NodeResources{CPUs: 3, MemoryMB: 4096, DiskMB: 4096}) {
		t.Fatalf("the node's sandbox is %+v", size)
	}
	if u := tg.store.UsageOf("acme"); u.Sandboxes != 1 || u.CPUs != 3 || u.MemoryMB != 4096 {
		t.Fatalf("usage after the resize: %+v", u)
	}

	// 3 of 4 CPUs are used: growing by 2 more is refused, by 1 is not; a
	// sandbox of 1 CPU still fits beside it, since the resize counted none.
	_, err = ann.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 5})
	wantCode(t, err, api.CodeRefused)
	if size, _ := n1.be.Size(sb.ID); size.CPUs != 3 {
		t.Fatalf("a refused resize reached the node: %+v", size)
	}
	if _, err := ann.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 2}); err != nil {
		t.Fatalf("shrinking: %v", err)
	}
	if _, err := ann.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 1}); err != nil {
		t.Fatalf("a create beside it: %v", err)
	}

	// Only its owner's tenant reaches it; a key without create cannot.
	other := tg.client("bob", "globex", ScopeRead, ScopeCreate)
	_, err = other.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 1})
	wantCode(t, err, api.CodeNotFound)
	reader := tg.client("ann", "acme", ScopeRead)
	_, err = reader.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 1})
	wantCode(t, err, api.CodeRefused)
}

// The sandbox cannot move, so growth its node has no room for is refused
// there, before the node is asked.
func TestResizeNeedsRoomOnItsNode(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	ann := tg.client("ann", "acme", ScopeRead, ScopeCreate)
	sb, err := ann.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ann.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 100})
	wantCode(t, err, api.CodeConflict)
	if size, _ := n1.be.Size(sb.ID); size.CPUs != 1 {
		t.Fatalf("a resize without room reached the node: %+v", size)
	}
}
