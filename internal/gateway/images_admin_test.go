package gateway

import (
	"slices"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
)

// noImages hides the fake's image store, as a backend that does not manage
// its images has none.
type noImages struct{ backend.Backend }

// adminImage is ref as GET /v1/admin/images lists it.
func adminImage(t *testing.T, tg *testGateway, ref string) (api.Image, bool) {
	t.Helper()
	l, err := tg.admin.AdminImages(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(l, func(img api.Image) bool { return img.Image == ref })
	if i < 0 {
		return api.Image{}, false
	}
	return l[i], true
}

func nodesOf(img api.Image) []string {
	var out []string
	for _, n := range img.Nodes {
		out = append(out, n.Node)
	}
	return out
}

// Only an admin manages the nodes' images: an image on a node is disk every
// tenant's sandboxes share.
func TestOnlyAnAdminManagesTheNodesImages(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	bob := tg.user("bob")
	_, err := bob.AdminImages(ctx)
	wantCode(t, err, api.CodeRefused)
	_, err = bob.AdminInstallImage(ctx, "example.com/tools:1")
	wantCode(t, err, api.CodeRefused)
	_, err = bob.AdminRemoveImage(ctx, "example.com/tools:1")
	wantCode(t, err, api.CodeRefused)
	if _, ok := adminImage(t, tg, "example.com/tools:1"); ok {
		t.Fatal("a user's install reached a node")
	}
}

// An install goes to every node that takes new sandboxes; the listing says
// where each image is; a removal is refused while any node needs the image,
// and then takes it from every node.
func TestAdminInstallsAndRemovesAcrossNodes(t *testing.T) {
	n1, n2, n3 := startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...), startNode(t, "n3", allCaps...)
	tg := startGateway(t, nil, n1, n2, n3)
	ctx := ctxT(t)
	if _, err := tg.admin.CordonNode(ctx, "n3", true); err != nil {
		t.Fatal(err)
	}
	const ref = "example.com/tools:1"

	img, err := tg.admin.AdminInstallImage(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := nodesOf(img); !slices.Equal(got, []string{"n1", "n2"}) {
		t.Errorf("the install went to %v; want the uncordoned n1 and n2", got)
	}
	waitFor(t, "the image installed on n1 and n2", func() bool {
		img, ok := adminImage(t, tg, ref)
		return ok && img.State == api.ImageInstalled && slices.Equal(nodesOf(img), []string{"n1", "n2"})
	})

	// A sandbox from it on one node holds it on every node.
	bob := tg.user("bob")
	sb, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{Image: ref})
	if err != nil {
		t.Fatal(err)
	}
	if img, _ := adminImage(t, tg, ref); img.InUse != 1 {
		t.Errorf("in use %d; want 1", img.InUse)
	}
	_, err = tg.admin.AdminRemoveImage(ctx, ref)
	wantCode(t, err, api.CodeConflict)
	if img, _ := adminImage(t, tg, ref); !slices.Equal(nodesOf(img), []string{"n1", "n2"}) {
		t.Fatalf("a refused removal took the image from %v", img.Nodes)
	}

	if err := bob.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the image out of use", func() bool { img, _ := adminImage(t, tg, ref); return img.InUse == 0 })
	rm, err := tg.admin.AdminRemoveImage(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if rm.FreedBytes != 2*(64<<20) {
		t.Errorf("freed %d bytes; want both nodes' copies", rm.FreedBytes)
	}
	if _, ok := adminImage(t, tg, ref); ok {
		t.Error("the image is still listed after its removal")
	}
	_, err = tg.admin.AdminRemoveImage(ctx, ref)
	wantCode(t, err, api.CodeNotFound)
	_, err = tg.admin.AdminRemoveImage(ctx, "")
	wantCode(t, err, api.CodeInvalidRequest)
}

// A node that does not manage images is left out of the listing, and an
// install no node accepts answers with what the node said.
func TestAdminImagesWithoutTheCapability(t *testing.T) {
	n1 := startNodeWith(t, "n1", func(f *fake.Backend) backend.Backend { return noImages{f} }, allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	l, err := tg.admin.AdminImages(ctx)
	if err != nil || len(l) != 0 {
		t.Fatalf("images = %+v, %v; want none", l, err)
	}
	_, err = tg.admin.AdminInstallImage(ctx, "example.com/tools:1")
	wantCode(t, err, api.CodeUnsupported)
}
