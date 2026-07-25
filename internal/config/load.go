package config

import (
	"fmt"
	"os"
	"path/filepath"

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
	cfg := Default()

	if p := userConfigPath(); p != "" {
		if err := mergeFile(&cfg, p); err != nil {
			return cfg, err
		}
	}

	if explicitPath != "" {
		if err := mergeFile(&cfg, explicitPath); err != nil {
			return cfg, err
		}
	} else if p := findProjectConfig(startDir); p != "" {
		if err := mergeFile(&cfg, p); err != nil {
			return cfg, err
		}
	}

	return cfg, nil
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

// findProjectConfig walks up from dir looking for .sandbox.yaml, stopping at the
// filesystem root. Returns "" if none is found.
func findProjectConfig(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(d, projectFileName)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "" // reached root
		}
		d = parent
	}
}

// mergeFile reads a YAML config file and merges its set fields over dst. Missing
// files are ignored (a user may have none); malformed files are an error.
func mergeFile(dst *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading config %s: %w", path, err)
	}
	var f Config
	if err := yaml.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parsing config %s: %w", path, err)
	}
	baseDir := filepath.Dir(path)
	mergeInto(dst, f, baseDir)
	return nil
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
	if src.Network.Mode != "" {
		dst.Network.Mode = src.Network.Mode
	}
	// Allow replaces (not appends) so a project config can fully redefine the
	// egress allowlist rather than only add to a broader inherited one.
	if src.Network.Allow != nil {
		dst.Network.Allow = src.Network.Allow
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
