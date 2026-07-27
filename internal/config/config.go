// Package config defines the sandbox configuration schema and its layered
// discovery/merge rules: built-in defaults < user config < project config < flags.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/image"
)

// Config is the merged sandbox configuration.
type Config struct {
	Image    string            `yaml:"image"`
	Workdir  string            `yaml:"workdir"`
	User     string            `yaml:"user"`
	Home     string            `yaml:"home"`
	Hostname string            `yaml:"hostname"`
	Mounts   []MountSpec       `yaml:"mounts"`
	Env      map[string]string `yaml:"env"`
	EnvAllow []string          `yaml:"env_allow"`
	Network  NetworkSpec       `yaml:"network"`
	// Ports are published to the host (docker -p), e.g. ["3000:3000"]. A spec
	// with no address of its own binds to 127.0.0.1 (see sandbox.NormalizePublish)
	// — write 0.0.0.0:3000:3000 to expose it to the network deliberately. Empty
	// (the default) publishes nothing. Declaring a project's dev-server ports here
	// is the point: `sandbox-cli run -- npm run dev` then just works.
	Ports    []string              `yaml:"ports"`
	Security SecuritySpec          `yaml:"security"`
	Cache    CacheSpec             `yaml:"cache"`
	Snapshot SnapshotSpec          `yaml:"snapshot"`
	Secrets  map[string]SecretSpec `yaml:"secrets"`
	// Runtime is the OCI runtime (docker --runtime); "" uses docker's default
	// (runc). Set to a stronger-isolation runtime the host has registered, e.g.
	// "kata-runtime" (microVM) or "runsc" (gVisor).
	Runtime string `yaml:"runtime"`

	// Engine is the container engine: "docker" (default) or "podman".
	//
	// User-config only, like `runtime`: it chooses which binary sandbox-cli
	// executes, so a repository that could set it would choose what runs on your
	// machine.
	Engine string `yaml:"engine"`

	// Profile selects the security profile: "dev" (interactive, warns) or "prod"
	// (unattended, refuses). See profile.go. A project config may raise this and
	// never lower it, which is what stops a hostile repository dropping a run out
	// of prod.
	Profile string `yaml:"profile"`

	// PersistAuth keeps the agent login across runs by mounting a sandbox-owned
	// host directory as the agent's HOME. Tri-state: nil means the default (on
	// for agent wrappers), so no existing config changes behaviour.
	//
	// It is here rather than only on the command line because prod needs to turn
	// it off, and for a reason worth stating: that directory holds a long-lived
	// OAuth refresh token, readable by the agent, and an unattended run has no
	// business carrying one.
	PersistAuth *bool `yaml:"persist_auth"`

	// Sync mounts the host's agent history for this project so sessions resolve
	// on both sides. Tri-state, same reasoning: it is the one default that
	// reaches a host path outside the workspace.
	Sync *bool `yaml:"sync"`
}

// SecretSpec is a brokered credential: a reference to a value resolved at run
// time and forwarded into the container by name, so the raw value never lands on
// the docker command line, in --dry-run, in this config, or in shell history.
// Exactly one source field must be set (enforced by Validate).
type SecretSpec struct {
	File    string `yaml:"file"`    // read the value from this host file
	Command string `yaml:"command"` // run this host command; its stdout is the value
	Env     string `yaml:"env"`     // read the value from this host env var
}

// SecuritySpec is the container-hardening policy. The pointer fields are
// tri-state: nil means "not set, use the built-in default" so a project or user
// config can override a default-on setting to false (which a plain bool cannot
// express under the non-zero-wins merge). Defaults are secure-by-default (see
// Default): no-new-privileges on, all capabilities dropped, a pids cap to blunt
// fork bombs. Resource limits (Memory, CPUs) are opt-in — empty means unlimited,
// preserving the historical behavior — because an unexpected OOM-kill is worse
// than an unbounded-but-observed container.
type SecuritySpec struct {
	NoNewPrivileges *bool    `yaml:"no_new_privileges"` // --security-opt no-new-privileges (default true)
	CapDrop         []string `yaml:"cap_drop"`          // --cap-drop each (default ["ALL"])
	CapAdd          []string `yaml:"cap_add"`           // --cap-add each (default none)
	PidsLimit       *int64   `yaml:"pids_limit"`        // --pids-limit (default 1024; <=0 disables)
	Memory          string   `yaml:"memory"`            // --memory, e.g. "2g" (default "" = unlimited)
	CPUs            string   `yaml:"cpus"`              // --cpus, e.g. "1.5" (default "" = unlimited)
	Seccomp         string   `yaml:"seccomp"`           // --security-opt seccomp=… ("" = docker default profile)
}

// NoNewPriv reports whether no-new-privileges should be enabled, defaulting to
// true when unset.
func (s SecuritySpec) NoNewPriv() bool { return s.NoNewPrivileges == nil || *s.NoNewPrivileges }

// Pids returns the resolved pids limit, or 0 (no limit) when unset.
func (s SecuritySpec) Pids() int64 {
	if s.PidsLimit == nil {
		return 0
	}
	return *s.PidsLimit
}

// MountSpec is a bind mount declared in config. Host paths may use ~ and may be
// relative (resolved against the config file's directory when loaded from a file).
type MountSpec struct {
	Host      string `yaml:"host"`
	Container string `yaml:"container"`
	Mode      string `yaml:"mode"` // "ro" | "rw"; empty defaults to "ro"
}

// NetworkSpec controls container networking.
//
//   - "default" — the docker bridge; unrestricted egress.
//   - "none"    — no network at all.
//   - "allowlist" — bridge networking with a default-deny egress firewall that
//     permits only the baseline domains (agent APIs + package registries, see
//     BaselineEgress) plus any listed in Allow. Enforced in-container at startup
//     (see the sandbox-firewall entrypoint), so it needs NET_ADMIN.
type NetworkSpec struct {
	Mode  string   `yaml:"mode"`  // "default" | "none" | "allowlist"
	Allow []string `yaml:"allow"` // extra domains permitted in allowlist mode

	// Baseline switches off the built-in domain set, making Allow the whole
	// allowlist. Tri-state like the security fields: nil means "keep the
	// default" (baseline on), so no existing config changes behavior.
	//
	// It exists because Allow could only ever *add*: a run that should reach an
	// internal registry and the model API and nothing else had no way to decline
	// github.com — which is a write endpoint, and so an exfiltration channel for
	// any token the agent is holding. Turning it off is deliberately awkward to
	// use (npm, pip and git all stop working unless listed), which is the right
	// trade for the case it serves.
	Baseline *bool `yaml:"baseline"`
}

// baselineEgress is the always-permitted domain set in allowlist mode: the agent
// APIs plus the common package registries and code hosts, so `npm install`,
// `pip install`, and `git` keep working out of the box without the user having
// to enumerate them. Kept deliberately small and auditable.
var baselineEgress = []string{
	"api.anthropic.com",
	"api.openai.com",
	"registry.npmjs.org",
	"pypi.org",
	"files.pythonhosted.org",
	"github.com",
	"codeload.github.com",
	"objects.githubusercontent.com",
	"raw.githubusercontent.com",
}

// BaselineEgress returns a fresh copy of the built-in allowlist domains.
func BaselineEgress() []string {
	return append([]string(nil), baselineEgress...)
}

// reservedEnvNames are the variables sandbox-cli uses to instruct code that runs
// **as root, before the agent starts** — the sandbox-firewall entrypoint. They
// are not preferences: SANDBOX_RUN_AS names the user privileges are dropped to,
// and SANDBOX_EGRESS_ALLOW is the allowlist the firewall programs.
//
// Supplying either from outside turns it into an off switch. Both were reachable:
// forwarding SANDBOX_RUN_AS by name with `root` in the host environment made the
// entrypoint skip its privilege drop and run the agent as root, and an empty
// SANDBOX_EGRESS_ALLOW made the entrypoint a transparent passthrough while
// sandbox-cli still reported it was enforcing an allowlist.
//
// This is an exact-name list rather than a `SANDBOX_*` prefix on purpose. The
// prefix is also used by knobs that are the user's to set — `--env
// SANDBOX_STATUSLINE_NO_USAGE=1` is documented in docs/AGENTS.md — and those are
// read by an unprivileged script in the guest phase, long after the drop. Banning
// the whole namespace would break a documented feature to fix an unrelated one.
//
// Anything added here must be a variable consumed before privileges are dropped.
var reservedEnvNames = map[string]bool{
	"SANDBOX_RUN_AS":        true,
	"SANDBOX_EGRESS_ALLOW":  true,
	"SANDBOX_INGRESS_PORTS": true, // which inbound ports the firewall leaves open
	"SANDBOX_PROXY_PORT":    true, // where the name-enforcing egress proxy listens

	// Interpreter- and loader-control variables. These are not sandbox-cli's, but
	// they decide what the container's root-phase startup *executes*, which puts
	// them in the same category.
	//
	// bash sources $BASH_ENV at the start of every non-interactive script — so
	// `--env BASH_ENV=/workspace/evil.sh` ran the workspace's file as root, with
	// NET_ADMIN, *before* sandbox-egress-setup had programmed anything, while the
	// guest afterwards still reported uid 1001. Pinning PATH in the scripts does
	// not help: this is read by the interpreter before the first line runs, so the
	// only place to stop it is here, before it is ever passed to docker.
	"BASH_ENV":        true,
	"ENV":             true, // the POSIX sh equivalent
	"LD_PRELOAD":      true,
	"LD_LIBRARY_PATH": true,
	"LD_AUDIT":        true,
	"SHELLOPTS":       true, // can turn on xtrace, which then evaluates PS4
	"BASHOPTS":        true,
	"PS4":             true,
	"IFS":             true,
	"GLOBIGNORE":      true,

	// Docker *client* variables. These never reach the container — they steer the
	// docker binary sandbox-cli runs, which is the one child that still receives
	// forwarded values (see runtime.childEnv). DOCKER_HOST points it at another
	// daemon entirely; DOCKER_CONFIG names a directory whose config.json can set
	// credential helpers; the TLS pair decides who it trusts. Reaching them takes
	// a deliberate `--secret DOCKER_HOST=...` — project configs cannot declare
	// secrets — so this is closing a narrow door, but the list is exactly the
	// place to close it.
	"DOCKER_HOST":       true,
	"DOCKER_CONFIG":     true,
	"DOCKER_CERT_PATH":  true,
	"DOCKER_TLS_VERIFY": true,
	"DOCKER_CONTEXT":    true,
}

const reservedEnvReason = "this variable decides what the container's root-phase startup does or " +
	"executes — which user it drops to, what egress it permits, which file its interpreter " +
	"sources before the first line runs, or which docker daemon the run is sent to — and cannot " +
	"be set or forwarded from outside"

// ValidEnvName reports whether name is usable as an environment variable name.
//
// It matters because sandbox-cli forwards values by emitting a bare `-e NAME`,
// which docker parses as KEY=VALUE when the string contains an "=". A secret
// named `LD_PRELOAD=/workspace/evil.so` therefore rendered as a real assignment
// rather than a forward. Nothing downstream should have to wonder about that, so
// the name is checked where it enters.
func ValidEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// IsReservedEnv reports whether name is one of sandbox-cli's own control
// variables and therefore may not be set or forwarded by a user or a config.
func IsReservedEnv(name string) bool {
	return reservedEnvNames[strings.TrimSpace(name)]
}

// ReservedEnvReason is the explanation shown when one of them is refused, shared
// so the config and flag paths say the same thing.
func ReservedEnvReason() string { return reservedEnvReason }

// BaselineEnabled reports whether the built-in domain set is part of the
// allowlist. It answers for the *whole* config, not just allowlist mode, because
// `--allow` can switch the allowlist on for a run whose config never named a
// mode — and `baseline: false` has to hold there too.
func (n NetworkSpec) BaselineEnabled() bool {
	return n.Baseline == nil || *n.Baseline
}

// EgressDomains returns the resolved allowlist for allowlist mode — the baseline
// domains unioned with any configured Allow — or nil when the mode is not
// "allowlist". The result is de-duplicated and stably ordered (baseline first).
//
// With `baseline: false` the result is Allow alone, and an empty Allow yields an
// empty list rather than an implicit fallback. Callers must not read that as
// "no allowlist requested": see the refusal in sandbox.BuildSpec, which is what
// keeps the empty case from silently running with no firewall.
func (n NetworkSpec) EgressDomains() []string {
	if n.Mode != "allowlist" {
		return nil
	}
	if !n.BaselineEnabled() {
		return DedupeDomains(n.Allow)
	}
	return DedupeDomains(append(BaselineEgress(), n.Allow...))
}

// DedupeDomains trims, drops empties, and removes duplicates while preserving
// first-seen order.
func DedupeDomains(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range in {
		d = strings.TrimSpace(d)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// CacheSpec controls persistent package-manager caches. When enabled, sandbox-cli
// mounts a docker-managed named volume at each cache directory so downloads
// (npm, pip, cargo, go modules, …) survive the ephemeral --rm container instead
// of being re-fetched every run. It is opt-in (Enabled nil/false) because it
// introduces persistent, cross-run state and disk usage. Volumes are shared
// across sandboxes by design — package caches are content-addressed, so reuse is
// safe and maximizes hits.
type CacheSpec struct {
	Enabled *bool    `yaml:"enabled"` // opt-in; nil/false => no cache volumes
	Paths   []string `yaml:"paths"`   // extra container cache dirs, added to the defaults
}

// IsEnabled reports whether cache volumes should be mounted.
func (c CacheSpec) IsEnabled() bool { return c.Enabled != nil && *c.Enabled }

// defaultCachePaths are the well-known cache directories persisted when caching
// is on. They live under the sandbox HOME and are pre-created (sandbox-owned) in
// the base image so a fresh named volume initializes with the right ownership.
var defaultCachePaths = []string{
	"/sandbox/home/.npm",            // npm
	"/sandbox/home/.cache/pip",      // pip
	"/sandbox/home/.cargo/registry", // cargo crates
	"/sandbox/home/go/pkg/mod",      // go modules
	"/sandbox/home/.cache/yarn",     // yarn
}

// DefaultCachePaths returns a fresh copy of the built-in cache directories.
func DefaultCachePaths() []string {
	return append([]string(nil), defaultCachePaths...)
}

// CachePaths returns the resolved set of container cache directories to persist —
// the defaults unioned with any configured Paths — de-duplicated, defaults first.
func (c CacheSpec) CachePaths() []string {
	return dedupePaths(append(DefaultCachePaths(), c.Paths...))
}

// CacheVolumeName derives a stable, docker-valid named-volume name for a cache
// directory. The name is a pure function of the path (independent of project) so
// the same cache is reused across every sandbox, e.g. "/sandbox/home/.npm" ->
// "sandbox-cache-npm".
func CacheVolumeName(containerPath string) string {
	p := containerPath
	for _, pre := range []string{"/sandbox/home/", "/root/", "/home/"} {
		if strings.HasPrefix(p, pre) {
			p = p[len(pre):]
			break
		}
	}
	var b strings.Builder
	b.WriteString("sandbox-cache-")
	prevDash := true // suppress a leading separator (e.g. the "." in ".npm")
	for _, r := range p {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// SnapshotSpec controls the crash safety net: while a sandbox runs, the
// workspace is periodically committed into the repository's own object store
// under refs/sandbox/snapshots/, so work survives a container, daemon, or
// sandbox-cli crash — and survives an agent that resets the branch out from
// under itself. It is on by default because the whole point is to be there
// when nobody thought to turn it on; a snapshot never touches the user's index,
// HEAD, branches, or working tree (see internal/rescue).
//
// Enabled is tri-state for the same reason as SecuritySpec's pointers: a
// project config must be able to override a default-on setting to false.
type SnapshotSpec struct {
	Enabled   *bool  `yaml:"enabled"`   // default true
	Interval  string `yaml:"interval"`  // Go duration, e.g. "2m" (default 2m; <=0 disables)
	Retention string `yaml:"retention"` // Go duration; snapshots older than this are pruned (default 336h = 14d)
}

// Snapshot defaults. Two minutes bounds the loss window on a hard kill without
// making the safety net noticeable; fourteen days is long enough that "the
// crash was last week" is still recoverable, short enough that abandoned
// snapshot refs stop pinning objects forever.
const (
	DefaultSnapshotInterval  = 2 * time.Minute
	DefaultSnapshotRetention = 14 * 24 * time.Hour
)

// IsEnabled reports whether snapshotting should run, defaulting to true when unset.
func (s SnapshotSpec) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// EveryDuration returns the resolved snapshot interval, falling back to the
// default when unset or unparseable (Validate rejects unparseable values, so a
// bad string only reaches here on a config that was never validated).
func (s SnapshotSpec) EveryDuration() time.Duration {
	return parseDurationOr(s.Interval, DefaultSnapshotInterval)
}

// RetentionDuration returns the resolved retention window.
func (s SnapshotSpec) RetentionDuration() time.Duration {
	return parseDurationOr(s.Retention, DefaultSnapshotRetention)
}

func parseDurationOr(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

func dedupePaths(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// Default returns the built-in base configuration.
// PersistAuthEnabled reports the effective value: on unless explicitly off.
func (c Config) PersistAuthEnabled() bool { return c.PersistAuth == nil || *c.PersistAuth }

// SyncEnabled reports the effective value: on unless explicitly off.
func (c Config) SyncEnabled() bool { return c.Sync == nil || *c.Sync }

func Default() Config {
	return Config{
		Image:   image.Ref(),
		Workdir: "/workspace",
		// Non-root by default: agents like Claude Code refuse
		// --dangerously-skip-permissions when running as root. On macOS Docker
		// Desktop bind-mount ownership is virtualized, so a non-root user still
		// writes /workspace files as the host user. Override with `--user root`.
		User:     "sandbox",
		Home:     "/sandbox/home",
		Hostname: "sandbox",
		Env:      map[string]string{},
		// allowlist, not "default". Dev's egress is bounded because dev is where
		// the developer's own long-lived credential is in reach: the persisted
		// agent HOME holds an OAuth refresh token the agent can read, and with
		// unbounded egress nothing stopped it being posted anywhere.
		//
		// The baseline stays on, so npm, pip and git keep working — that is what
		// the baseline is for, and a control that breaks a normal workflow gets
		// switched off rather than obeyed. Be clear about what this does and does
		// not buy: the baseline contains github.com, a write endpoint, so this
		// converts silent exfiltration to any host into exfiltration through a
		// small set of named, logged, auditable hosts. A real reduction in reach
		// and a large gain in attributability; not containment of a capable
		// attacker. Only `baseline: false` with an explicit allow is that, which
		// is why it is prod's setting and not dev's.
		//
		// The recorded objection was that allowlist mode puts every run through
		// the root entrypoint with NET_ADMIN. That was written before the root
		// phase was hardened — BASH_ENV and friends reserved, PATH pinned, the
		// agent HOME off the image PATH — and it costs ~166ms of startup. Still a
		// privileged phase, no longer the larger risk.
		Network: NetworkSpec{Mode: "allowlist"},
		// Secure-by-default hardening. Dropping all capabilities and forbidding
		// privilege escalation is essentially free for the non-root `sandbox`
		// user and closes the obvious escape routes; the pids cap blunts fork
		// bombs while staying well above real build/agent process counts. Memory
		// and CPU stay unlimited (opt-in) to avoid surprising OOM-kills.
		Security: SecuritySpec{
			NoNewPrivileges: boolPtr(true),
			CapDrop:         []string{"ALL"},
			PidsLimit:       int64Ptr(1024),
		},
	}
}

func boolPtr(b bool) *bool    { return &b }
func int64Ptr(n int64) *int64 { return &n }

// Validate checks that the merged config is internally consistent.
func (c Config) Validate() error {
	if c.Image == "" {
		return fmt.Errorf("image must not be empty")
	}
	// A typo here would otherwise be executed: NewEngine treats an unknown name
	// as the binary to run, so `engine: dokcer` becomes a confusing "executable
	// file not found" from deep inside a run rather than a config error.
	if c.Engine != "" && c.Engine != "docker" && c.Engine != "podman" {
		return fmt.Errorf("engine %q: want docker or podman", c.Engine)
	}
	// An image reference beginning with a dash is read by docker as another flag,
	// not as the image: `image: "--privileged"` rendered a real --privileged into
	// the argv and promoted the guest's first argument to the image name. BuildArgs
	// now also emits `--` before the image, so this is belt and braces — but the
	// config is where the mistake is legible, so it is reported here too.
	if strings.HasPrefix(c.Image, "-") {
		return fmt.Errorf("image %q must not begin with %q: docker would read it as a flag rather than an image reference", c.Image, "-")
	}
	if strings.ContainsAny(c.Image, " \t\n") {
		return fmt.Errorf("image %q must not contain whitespace", c.Image)
	}
	for k := range c.Env {
		if IsReservedEnv(k) {
			return fmt.Errorf("env %q: %s", k, reservedEnvReason)
		}
		if !ValidEnvName(k) {
			return fmt.Errorf("env %q is not a valid environment variable name", k)
		}
	}
	for k := range c.Secrets {
		if !ValidEnvName(k) {
			return fmt.Errorf("secret %q is not a valid environment variable name "+
				"(it is forwarded as `-e %s`, so anything else is read by docker as something other than a name)", k, k)
		}
		if IsReservedEnv(k) {
			return fmt.Errorf("secret %q: %s", k, reservedEnvReason)
		}
	}
	if c.Workdir == "" {
		return fmt.Errorf("workdir must not be empty")
	}
	switch c.Network.Mode {
	case "", "default", "none", "allowlist":
	default:
		return fmt.Errorf("network.mode must be \"default\", \"none\", or \"allowlist\", got %q", c.Network.Mode)
	}
	for i, m := range c.Mounts {
		if m.Host == "" || m.Container == "" {
			return fmt.Errorf("mounts[%d]: host and container are required", i)
		}
		switch m.Mode {
		case "", "ro", "rw":
		default:
			return fmt.Errorf("mounts[%d]: mode must be \"ro\" or \"rw\", got %q", i, m.Mode)
		}
	}
	for _, d := range []struct{ field, value string }{
		{"snapshot.interval", c.Snapshot.Interval},
		{"snapshot.retention", c.Snapshot.Retention},
	} {
		if d.value == "" {
			continue
		}
		if _, err := time.ParseDuration(d.value); err != nil {
			return fmt.Errorf("%s must be a duration like \"2m\" or \"336h\", got %q", d.field, d.value)
		}
	}
	for name, s := range c.Secrets {
		if name == "" {
			return fmt.Errorf("secrets: a secret name must not be empty")
		}
		n := 0
		for _, set := range []bool{s.File != "", s.Command != "", s.Env != ""} {
			if set {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("secrets[%q]: set exactly one of file, command, or env (got %d)", name, n)
		}
	}
	return nil
}

// NetworkArg maps the config network mode to a docker --network value, or "" for
// the default bridge (no flag emitted).
func (c Config) NetworkArg() string {
	if c.Network.Mode == "none" {
		return "none"
	}
	return ""
}
