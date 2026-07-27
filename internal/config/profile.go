package config

import (
	"fmt"
	"sort"
	"strings"
)

// Security profiles: one tool, two deployments, and no insecure mode.
//
// sandbox-cli serves interactive local development and unattended production
// agents. Those pull in different directions, but the split is deliberately NOT
// "a lax mode and a strict mode": local development is where a prompt-injected
// agent has the most valuable thing in reach — the developer's machine, their
// credentials, their other repositories — so a profile that relaxed the host
// boundary would undo the work the boundary audit exists for.
//
// Both profiles are secure. They differ in what they optimise *within* a secure
// baseline, never in whether the baseline holds. Dev trades some containment of
// the agent's own reach for ergonomics; prod trades ergonomics for containment.
// Neither trades the host boundary, because that is not a preference.
//
// Design, threat model per profile, and the rejected alternatives:
// docs/proposals/security-profiles.md.
const (
	// ProfileDev is the interactive default: a developer is watching, so a
	// control that cannot be satisfied warns rather than refuses.
	ProfileDev = "dev"
	// ProfileProd is for unattended runs, which may carry untrusted agents.
	// Nobody is watching, so a control that cannot be satisfied refuses — a
	// production run that quietly proceeded in a weaker configuration than it
	// asked for is the failure this exists to prevent.
	ProfileProd = "prod"
)

// profileStrength orders the profiles so a project config can demand a stronger
// one and never a weaker one — the same direction-of-travel rule the network
// keys already follow.
var profileStrength = map[string]int{
	ProfileDev:  1,
	ProfileProd: 2,
}

// KnownProfile reports whether name is a profile sandbox-cli defines.
func KnownProfile(name string) bool {
	_, ok := profileStrength[name]
	return ok
}

// ProfileNames lists the defined profiles, weakest first.
func ProfileNames() []string {
	out := make([]string, 0, len(profileStrength))
	for n := range profileStrength {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return profileStrength[out[i]] < profileStrength[out[j]] })
	return out
}

// profileBase returns the configuration a profile starts from, applied as the
// base layer *under* the user's own config.
//
// Under, not over, so a user can still tune their own setup — their config is
// trusted, and a profile that could not be adjusted would be abandoned rather
// than used. What stops that from hollowing out prod is ValidateProfile below:
// the settings that make prod prod are checked on the *resolved* config, so they
// can be built upon but not tuned away.
func profileBase(name string) Config {
	cfg := Default()
	switch name {
	case ProfileProd:
		// Egress: the allowlist is the whole list. Only baseline-off closes
		// exfiltration, because the baseline contains github.com — a write
		// endpoint, and so a channel for any token the agent holds. Deliberately
		// left empty: prod must name the endpoints it needs, and an allowlist
		// that resolves to nothing already refuses the run.
		cfg.Network.Mode = "allowlist"
		cfg.Network.Baseline = boolPtr(false)

		// No long-lived credential in the container. This is the whole answer to
		// the credential problem for prod: the OAuth refresh token is never
		// mounted, so there is nothing to steal, and no TLS-terminating proxy is
		// needed to hide it. Prod authenticates with scoped, revocable tokens
		// through the secrets broker instead.
		cfg.PersistAuth = boolPtr(false)
		cfg.Sync = boolPtr(false)

		// A syscall filter is required rather than hoped for; see
		// ValidateProfile. Resource limits are bounded so one runaway agent
		// cannot take a shared host down.
		cfg.Security.Seccomp = SeccompRequired
		cfg.Security.Memory = "2g"
		cfg.Security.CPUs = "2"
		cfg.Security.PidsLimit = int64Ptr(512)

		// Prod may carry untrusted agents, so a container namespace is not the
		// boundary. Left empty rather than guessed: which stronger runtime is
		// registered is a property of the host, and `doctor --profile prod`
		// reports it rather than this file assuming it.
		cfg.Ports = []string{}
	case ProfileDev:
		// Today's defaults. Dev's own hardening is expressed in Default() so that
		// running without a profile and running `--profile dev` cannot drift
		// apart.
	}
	return cfg
}

// SeccompRequired is the Security.Seccomp value meaning "refuse to run unless
// the daemon actually applies a syscall filter".
//
// It is a sentinel rather than a bool because Seccomp already carries a profile
// path, and the three states that matter — take the daemon's default, use this
// profile, insist there is one — are one field's worth of meaning.
const SeccompRequired = "required"

// ResolveProfile picks the profile in force from the layers that may choose one.
//
// Precedence: an explicit --profile beats everything, then the strongest of what
// the user's config selected and what a project demanded, then dev.
//
// A project may only ever *raise* the profile. That asymmetry is the security
// property: if a .sandbox.yaml could select the weaker profile, a hostile
// repository would drop the user out of prod and every other control becomes
// decoration. Raising is safe and occasionally right — a repo that knows it
// handles untrusted input can insist on prod.
func ResolveProfile(flag, user, project string) (string, error) {
	if flag != "" {
		if !KnownProfile(flag) {
			return "", fmt.Errorf("unknown profile %q (known: %s)", flag, strings.Join(ProfileNames(), ", "))
		}
		return flag, nil
	}
	chosen := ProfileDev
	if KnownProfile(user) {
		chosen = user
	}
	if KnownProfile(project) && profileStrength[project] > profileStrength[chosen] {
		chosen = project
	}
	return chosen, nil
}

// ValidateProfile checks that a fully-resolved configuration still delivers what
// its profile promises.
//
// This is what keeps a named profile honest. profileBase sets prod's settings as
// a base layer, which a later layer could in principle undo; a profile that
// quietly stopped delivering would be worse than no profile at all, because it
// is trusted. So the settings that define prod are asserted here, against the
// configuration that will actually be run.
//
// Only prod has invariants. Dev's guarantees are the ones in Default() and the
// non-negotiable host-boundary rules that hold in every profile — none of which
// is expressible as "this config field must have this value".
func ValidateProfile(name string, cfg Config) error {
	if name != ProfileProd {
		return nil
	}
	var bad []string
	if cfg.Network.Mode != "allowlist" {
		bad = append(bad, fmt.Sprintf("network.mode is %q, must be \"allowlist\"", cfg.Network.Mode))
	}
	if cfg.Network.Baseline == nil || *cfg.Network.Baseline {
		bad = append(bad, "network.baseline must be false (the baseline contains github.com, a write endpoint)")
	}
	if cfg.PersistAuth != nil && *cfg.PersistAuth {
		bad = append(bad, "persist_auth must be false (no long-lived credential in the container)")
	}
	if cfg.Sync != nil && *cfg.Sync {
		bad = append(bad, "sync must be false (no host history mounted)")
	}
	if cfg.Security.Seccomp != SeccompRequired {
		bad = append(bad, "security.seccomp must be \"required\"")
	}
	if isRootUserName(cfg.User) {
		bad = append(bad, "user must not be root")
	}
	if len(cfg.Security.CapAdd) > 0 {
		bad = append(bad, fmt.Sprintf("security.cap_add must be empty, has %v", cfg.Security.CapAdd))
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("the resolved configuration does not satisfy the %q profile:\n  - %s",
		name, strings.Join(bad, "\n  - "))
}

// isRootUserName matches the spellings docker accepts for uid 0, the same set
// sandbox.isRootUser refuses alongside an allowlist.
func isRootUserName(u string) bool {
	switch strings.TrimSpace(u) {
	case "root", "0", "0:0", "root:root":
		return true
	}
	return false
}
