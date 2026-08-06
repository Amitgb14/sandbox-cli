// Package version holds build metadata and the base image tag.
package version

// Version is the sandbox-cli release version. Overridable at build time via
// -ldflags "-X github.com/Amitgb14/sandbox-cli/internal/version.Version=x.y.z".
var Version = "0.0.1beta.11"

// BaseImageVersion is the human-readable generation of the base image. It is
// only the prefix of the image tag: the full reference (image.Ref) appends a
// hash of the embedded Dockerfile, so any change to the image content produces
// a new tag and rebuilds automatically. Bump this only to mark a new image
// generation for readability — correctness does not depend on it.
const BaseImageVersion = "0.0.1"
