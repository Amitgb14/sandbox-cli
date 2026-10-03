// Package image lazily builds the embedded sandbox base image on first use.
package image

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/version"

	"github.com/Amitgb14/sandbox-cli/internal/egressproxy"
)

//go:embed assets/Dockerfile
var dockerfile []byte

// Ref is the reference of the base image this build produces:
//
//	sandbox-base:<generation>-<short hash of the embedded Dockerfile>
//
// The hash makes the tag content-addressed, so editing assets/Dockerfile always
// yields a new tag. EnsureImage then sees the image as absent and rebuilds it.
// Relying on a hand-bumped version instead let four image changes ship under one
// tag, leaving stale images that silently lacked the egress entrypoint and the
// pre-created cache directories.
func Ref() string {
	// The tag is content-addressed over everything that goes into the image, not
	// only the Dockerfile. The egress proxy's source is compiled into the image
	// too, so hashing the Dockerfile alone would mean a changed proxy shipped
	// under the old tag and nobody rebuilt — a stale binary enforcing the
	// allowlist, which is the worst thing to be stale.
	h := sha256.New()
	h.Write(dockerfile)
	for _, name := range egressproxy.EmbeddedFiles() {
		src, err := egressproxy.SourceOf(name)
		if err != nil {
			continue
		}
		h.Write([]byte(name))
		h.Write([]byte(src))
	}
	h.Write([]byte(egressproxy.GeneratedSources()))
	return "sandbox-base:" + version.BaseImageVersion + "-" + hex.EncodeToString(h.Sum(nil))[:8]
}

// Register wires the image builder into a DockerCLI runtime so EnsureImage can
// build a missing base image. Call this once at startup.
func Register(d *runtime.DockerCLI) {
	d.SetBuilder(func(ctx context.Context, ref string) error {
		return Build(ctx, d.Bin, ref)
	}, Ref())
}

// Build builds ref from the embedded Dockerfile. The build context is a temp
// dir containing only the Dockerfile (the image needs no local files). Progress
// streams to stderr.
func Build(ctx context.Context, dockerBin, ref string) error {
	if dockerBin == "" {
		dockerBin = "docker"
	}

	tmp, err := os.MkdirTemp("", "sandbox-build-*")
	if err != nil {
		return fmt.Errorf("creating build context: %w", err)
	}
	defer os.RemoveAll(tmp)

	dfPath := filepath.Join(tmp, "Dockerfile")
	if err := os.WriteFile(dfPath, dockerfile, 0o644); err != nil {
		return fmt.Errorf("writing Dockerfile: %w", err)
	}

	// The egress proxy runs inside the container, so its source has to be in the
	// build context. It is compiled by a builder stage rather than cross-compiled
	// here, because sandbox-cli ships as a binary and most users have no Go
	// toolchain — requiring one would be a new prerequisite for everybody.
	if err := egressproxy.WriteBuildContext(tmp, func(name string, data []byte) error {
		p := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, data, 0o644)
	}); err != nil {
		return fmt.Errorf("writing proxy source: %w", err)
	}

	fmt.Fprintf(os.Stderr, "sandbox-cli: building base image %s (first run only)...\n", ref)
	cmd := exec.CommandContext(ctx, dockerBin, "build", "-t", ref, "-f", dfPath, tmp)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}
