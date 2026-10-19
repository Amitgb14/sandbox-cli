package gateway

import (
	"context"
	"slices"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/api/conformance"
)

// A fleet where one node of two has GPUs: the fleet offers them, a GPU
// sandbox goes to that node however much room the other has, a burst of them
// takes no more than it has, and the suite holds as on any fleet.
func TestGPUSandboxesGoToTheNodeWithGPUs(t *testing.T) {
	plain := startNode(t, "plain", allCaps...)
	gpu := startNode(t, "gpu", append(slices.Clone(allCaps), api.CapGPU)...)
	plain.capacity.MemoryMB = 1 << 20 // the roomier node, preferred for anything else
	tg := startGateway(t, nil, plain, gpu)
	c := tg.user("gpu-user")
	ctx := context.Background()

	caps, err := c.Capabilities(ctx)
	if err != nil || !caps.Has(api.CapGPU) || caps.Limits.MaxGPUs == 0 {
		t.Fatalf("the fleet's capabilities: gpu %v, max_gpus %d, %v", caps.Has(api.CapGPU), caps.Limits.MaxGPUs, err)
	}
	var held, ids []string
	for range 2 {
		sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: 1})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.TerminateSandbox(context.Background(), sb.ID) })
		if gpu.be.GPUs(sb.ID) == nil {
			t.Fatalf("sandbox %s was not created on the GPU node", sb.ID)
		}
		held, ids = append(held, sb.GPUs...), append(ids, sb.ID)
	}
	slices.Sort(held)
	if !slices.Equal(held, []string{"0000:01:00.0", "0000:41:00.0"}) {
		t.Fatalf("two GPU sandboxes hold %v", held)
	}
	_, err = c.CreateSandbox(ctx, api.CreateSandboxRequest{GPUs: 1})
	wantCode(t, err, api.CodeUnavailable)

	// Terminated through the gateway, the GPUs are free for the suite's.
	for _, id := range ids {
		if err := c.TerminateSandbox(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	conformance.Run(t, tg.user("suite"))
}
