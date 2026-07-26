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
// What is still permitted from a project file is the set that describes the
// project rather than the boundary: `hostname`, `ports`, `snapshot`,
// `cache.enabled`, and the egress `network.allow` / `network.baseline` (see
// restrictedProjectKeys for why those two are treated as narrowing).

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

	// Refused only when they weaken what is already in force. A project saying
	// "this code needs stricter confinement than your default" is exactly what a
	// project file is for; the same key saying "actually, less" is the attack.
	if src.Network.Mode != "" && networkStrength(src.Network.Mode) < networkStrength(inherited.Network.Mode) {
		add("network.mode")
	}
	// baseline is the same shape one level down: turning the built-in egress
	// domains off narrows, turning them back on widens.
	if src.Network.Baseline != nil && *src.Network.Baseline && !inherited.Network.BaselineEnabled() {
		add("network.baseline")
	}

	sort.Strings(bad)
	return bad
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
