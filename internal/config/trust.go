package config

import (
	"fmt"
	"sort"
	"strings"
)

// The trust boundary for project-level config.
//
// A `.sandbox.yaml` discovered by walking up from the working directory is
// **untrusted input**. It arrives inside repositories people clone, and the agent
// running in the sandbox has read-write access to the very file that configures
// its next run. Until this file existed, every key in it outranked the user's own
// ~/.config/sandbox/config.yaml — which made a checked-in config a complete
// bypass of the product:
//
//   - `secrets: {X: {command: ...}}` ran arbitrary commands on the **host**, via
//     `sh -c`, before any container started;
//   - `mounts: [{host: /, mode: rw}]` plus `user: root` handed over the host
//     filesystem;
//   - `env: {PATH: /workspace/.bin:...}` redirected the root entrypoint's own
//     interpreter to a file in the repository;
//   - `network: {mode: default}` silently switched off an egress allowlist the
//     user had configured globally.
//
// So the privilege-relevant keys are refused from a project file rather than
// honored. The refusal names the offending keys and where they may live instead;
// it is not a warning, because the scenario this exists for is an unattended
// agent whose operator is not reading the terminal.
//
// Two escape hatches remain, and both require the user to act deliberately:
// `~/.config/sandbox/config.yaml` (a file only the user writes) and the CLI
// flags. An explicit `--config <path>` is also trusted — typing the path *is* the
// deliberate act — which is the supported way to use a checked-in config you have
// actually read.
//
// What is still permitted from a project file is the small set that describes the
// project without touching the boundary: `hostname`, `cache.enabled`, and a
// `network.mode` / `network.baseline` that *tightens* what is already in force.
// Everything else — including `network.allow`, `ports` and `snapshot`, all of
// which were permitted at first — turned out to be a decision about the boundary
// rather than about the project.

// restrictedProjectKeys returns the yaml paths of privilege-relevant keys that
// src sets and a project-level file may not. inherited is the already-merged
// configuration the project file would be layered onto, needed for the keys whose
// legitimacy depends on direction of travel rather than on being set at all.
//
// The result is sorted, so the error message is stable and testable.
func restrictedProjectKeys(src, inherited Config) []string {
	var bad []string
	add := func(k string) { bad = append(bad, k) }

	// Unconditionally refused: each one either chooses what executes, reaches the
	// host, or relaxes the container's confinement.
	if src.Image != "" {
		add("image") // chooses the image, and a dash-leading value injects docker flags
	}
	if src.Workdir != "" {
		add("workdir") // moves the workspace mount target
	}
	if src.User != "" {
		add("user") // `user: root` cancels the non-root default
	}
	if src.Home != "" {
		add("home") // relocates the container HOME, and with it the persisted-auth mount
	}
	if src.Engine != "" {
		add("engine") // chooses which binary sandbox-cli executes on your machine
	}
	if src.Runtime != "" {
		add("runtime") // selects the OCI runtime, i.e. the strength of the boundary
	}
	if src.Mounts != nil {
		add("mounts") // the whole host filesystem is one line away
	}
	if src.Secrets != nil {
		// All three sources, not just `command`. `file:` reads any host file the
		// user can read and hands it to the container, and it resolves relative to
		// the process cwd — which is normally the untrusted repository itself.
		add("secrets")
	}
	if src.Env != nil {
		// Refused wholesale rather than by denylist. Every language runtime in the
		// image ships at least one environment variable that turns into code
		// execution — PATH, LD_PRELOAD, NODE_OPTIONS, PYTHONSTARTUP, BASH_ENV — and
		// a denylist of those is a game that gets lost quietly. `--env` still works
		// for the one-off case, and the user's own config for the standing one.
		add("env")
	}
	if src.EnvAllow != nil {
		add("env_allow") // names any host variable to forward: AWS_SECRET_ACCESS_KEY, GITHUB_TOKEN
	}
	if src.Cache.Paths != nil {
		// A cache path becomes a writable named volume mounted at that container
		// path, so it can be aimed at somewhere meaningful (the persisted
		// credentials directory) to shadow it.
		add("cache.paths")
	}
	if !securityIsZero(src.Security) {
		add("security") // no_new_privileges, cap_add/cap_drop, seccomp, pids_limit
	}
	if src.Network.Allow != nil {
		// Permitting this was a mistake, and the reasoning for it was wrong: it was
		// treated as "narrowing", but `allow` only ever *widens* — and mergeInto
		// makes it REPLACE rather than append, so a checked-in file could rewrite an
		// allowlist the user had configured, wholesale. With `baseline: false` in the
		// user's config, `allow: [exfil.example.com]` in the repository became the
		// entire allowlist: the repository choosing where a compromised agent may
		// send data, which is the one thing the allowlist exists to decide.
		//
		// This costs a project file its most natural network knob. The escape hatch
		// is the same as for every other refused key: the user's own config, or
		// --allow on the command line.
		add("network.allow")
	}
	if src.Ports != nil {
		// Publishing binds a host port and, under an allowlist, punches a hole in the
		// default-deny INPUT chain (SANDBOX_INGRESS_PORTS). `0.0.0.0:8022:22` in a
		// checked-in file exposes the container on every interface, and a project can
		// also squat host ports. Declaring a dev-server port is a real use, but it is
		// a decision about the boundary, so it belongs to the user.
		add("ports")
	}
	if src.Snapshot.Enabled != nil || src.Snapshot.Interval != "" || src.Snapshot.Retention != "" {
		// `snapshot.enabled: false` silently removes crash protection, and
		// `interval: 1ms` turns the host into a sustained `git add -A` loop for the
		// length of the session (confirmed: 6 commits and 18 loose objects in five
		// seconds). maxSnapshotFileBytes was deliberately made a constant to keep it
		// out of a project file; the cadence deserved the same treatment.
		add("snapshot")
	}

	// Refused only when they weaken what is already in force. A project saying
	// "this code needs stricter confinement than your default" is exactly what a
	// project file is for; the same key saying "actually, less" is the attack.
	if src.Network.Mode != "" && networkStrength(src.Network.Mode) < networkStrength(inherited.Network.Mode) {
		// The common way to reach this is not an attack but an upgrade: egress is
		// now default-denied, so a `mode: default` that used to match the built-in
		// default — and that `sandbox-cli init` itself once scaffolded — became a
		// weakening overnight and fails every command in the directory. The key
		// name alone would not explain that, so this one carries its own note.
		//
		// It names the mode actually in force rather than telling the
		// default-denied story unconditionally: the same branch fires when a
		// user's own config chose `none` and the project asks for `default`, and
		// blaming a changed default there would be false.
		//
		// The remedies are the two that work. `--network` is deliberately not one
		// of them: this error is returned while the config is being loaded, before
		// any flag override is applied, so a user who followed that advice would
		// get the identical message and rightly trust it less.
		add("network.mode (\"" + src.Network.Mode + "\" is weaker than the \"" +
			inheritedNetworkMode(inherited) + "\" already in force; remove the key, " +
			"or load this file deliberately with --config)")
	}
	// baseline is the same shape one level down: turning the built-in egress
	// domains off narrows, turning them back on widens.
	if src.Network.Baseline != nil && *src.Network.Baseline && !inherited.Network.BaselineEnabled() {
		add("network.baseline")
	}
	// profile is the same shape at the top level. A repository that knows it
	// handles untrusted input may demand a stronger profile; the same key naming
	// a weaker one is the attack, and the worst one available — it would drop the
	// run out of prod and leave every other control in this file decorative.
	if src.Profile != "" && profileStrength[src.Profile] < profileStrength[inherited.Profile] {
		add("profile")
	}
	// persist_auth true mounts a sandbox-owned host directory as the agent's
	// whole HOME, and that directory holds a long-lived OAuth refresh token.
	// Turning it off narrows; turning it on hands the agent a credential.
	if src.PersistAuth != nil && *src.PersistAuth && !inherited.PersistAuthEnabled() {
		add("persist_auth")
	}
	// sync true mounts a host path outside the workspace. Same direction rule.
	if src.Sync != nil && *src.Sync && !inherited.SyncEnabled() {
		add("sync")
	}

	sort.Strings(bad)
	return bad
}

// inheritedNetworkMode spells the mode in force for a message, including the
// empty value that means "the built-in default".
func inheritedNetworkMode(inherited Config) string {
	if inherited.Network.Mode == "" {
		return "default"
	}
	return inherited.Network.Mode
}

// networkStrength ranks the network modes by how much they confine. Used only to
// decide whether a project file is tightening or loosening; the absolute numbers
// mean nothing outside that comparison.
func networkStrength(mode string) int {
	switch mode {
	case "none":
		return 2 // no network at all
	case "allowlist":
		return 1 // default-deny egress
	default:
		return 0 // "" / "default": unrestricted bridge
	}
}

// securityIsZero reports whether a SecuritySpec carries no opinion at all. Any
// field set means the file is trying to shape the container's confinement, which
// a project file may not do — including tightening it, because "cap_add: [X]"
// looks like tightening and is not.
func securityIsZero(s SecuritySpec) bool {
	return s.NoNewPrivileges == nil &&
		s.CapDrop == nil &&
		s.CapAdd == nil &&
		s.PidsLimit == nil &&
		s.Memory == "" &&
		s.CPUs == "" &&
		s.Seccomp == ""
}

// ErrRestrictedProjectKeys is returned when a project-level .sandbox.yaml sets
// keys reserved to the user's own configuration. It names them, because "your
// config is not allowed" without saying which part is unactionable.
type ErrRestrictedProjectKeys struct {
	Path string
	Keys []string
}

func (e *ErrRestrictedProjectKeys) Error() string {
	plural := "key"
	if len(e.Keys) > 1 {
		plural = "keys"
	}
	return fmt.Sprintf(
		"%s sets %s that a project config may not: %s\n"+
			"  A .sandbox.yaml travels with the repository, so it is treated as untrusted: these\n"+
			"  keys can run commands on your machine, mount host paths, or weaken the sandbox.\n"+
			"  Move them to your own config (%s), pass them as flags, or — if you have read this\n"+
			"  file and trust it — load it explicitly with --config %s",
		e.Path, plural, strings.Join(e.Keys, ", "), userConfigPathForMessage(), e.Path)
}

// userConfigPathForMessage is the user config path for error text, falling back
// to the conventional location when the home directory cannot be determined.
func userConfigPathForMessage() string {
	if p := userConfigPath(); p != "" {
		return p
	}
	return "~/.config/sandbox/config.yaml"
}
