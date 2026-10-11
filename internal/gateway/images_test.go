package gateway

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A user behind a gateway can see what the nodes have built, before making
// any sandbox; installing stays each node's operator's.
func TestFleetImagesListsWhatNodesBuilt(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.user("ana")
	// The node builds its default image as the first sandbox starts; any
	// sandbox's own, to compare with.
	sb, err := tg.admin.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the node's status to list the image", func() bool {
		tg.g.nodes.pollAll(ctx)
		imgs, err := ana.FleetImages(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range imgs {
			if i.Image == sb.Image && i.State == "installed" {
				return true
			}
		}
		return false
	})
	// The endpoint without the images capability still says so.
	_, err = ana.Images(ctx)
	wantCode(t, err, api.CodeUnsupported)
	_, err = tg.client("nobody", "", ScopeSSH).FleetImages(ctx)
	wantCode(t, err, api.CodeRefused)
}
