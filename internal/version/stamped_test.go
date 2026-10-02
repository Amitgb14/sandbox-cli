package version

import (
	"os"
	"strings"
	"testing"
)

// Every build that ships has to stamp the version, and nothing was checking it.
//
// This is the same failure `TestSiteVersionMatchesTheBinary` exists for, one copy
// over. `Version` is a compile-time default — `0.0.1` — overridden by
// `-X .../internal/version.Version=…`, which the Makefile does and
// `Dockerfile.studio-api` did not. So every published `sandbox-studio-api` image
// reported `0.0.1` forever, tagged releases included, and Studio's own header
// showed it. A plain `go build` is not the problem: it is for developers, and a
// developer's binary saying the default is honest. A *published artefact* saying
// it is not.
//
// Checked by reading the build files rather than by running a build, because the
// failure is a missing flag and a missing flag is visible in the text. A test that
// ran `docker build` would be a test nobody runs.
func TestEveryShippedBuildStampsTheVersion(t *testing.T) {
	const flag = "internal/version.Version="

	// Each entry is a file that produces something somebody else runs.
	for _, f := range []struct {
		path string
		why  string
	}{
		{"../../Makefile", "the binaries a release is cut from"},
		{"../../Dockerfile.studio-api", "the published control-plane image"},
	} {
		b, err := os.ReadFile(f.path)
		if err != nil {
			t.Skipf("no %s to check (%v)", f.path, err)
			continue
		}
		if !strings.Contains(string(b), flag) {
			t.Errorf("%s builds %s and does not pass -X %s…\n"+
				"  Without it the binary reports internal/version's compile-time default (%s)\n"+
				"  whatever commit it was built from — which is how the studio-api image\n"+
				"  reported %s through every release.", f.path, f.why, flag, Version, Version)
		}
	}

	// And the other half: the Dockerfile takes the version from outside rather than
	// hardcoding one, and CI passes it. A Dockerfile with the flag and a literal
	// after it would satisfy the check above while being wrong in a worse way — it
	// would be confidently specific.
	df, err := os.ReadFile("../../Dockerfile.studio-api")
	if err == nil {
		if !strings.Contains(string(df), "ARG VERSION") {
			t.Error("Dockerfile.studio-api stamps a version without an ARG to supply it; a hardcoded one goes stale silently")
		}
	}
	wf, err := os.ReadFile("../../.github/workflows/images.yml")
	if err == nil {
		if !strings.Contains(string(wf), "VERSION=") {
			t.Error("the image workflow builds Dockerfile.studio-api without passing VERSION, so the ARG's default is what ships")
		}
		// `git describe` needs the tags, and a default checkout has none. Without
		// this the version degrades to a bare sha — which is not wrong, but is a
		// quiet loss of the lineage the flag exists to carry.
		if strings.Contains(string(wf), "git describe") && !strings.Contains(string(wf), "fetch-depth: 0") {
			t.Error("the image workflow runs `git describe` on a shallow checkout; add fetch-depth: 0 or it resolves to a bare sha")
		}
	}
}
