// Package spec folds configuration and per-invocation options into a fully
// resolved, backend-neutral RunSpec — and refuses what the chosen backend cannot
// honour, rather than letting it degrade at run time.
//
// Together with backend/oci.BuildArgs this is where isolation lives, and the
// only place. Rewritten at M2 from the old sandbox.BuildSpec.
package spec
