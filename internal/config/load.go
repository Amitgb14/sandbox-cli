package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// projectFileName is the per-project config file discovered by walking up from cwd.
const projectFileName = ".sandbox.yaml"

// Load discovers and merges configuration in precedence order (lowest to highest):
//
//	built-in defaults < user config (~/.config/sandbox/config.yaml) < nearest
//	.sandbox.yaml (walking up from startDir) < the explicit file at explicitPath.
//
// Host paths in mounts are resolved to absolute paths relative to the file that
// declared them. Flag overrides are applied by the caller after Load.
func Load(startDir, explicitPath string) (Config, error) {
	return LoadProfile(startDir, explicitPath, "")
}

// Overrides are the values a CLI flag imposes on the resolved configuration,
// applied after every file layer and before the profile is validated.
//
// They exist because the profile is validated *here* — against the configuration
// that will actually run — and a flag applied afterwards therefore arrives too
// late to be part of it. `--network allowlist` is documented as outranking the
// profile's default, and could not: LoadProfile had already refused a prod run
// whose config said `mode: default`, so the escape hatch never opened.
//
// A struct rather than another string parameter, so the next flag that outranks
// a file does not change this signature again — and so the one place that
// decides precedence stays one place.
type Overrides struct {
	// NetworkMode is "" for "no flag given", else an already-validated mode. The
	// caller checks the spelling, because an unknown value is a flag error and
	// should not surface as a profile complaint about a mode nobody typed.
	NetworkMode string

	// Allow is --allow. It belongs here for the same reason NetworkMode does:
	// BuildSpec turns the allowlist on when *either* the mode says so or domains
	// were named (`allowlist := cfg.Network.Mode == "allowlist" || len(opts.Allow) > 0`),
	// so a prod run asking for one with --allow satisfies the profile in fact
	// while a validation that only reads the mode refuses it. Fixing the mode
	// flag and not this one would have left the identical unreachable escape
	// hatch for the sibling flag root.go's own comment names beside it.
	Allow []string
}

// LoadProfile is Load with an explicit --profile override.
//
// It resolves in two passes because the profile has to be the *base* layer — the
// thing the other layers are merged on top of — while the name selecting it may
// itself come from one of those layers. So the first pass reads only the profile
// keys, and the second builds the real configuration on the base that names.
//
// Applying the profile underneath the user's own config, rather than over it,
// is deliberate: their config is trusted and a profile that could not be adjusted
// would be abandoned rather than used. What stops that from hollowing prod out is
// ValidateProfile, which checks the settings that define prod against the
// configuration that will actually run.
func LoadProfile(startDir, explicitPath, flagProfile string) (Config, error) {
	return LoadProfileWith(startDir, explicitPath, flagProfile, Overrides{})
}

// LoadProfileWith is LoadProfile with the CLI's own overrides folded in before
// the profile is checked, so one validation sees the run as it will be.
func LoadProfileWith(startDir, explicitPath, flagProfile string, ov Overrides) (Config, error) {
	name, err := discoverProfile(startDir, explicitPath, flagProfile)
	if err != nil {
		return Config{}, err
	}
	cfg := profileBase(name)
	cfg.Profile = name

	if p := userConfigPath(); p != "" {
		if err := mergeFile(&cfg, p); err != nil {
			return cfg, err
		}
	}

	if explicitPath != "" {
		// An explicitly named file is trusted: typing the path is the deliberate
		// act that a discovered .sandbox.yaml never involves. This is the supported
		// way to use a checked-in config you have actually read.
		//
		// Which is also why it has to be there: see ErrConfigNotFound.
		if _, err := os.Stat(explicitPath); err != nil {
			if os.IsNotExist(err) {
				return cfg, &ErrConfigNotFound{Path: explicitPath}
			}
			return cfg, fmt.Errorf("reading config %s: %w", explicitPath, err)
		}
		if err := mergeFile(&cfg, explicitPath); err != nil {
			return cfg, err
		}
	} else if p := findProjectConfig(startDir); p != "" {
		if err := mergeProjectFile(&cfg, p); err != nil {
			return cfg, err
		}
	}

	// The profile is not something a later layer may change out from under the
	// caller: it was resolved above, from the layers entitled to choose it.
	cfg.Profile = name

	// Flags last, because they outrank every file — and *before* the validation
	// below, because validating a configuration the run will not use is how the
	// documented `--network` escape hatch came to be unreachable.
	// --allow first, then --network: naming domains asks for an allowlist, and an
	// explicit mode is the more specific statement, so it wins over the implied one.
	if len(ov.Allow) > 0 && cfg.Network.Mode != "none" {
		cfg.Network.Mode = "allowlist"
	}
	if ov.NetworkMode != "" {
		cfg.Network.Mode = ov.NetworkMode
	}

	if err := ValidateProfile(name, cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// discoverProfile reads just the profile key out of the layers that may select
// one, so the answer is known before anything is merged.
//
// Errors from reading a file are swallowed here: the real merge below reads the
// same files and reports properly. This pass only answers "which base".
func discoverProfile(startDir, explicitPath, flagProfile string) (string, error) {
	var user, project string
	if p := userConfigPath(); p != "" {
		if f, err := readConfigFile(p); err == nil && f != nil {
			user = f.Profile
		}
	}
	if explicitPath != "" {
		if f, err := readConfigFile(explicitPath); err == nil && f != nil && f.Profile != "" {
			// An explicitly named file is trusted, so it selects like the user's own.
			user = f.Profile
		}
	} else if p := findProjectConfig(startDir); p != "" {
		if f, err := readConfigFile(p); err == nil && f != nil {
			project = f.Profile
		}
	}
	return ResolveProfile(flagProfile, user, project)
}

// ErrConfigNotFound is returned when --config names a file that is not there.
//
// A missing config is ordinary at every other layer — a user may have none, and
// a project may not carry one — but an explicit path is a string somebody typed,
// so ignoring it silently is the worst of both: discovery is skipped *because*
// the flag was given, the profile's defaults are all that remain, and nothing on
// screen says the file was never read. The report was `--config .sandbox.yml`
// against a file named `.sandbox.yaml`, which ran under the dev profile's egress
// allowlist that the file being pointed at had turned off.
type ErrConfigNotFound struct{ Path string }

func (e *ErrConfigNotFound) Error() string {
	msg := fmt.Sprintf("config file not found: %s", e.Path)
	if alt := siblingConfigPath(e.Path); alt != "" {
		msg += fmt.Sprintf("\n  Did you mean %s?", alt)
	}
	return msg + "\n" +
		"  --config names a file explicitly, so a missing one is a typo rather than a\n" +
		"  default: continuing would run with none of its settings and say nothing."
}

// siblingConfigPath returns the same path under the other YAML extension when
// that file does exist, so a .yml/.yaml slip answers itself. Returns "" when
// there is nothing to suggest — a guess that does not exist is noise.
func siblingConfigPath(path string) string {
	var alt string
	switch filepath.Ext(path) {
	case ".yml":
		alt = strings.TrimSuffix(path, ".yml") + ".yaml"
	case ".yaml":
		alt = strings.TrimSuffix(path, ".yaml") + ".yml"
	default:
		return ""
	}
	if fi, err := os.Stat(alt); err != nil || fi.IsDir() {
		return ""
	}
	return alt
}

// mergeProjectFile is mergeFile for a *discovered* .sandbox.yaml — the one config
// layer that arrives inside the repository and is therefore untrusted. It refuses
// the privilege-relevant keys instead of honoring them; see trust.go for what is
// on that list and why.
func mergeProjectFile(dst *Config, path string) error {
	f, err := readConfigFile(path)
	if err != nil || f == nil {
		return err
	}
	if bad := restrictedProjectKeys(*f, *dst); len(bad) > 0 {
		return &ErrRestrictedProjectKeys{Path: path, Keys: bad}
	}
	mergeInto(dst, *f, filepath.Dir(path))
	return nil
}

// configRoot is the sandbox-cli config/state directory: $XDG_CONFIG_HOME/sandbox or
// ~/.config/sandbox. Returns "" if the home directory cannot be determined.
func configRoot() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "sandbox")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "sandbox")
}

func userConfigPath() string {
	r := configRoot()
	if r == "" {
		return ""
	}
	return filepath.Join(r, "config.yaml")
}

// ConfigRoot exposes the sandbox config/state directory for callers that need to
// place auxiliary files (e.g. generated managed-settings). Returns "" if the
// home directory cannot be determined.
func ConfigRoot() string { return configRoot() }

// AgentStateDir returns the dedicated host directory that persists a named
// agent's state (credentials, sessions) across ephemeral containers, e.g.
// ~/.config/sandbox/agents/claude. It is sandbox-owned and never the host's real
// agent config. Returns "" if the home directory cannot be determined.
func AgentStateDir(name string) string {
	r := configRoot()
	if r == "" {
		return ""
	}
	return filepath.Join(r, "agents", name)
}

// SharedDir returns the single sandbox-owned host directory that --share mounts
// into the container, e.g. ~/.config/sandbox/shared. Unlike every other mount it
// is deliberately *not* derived from the project: one well-known path is what
// lets agents in different projects — or different worktrees of one project —
// hand a file to each other without a git remote or a hand-written --mount.
// Returns "" if the home directory cannot be determined.
func SharedDir() string {
	r := configRoot()
	if r == "" {
		return ""
	}
	return filepath.Join(r, "shared")
}

// RescueDir returns the sandbox-owned host directory holding crash-recovery
// state — the per-session manifests and private snapshot index files, e.g.
// ~/.config/sandbox/rescue. It is deliberately outside every repository: after a
// crash the repository itself may be the thing that is broken, and the answer to
// "which branch was that work on?" has to survive that. Returns "" if the home
// directory cannot be determined.
func RescueDir() string {
	r := configRoot()
	if r == "" {
		return ""
	}
	return filepath.Join(r, "rescue")
}

// AuditDir returns the sandbox-owned host directory holding the run log, e.g.
// ~/.config/sandbox/audit. Outside every repository like the rescue and contexts
// state, and for the same reason: a record of what a run did has to survive the
// project it ran in. Returns "" if the home directory cannot be determined.
func AuditDir() string {
	r := configRoot()
	if r == "" {
		return ""
	}
	return filepath.Join(r, "audit")
}

// ContextsDir returns the sandbox-owned host directory holding conversation
// context state — the per-context manifests and the verified agent session-store
// registry, e.g. ~/.config/sandbox/contexts. Like RescueDir it sits outside every
// repository, because a context outlives the checkout it started in and has to be
// answerable ("which sessions do I have for this repo?") from anywhere. Returns
// "" if the home directory cannot be determined.
func ContextsDir() string {
	r := configRoot()
	if r == "" {
		return ""
	}
	return filepath.Join(r, "contexts")
}

// StudioDir returns the sandbox-owned host directory holding Studio's own state
// — today the registry of repositories the control plane has been pointed at,
// e.g. ~/.config/sandbox/studio. Outside every repository like the rescue,
// audit and contexts state, and for the sharper version of the same reason: the
// list of repositories Studio manages cannot live in any one of them. Returns ""
// if the home directory cannot be determined.
func StudioDir() string {
	r := configRoot()
	if r == "" {
		return ""
	}
	return filepath.Join(r, "studio")
}

// findProjectConfig walks up from dir looking for .sandbox.yaml, stopping at a
// boundary rather than at the filesystem root. Returns "" if none is found.
//
// The boundary matters. Walking to `/` meant a .sandbox.yaml in *any* ancestor
// won, silently — and on Linux that makes a world-writable shared directory a
// config-injection point for everyone working beneath it: drop one file in /tmp
// and every sandbox started from /tmp/<anything> picks it up. Combined with what
// a config could then do, that was a local privilege-escalation vector rather
// than a convenience wart.
//
// So the search stops at the nearest enclosing repository root, because that is
// the unit a project config describes and the unit a user thinks they are in. If
// there is no repository, it stops at the home directory — still the user's own
// territory. Outside both (a scratch dir under /tmp, say) only the starting
// directory is consulted, because nothing above it is meaningfully "the project".
func findProjectConfig(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	stopAt := projectSearchBoundary(d)
	for {
		candidate := filepath.Join(d, projectFileName)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate
		}
		if d == stopAt {
			return ""
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "" // reached the filesystem root
		}
		d = parent
	}
}

// projectSearchBoundary returns the highest directory findProjectConfig may
// consult, given an absolute starting directory: the nearest enclosing repository
// root, else the home directory when dir is inside it, else dir itself.
func projectSearchBoundary(dir string) string {
	if root := repoRootOf(dir); root != "" {
		return root
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if abs, err := filepath.Abs(home); err == nil && withinDir(abs, dir) {
			return abs
		}
	}
	return dir
}

// repoRootOf returns the nearest ancestor of dir (inclusive) holding a .git
// entry, or "" when there is none. A worktree's .git is a file rather than a
// directory, so both count.
func repoRootOf(dir string) string {
	d := dir
	for {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// withinDir reports whether path is root or lives beneath it. Both must be
// absolute and cleaned by the caller.
func withinDir(root, path string) bool {
	r := filepath.Clean(root)
	p := filepath.Clean(path)
	if r == p {
		return true
	}
	return strings.HasPrefix(p, r+string(filepath.Separator))
}

// mergeFile reads a YAML config file and merges its set fields over dst. Missing
// files are ignored (a user may have none); malformed files are an error.
func mergeFile(dst *Config, path string) error {
	f, err := readConfigFile(path)
	if err != nil || f == nil {
		return err
	}
	mergeInto(dst, *f, filepath.Dir(path))
	return nil
}

// readConfigFile parses one config file. It returns (nil, nil) for a file that is
// not there, which is not an error at any layer — a user may have no config, and
// findProjectConfig can race with a file being removed.
func readConfigFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var f Config
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	return &f, nil
}

// mergeInto overlays the non-zero fields of src onto dst. Mount host paths are
// resolved relative to baseDir (the config file's directory).
func mergeInto(dst *Config, src Config, baseDir string) {
	if src.Image != "" {
		dst.Image = src.Image
	}
	if src.Workdir != "" {
		dst.Workdir = src.Workdir
	}
	if src.User != "" {
		dst.User = src.User
	}
	if src.Home != "" {
		dst.Home = src.Home
	}
	if src.Hostname != "" {
		dst.Hostname = src.Hostname
	}
	if src.Runtime != "" {
		dst.Runtime = src.Runtime
	}
	if src.Engine != "" {
		dst.Engine = src.Engine
	}
	if src.PersistAuth != nil {
		dst.PersistAuth = src.PersistAuth
	}
	if src.Sync != nil {
		dst.Sync = src.Sync
	}
	if src.Network.Mode != "" {
		dst.Network.Mode = src.Network.Mode
	}
	// Allow replaces (not appends) so a project config can fully redefine the
	// egress allowlist rather than only add to a broader inherited one.
	if src.Network.Allow != nil {
		dst.Network.Allow = src.Network.Allow
	}
	// Tri-state, so a project can turn the baseline off *and* a nearer config can
	// turn it back on. Only an explicit value overrides the inherited one.
	if src.Network.Baseline != nil {
		dst.Network.Baseline = src.Network.Baseline
	}
	// Ports replace for the same reason, and one more: publishing opens the
	// boundary inward, so a project must be able to say "only these" — and to say
	// "none" with `ports: []` — without a user-level default leaking through.
	if src.Ports != nil {
		dst.Ports = src.Ports
	}
	mergeSecurity(&dst.Security, src.Security)
	if src.Cache.Enabled != nil {
		dst.Cache.Enabled = src.Cache.Enabled
	}
	// Paths replace (not append) so a project can fully redefine the extra cache
	// set; the built-in defaults are always added on top at resolution time.
	if src.Cache.Paths != nil {
		dst.Cache.Paths = src.Cache.Paths
	}
	if src.Snapshot.Enabled != nil {
		dst.Snapshot.Enabled = src.Snapshot.Enabled
	}
	if src.Snapshot.Interval != "" {
		dst.Snapshot.Interval = src.Snapshot.Interval
	}
	if src.Snapshot.Retention != "" {
		dst.Snapshot.Retention = src.Snapshot.Retention
	}
	// Secrets overlay per-key (like Env): a later layer can add or replace an
	// individual secret without wiping the inherited set.
	for k, v := range src.Secrets {
		if dst.Secrets == nil {
			dst.Secrets = map[string]SecretSpec{}
		}
		dst.Secrets[k] = v
	}
	for k, v := range src.Env {
		if dst.Env == nil {
			dst.Env = map[string]string{}
		}
		dst.Env[k] = v
	}
	dst.EnvAllow = append(dst.EnvAllow, src.EnvAllow...)
	for _, m := range src.Mounts {
		m.Host = resolveHostPath(m.Host, baseDir)
		dst.Mounts = append(dst.Mounts, m)
	}
}

// mergeSecurity overlays the set fields of src onto dst. Pointer/string/slice
// "unset" (nil / "" / nil) is left untouched so lower layers show through. A
// slice set to an explicit empty list (e.g. `cap_drop: []`) is non-nil and thus
// replaces, letting a config clear an inherited default. Slices replace rather
// than append (unlike env_allow) so a policy can be fully redefined, not only added to.
func mergeSecurity(dst *SecuritySpec, src SecuritySpec) {
	if src.NoNewPrivileges != nil {
		dst.NoNewPrivileges = src.NoNewPrivileges
	}
	if src.CapDrop != nil {
		dst.CapDrop = src.CapDrop
	}
	if src.CapAdd != nil {
		dst.CapAdd = src.CapAdd
	}
	if src.PidsLimit != nil {
		dst.PidsLimit = src.PidsLimit
	}
	if src.Memory != "" {
		dst.Memory = src.Memory
	}
	if src.CPUs != "" {
		dst.CPUs = src.CPUs
	}
	if src.Seccomp != "" {
		dst.Seccomp = src.Seccomp
	}
}

// resolveHostPath expands ~ and makes relative paths absolute against baseDir.
func resolveHostPath(p, baseDir string) string {
	p = ExpandTilde(p)
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(baseDir, p))
}

// ExpandTilde replaces a leading ~ with the user's home directory.
func ExpandTilde(p string) string {
	if p == "~" || (len(p) >= 2 && p[0] == '~' && (p[1] == '/' || p[1] == filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// UserConfigPath exposes the resolved user config path for `sandbox-cli config path`.
func UserConfigPath() string { return userConfigPath() }

// FindProjectConfig exposes project config discovery for `sandbox-cli config path`.
func FindProjectConfig(dir string) string { return findProjectConfig(dir) }
