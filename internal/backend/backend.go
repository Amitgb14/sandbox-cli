// Package backend is the seam between what a sandbox should be and the engine
// that makes one.
//
// The old tree had a Runtime interface, but it was docker-shaped — networks,
// labels as the only state store, `docker logs`, the engine's API socket — and
// twelve files named the concrete DockerCLI anyway. Here every caller holds a
// Backend, and a backend says what it can honour through Capabilities, so that
// spec can refuse a request a backend would silently weaken.
//
// Filled in at M2. backend/oci (docker and podman, one dialect) is the first
// implementation; backend/fake is for tests.
package backend
