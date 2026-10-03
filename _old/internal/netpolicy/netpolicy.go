// Package netpolicy is a seam for network egress control. The MVP honors only
// the "default" and "none" modes carried on RunSpec.Network. A later milestone
// can add an egress proxy with a domain allowlist (github.com, npm, pypi, ...).
package netpolicy

import "github.com/Amitgb14/sandbox-cli/internal/runtime"

// Policy adjusts a RunSpec to enforce a network posture.
type Policy interface {
	Apply(spec *runtime.RunSpec) error
}

// ModePolicy applies a simple docker --network mode. This is the MVP policy.
type ModePolicy struct {
	Mode string // "" or "default" => bridge; "none" => no network
}

// Apply sets the spec's Network field from the mode.
func (p ModePolicy) Apply(spec *runtime.RunSpec) error {
	if p.Mode == "none" {
		spec.Network = "none"
	}
	return nil
}
