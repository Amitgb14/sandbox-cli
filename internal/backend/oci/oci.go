// Package oci is the docker and podman backend: one backend with a dialect, not
// two, because the engines differ in a handful of measured places rather than in
// kind. BuildArgs stays a pure, deterministic function of the RunSpec — the
// property the dry-run golden test depends on.
//
// Ported from the old internal/runtime at M2.
package oci
