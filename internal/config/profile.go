package config

import (
	"runtime"

	"fmt"
	runtimepkg "github.com/Amitgb14/sandbox-cli/internal/runtime"
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

		// Nothing published. Prod is likelier to be multi-tenant, and an inbound
		// port is the one thing that opens the boundary the other way.
		cfg.Ports = []string{}

		// Runtime is **demanded but not named**, and the difference is the whole
		// design. prod may carry untrusted agents, for which a container
		// namespace is not a boundary — but *which* stronger runtime a host has
		// is a property of the machine, and writing `runsc` here would refuse
		// every prod run on a host that has Kata, while writing `kata-runtime`
		// would refuse every host that has gVisor. A profile that guesses a name
		// fails on the machine it was supposed to protect.
		//
		// So the name stays the user's to choose and ValidateProfile refuses prod
		// without one, on the hosts where one can exist. See
		// hostCanRegisterStrongerRuntime for why that is not everywhere.
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
	// A typo in the one key that selects the security posture must not be
	// indistinguishable from not setting it. `profile: prodd` used to fail
	// KnownProfile, be discarded, and run as dev — persisted auth mounted, host
	// history synced, baseline on, seccomp merely warned about — while the
	// operator believed they had asked for prod.
	if user != "" {
		if !KnownProfile(user) {
			return "", fmt.Errorf("unknown profile %q in your config (known: %s)", user, strings.Join(ProfileNames(), ", "))
		}
		chosen = user
	}
	// The project branch stays silent on an unrecognised value: it can only ever
	// raise, so a name that matches nothing is already inert, and erroring would
	// let a repository fail a run by writing nonsense.
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
// hostCanRegisterStrongerRuntime reports whether this host can be given an OCI
// runtime that does not share the host kernel.
//
// Linux can: Kata and gVisor are installed and registered with the daemon
// there. Docker Desktop on macOS and Windows cannot — it runs every container
// inside its own managed Linux VM and does not allow registering a custom
// runtime — and podman's machine is the same shape. So prod demanding one there
// would be demanding something the platform cannot supply, which is not a
// boundary control but a refusal to run.
//
// That is not a hole: a Desktop container already sits inside a VM the host
// does not share. The boundary is real, it is simply not per-run and not
// selectable. `doctor` says so in as many words rather than staying quiet.
//
// A var so tests can pin the one input that differs per machine — the same
// reason hostTimezone and hostPrimaryGID are vars.
var hostCanRegisterStrongerRuntime = func() bool { return runtime.GOOS == "linux" }

// HostCanRegisterStrongerRuntime is the same question, for callers outside this
// package. `doctor` asks it so the preflight and the profile cannot disagree
// about which hosts are held to which rule — the preflight exists to tell you
// what a run would do before it does it.
func HostCanRegisterStrongerRuntime() bool { return hostCanRegisterStrongerRuntime() }

func ValidateProfile(name string, cfg Config) error {
	if name != ProfileProd {
		return nil
	}
	var bad []string
	// The one control whose absence is a property of the machine rather than of
	// the configuration, which is why it is asked this way round: not "is the
	// name valid" but "can this host give a run its own kernel, and did anyone
	// ask for one".
	if hostCanRegisterStrongerRuntime() && !runtimepkg.StrongerRuntime(cfg.Runtime) {
		detail := "runtime must name a runtime with a kernel of its own (e.g. kata-runtime, or runsc for gVisor)"
		if cfg.Runtime != "" {
			detail = fmt.Sprintf("runtime is %q, which shares the host kernel — prod needs one of its own (e.g. kata-runtime, or runsc for gVisor)", cfg.Runtime)
		}
		bad = append(bad, detail+"; `sandbox-cli doctor --profile prod` lists what this host has registered")
	}
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
	if IsRootUser(cfg.User) {
		bad = append(bad, "user must not be root")
	}
	if len(cfg.Security.CapAdd) > 0 {
		bad = append(bad, fmt.Sprintf("security.cap_add must be empty, has %v", cfg.Security.CapAdd))
	}
	// Resource bounds and published ports are in prod's stated guarantees, so
	// they are asserted rather than merely defaulted. A base layer a later config
	// can quietly raise is not a guarantee, and a table that promises one is
	// worse than a table that does not.
	if cfg.Security.Memory == "" {
		bad = append(bad, "security.memory must be set (a runaway agent must not take a shared host down)")
	}
	if cfg.Security.CPUs == "" {
		bad = append(bad, "security.cpus must be set")
	}
	if cfg.Security.PidsLimit == nil || *cfg.Security.PidsLimit <= 0 {
		bad = append(bad, "security.pids_limit must be a positive bound")
	}
	if len(cfg.Ports) > 0 {
		bad = append(bad, fmt.Sprintf("ports must be empty, has %v (publishing opens the boundary inward)", cfg.Ports))
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("the resolved configuration does not satisfy the %q profile:\n  - %s",
		name, strings.Join(bad, "\n  - "))
}

// IsRootUser reports whether a --user/config value asks to run as uid 0.
//
// One implementation, because there were three and they disagreed. docker
// accepts `user`, `uid`, `user:group` and `uid:gid`, so only the part before the
// colon decides — and a version that matched a fixed list of spellings missed
// `0:1000`, while the one that split on the colon missed `root:root`. The
// disagreement had teeth: the CLI would decline to yield the default allowlist
// for `--user 0:1000` and BuildSpec would then refuse the run, which is exactly
// the regression the yield exists to prevent.
func IsRootUser(user string) bool {
	u := strings.TrimSpace(user)
	if i := strings.IndexByte(u, ':'); i >= 0 {
		u = u[:i]
	}
	return u == "root" || u == "0"
}
