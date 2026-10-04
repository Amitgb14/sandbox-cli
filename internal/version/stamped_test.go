package version

import (
	"os"
	"strings"
	"testing"
)

// Every build that ships has to stamp the version, and nothing was checking it.
//
// `Version` is a compile-time default, overridden by
// `-X .../internal/version.Version=…`. On beta.15's line the published
// control-plane image built without the flag and reported the default from
// every commit, tagged releases included. A plain `go build` saying the default
// is honest — it is a developer's binary; a published artefact saying it is not.
//
// Checked by reading the build files rather than by running a build, because
// the failure is a missing flag, and a missing flag is visible in the text.
func TestEveryShippedBuildStampsTheVersion(t *testing.T) {
	const flag = "internal/version.Version="
	for _, f := range []struct{ path, why string }{
		{"../../Makefile", "the binaries a developer or a release is cut from"},
		{"../../.goreleaser.yaml", "every released binary: sandbox-cli, sandboxd, sandbox-guestd"},
	} {
		b, err := os.ReadFile(f.path)
		if err != nil {
			t.Skipf("no %s to check (%v)", f.path, err)
		}
		if !strings.Contains(string(b), flag) {
			t.Errorf("%s builds %s and does not pass -X %s…", f.path, f.why, flag)
		}
	}
	// And every build goreleaser makes, not just the first: one binary of three
	// reporting the default is the same bug.
	b, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		return
	}
	builds := strings.Count(string(b), "main: ./cmd/")
	if stamped := strings.Count(string(b), flag); builds == 0 || stamped < builds {
		t.Errorf(".goreleaser.yaml has %d builds and stamps the version in %d", builds, stamped)
	}
	// And its own base image, so a release boots the image published with it
	// rather than whatever :edge has become (policy.DefaultImage).
	if img := strings.Count(string(b), "internal/policy.DefaultImage=ghcr.io/amitgb14/sandbox-base:{{ .Tag }}"); img < builds {
		t.Errorf(".goreleaser.yaml has %d builds and stamps the default image in %d", builds, img)
	}
}
