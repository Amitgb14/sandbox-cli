package server

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

func imagesServer(t *testing.T, mod func(*spec.Policy)) *api.Client {
	t.Helper()
	be := fake.New(api.CapEgressAllowlist)
	pol := spec.DefaultPolicyFor(be.Capabilities())
	if mod != nil {
		mod(&pol)
	}
	ts := httptest.NewServer((&Server{Backend: be, Policy: pol}).Handler())
	t.Cleanup(ts.Close)
	c, err := api.NewClient(ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// imageNamed waits for ref to leave installing, and returns it as listed.
func imageNamed(t *testing.T, c *api.Client, ref string) (api.Image, bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		list, err := c.Images(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(list, func(img api.Image) bool { return img.Image == ref })
		if i < 0 {
			return api.Image{}, false
		}
		if list[i].State != api.ImageInstalling || time.Now().After(deadline) {
			return list[i], true
		}
	}
}

func wantAPICode(t *testing.T, err error, code string) {
	t.Helper()
	if !api.IsCode(err, code) {
		t.Fatalf("error %v; want %s", err, code)
	}
}

// An image is installed ahead of use, counted while a sandbox starts from
// it, and removed only once none does.
func TestImagesAreInstalledUsedAndRemoved(t *testing.T) {
	c := imagesServer(t, nil)
	ctx := context.Background()
	caps, _ := c.Capabilities(ctx)
	if !caps.Has(api.CapImages) {
		t.Fatal("no images capability on a backend that manages images")
	}
	const ref = "registry.example/team/tool:1.0"
	got, err := c.InstallImage(ctx, ref)
	if err != nil || got.State != api.ImageInstalling || got.Progress == nil {
		t.Fatalf("install: %+v, %v", got, err)
	}
	img, ok := imageNamed(t, c, ref)
	if !ok || img.State != api.ImageInstalled || img.Bytes == 0 || img.InstalledAt == nil || img.InUse != 0 {
		t.Fatalf("listed %+v, %v", img, ok)
	}

	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Image: ref})
	if err != nil {
		t.Fatal(err)
	}
	if img, _ := imageNamed(t, c, ref); img.InUse != 1 {
		t.Errorf("in_use %d with a sandbox started from it", img.InUse)
	}
	_, err = c.RemoveImage(ctx, ref)
	wantAPICode(t, err, api.CodeConflict)
	if err := c.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	removed, err := c.RemoveImage(ctx, ref)
	if err != nil || removed.FreedBytes == 0 {
		t.Fatalf("remove: %+v, %v", removed, err)
	}
	if _, ok := imageNamed(t, c, ref); ok {
		t.Error("a removed image is still listed")
	}
	_, err = c.RemoveImage(ctx, ref)
	wantAPICode(t, err, api.CodeNotFound)
}

// What may not be installed or removed is refused, and a failed install
// says why until it is cleared.
func TestImageRefusals(t *testing.T) {
	c := imagesServer(t, func(p *spec.Policy) {
		p.Images = []string{p.DefaultImage, "ok.example/a:1", "x/" + fake.MissingImage + ":1"}
	})
	ctx := context.Background()
	for name, ref := range map[string]string{"not a reference": "Not An Image!", "outside the policy's list": "other.example/b:1"} {
		_, err := c.InstallImage(ctx, ref)
		if err == nil {
			t.Errorf("%s: installed", name)
		}
	}
	_, err := c.InstallImage(ctx, "other.example/b:1")
	wantAPICode(t, err, api.CodeRefused)

	// The default image is pinned, installed or not.
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{}); err != nil {
		t.Fatal(err)
	}
	list, _ := c.Images(ctx)
	var def *api.Image
	for i := range list {
		if list[i].Default {
			def = &list[i]
		}
	}
	if def == nil || def.InUse != 1 {
		t.Fatalf("the default image is not listed as default and in use: %+v", list)
	}
	_, err = c.RemoveImage(ctx, def.Image)
	wantAPICode(t, err, api.CodeConflict)

	missing := "x/" + fake.MissingImage + ":1"
	if _, err := c.InstallImage(ctx, missing); err != nil {
		t.Fatal(err)
	}
	img, ok := imageNamed(t, c, missing)
	if !ok || img.State != api.ImageFailed || !strings.Contains(img.Error, "manifest unknown") {
		t.Fatalf("a failed install is listed as %+v, %v", img, ok)
	}
	if _, err := c.RemoveImage(ctx, missing); err != nil {
		t.Fatalf("clearing a failed install: %v", err)
	}
	if _, ok := imageNamed(t, c, missing); ok {
		t.Error("a cleared failure is still listed")
	}
}

// Two asks for one image while it installs are one install.
func TestAnInstallUnderWayIsShared(t *testing.T) {
	c := imagesServer(t, nil)
	ctx := context.Background()
	const ref = "registry.example/team/tool:2.0"
	a, err1 := c.InstallImage(ctx, ref)
	b, err2 := c.InstallImage(ctx, ref)
	if err1 != nil || err2 != nil || a.Image != ref || b.Image != ref {
		t.Fatalf("%+v %v / %+v %v", a, err1, b, err2)
	}
	list, _ := c.Images(ctx)
	n := 0
	for _, img := range list {
		if img.Image == ref {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("listed %d times", n)
	}
}
