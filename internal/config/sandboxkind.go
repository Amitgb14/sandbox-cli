package config

import "fmt"

// SandboxKind is what actually isolates a run — the thing that stands between an
// agent and the host.
//
// It is deliberately a *different* question from `engine`, which asks which
// binary talks to a container daemon. Today the two answers coincide, because
// every kind that works is a container kind; the type exists so that the
// question can be asked before a second answer exists, and so the answer is
// recorded on a pane rather than inferred from whichever engine the machine
// happened to have.
//
// Two of the four values name work that has not been done, and they are declared
// rather than omitted for one reason: the session server records a pane's kind in
// its catalog, and a catalog whose enum grows later cannot be read by an older
// reader. Naming them now costs a refusal each; discovering them later costs a
// migration. `SandboxKind.Available` is what separates the two groups, and no
// code path may treat an unavailable kind as a weaker-but-acceptable fallback —
// the whole point of the type is that the answer is explicit.
type SandboxKind string

const (
	// SandboxDocker and SandboxPodman are the container kinds, and the only two
	// that run anything today. Which one a run gets follows `engine` unless a
	// caller says otherwise, so the zero value means "whatever the engine is".
	SandboxDocker SandboxKind = "docker"
	SandboxPodman SandboxKind = "podman"

	// SandboxBwrap is a bubblewrap namespace sandbox: no daemon, no image, much
	// less to install — and a weaker boundary, since it shares the host kernel
	// with none of the runtime work that makes that acceptable. Not implemented.
	SandboxBwrap SandboxKind = "bwrap"

	// SandboxNone is no sandbox at all: the agent runs as you, on your machine,
	// with your files. It is named here so that "there is no isolation" is a
	// value the catalog can hold and a person can read, rather than a state the
	// tool can drift into. Not implemented, and see RefuseUnimplemented for why
	// "dev-only" was not considered protection enough.
	SandboxNone SandboxKind = "none"
)

// KnownSandboxKind reports whether s names a kind at all. An unknown value is a
// typo and must be an error rather than a default: `sandbox: dokcer` silently
// falling back to a container is the good case, and silently falling back to
// something else is the bad one.
func KnownSandboxKind(s SandboxKind) bool {
	switch s {
	case SandboxDocker, SandboxPodman, SandboxBwrap, SandboxNone:
		return true
	}
	return false
}

// Available reports whether this kind can actually run something today.
//
// The split is the honest form of "not implemented yet". A kind that is known,
// unavailable and refused is a feature that has not landed; a kind that is known,
// unavailable and quietly ignored is a boundary that is not there.
func (s SandboxKind) Available() bool {
	return s == SandboxDocker || s == SandboxPodman
}

// Resolve says which kind a run gets, given the engine in force. The zero value
// follows the engine, so a config that never mentions `sandbox` behaves exactly
// as it did before the field existed.
func (s SandboxKind) Resolve(engine string) SandboxKind {
	if s != "" {
		return s
	}
	if engine == "podman" {
		return SandboxPodman
	}
	return SandboxDocker
}

// RefuseUnimplemented is the error for a kind that is spelled correctly and does
// not work yet.
//
// It refuses under **every** profile, which is the one decision in this file
// worth arguing. The obvious alternative — allow `none` under dev, refuse it
// under prod — reads like the asymmetry the profiles already use, and is not the
// same thing at all. Dev is the *default* profile: almost every run on almost
// every machine is a dev run, so "dev-only" would make an unimplemented
// no-isolation mode reachable by the ordinary path rather than an unusual one.
// The profile asymmetry is about whether a control that *could not be applied* is
// a warning or a failure. This is about a control that was not written.
func RefuseUnimplemented(s SandboxKind) error {
	return fmt.Errorf(
		"sandbox %q is not implemented: sandbox-cli isolates with a container today, so %q would mean no boundary at all rather than a different one.\n"+
			"  Use \"docker\" or \"podman\", or leave `sandbox` unset to follow `engine`.",
		string(s), string(s))
}
