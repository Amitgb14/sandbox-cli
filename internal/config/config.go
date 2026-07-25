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

// EgressDomains returns the resolved allowlist for allowlist mode — the baseline
// domains unioned with any configured Allow — or nil when the mode is not
// "allowlist". The result is de-duplicated and stably ordered (baseline first).
func (n NetworkSpec) EgressDomains() []string {
	if n.Mode != "allowlist" {
		return nil
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
		Network:  NetworkSpec{Mode: "default"},
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
