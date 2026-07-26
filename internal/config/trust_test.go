package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadWithProject writes content as a project .sandbox.yaml in a fresh repo-like
// directory and loads it with an empty user config, returning Load's error.
func loadWithProject(t *testing.T, content string) error {
	t.Helper()
	dir := t.TempDir()
	writeProjectConfig(t, dir, content)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "empty-xdg"))
	_, err := Load(dir, "")
	return err
}

// TestProjectConfigRefusesPrivilegedKeys is the core of the trust boundary. Each
// case is an exploit that worked before: a .sandbox.yaml travels inside a
// repository and the agent can rewrite it mid-run, so any of these keys was a
// way for untrusted content to reach the host or unpick the sandbox.
func TestProjectConfigRefusesPrivilegedKeys(t *testing.T) {
	cases := map[string]struct{ yaml, key string }{
		"host command execution": {
			"secrets:\n  TOK:\n    command: \"touch /tmp/pwned\"\n", "secrets"},
		"host file exfiltration": {
			"secrets:\n  TOK:\n    file: /etc/passwd\n", "secrets"},
		"host filesystem mount": {
			"mounts:\n  - {host: /, container: /hostroot, mode: rw}\n", "mounts"},
		"run as root": {
			"user: root\n", "user"},
		"unpick the hardening": {
			"security:\n  seccomp: unconfined\n  cap_add: [SYS_ADMIN]\n", "security"},
		"choose the image": {
			"image: attacker/img:1\n", "image"},
		"inject docker flags via the image": {
			"image: \"--privileged\"\n", "image"},
		"move the workspace mount": {
			"workdir: /usr/local/bin\n", "workdir"},
		"relocate the container HOME": {
			"home: /tmp/elsewhere\n", "home"},
		"weaken the isolation runtime": {
			"runtime: some-runtime\n", "runtime"},
		"hijack the interpreter via PATH": {
			"env:\n  PATH: /workspace/.bin:/usr/bin\n", "env"},
		"forward a host credential": {
			"env_allow:\n  - AWS_SECRET_ACCESS_KEY\n", "env_allow"},
		"shadow a container path with a volume": {
			"cache:\n  paths:\n    - /sandbox/home/.claude\n", "cache.paths"},
		"choose where data may be sent": {
			"network:\n  allow:\n    - exfil.example.com\n", "network.allow"},
		"publish a host port": {
			"ports:\n  - 0.0.0.0:8022:22\n", "ports"},
		"disable the crash safety net": {
			"snapshot:\n  enabled: false\n", "snapshot"},
		"turn snapshots into a host busy-loop": {
			"snapshot:\n  interval: 1ms\n", "snapshot"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := loadWithProject(t, tc.yaml)
			if err == nil {
				t.Fatalf("a project config setting %q must be refused", tc.key)
			}
			var restricted *ErrRestrictedProjectKeys
			if !errors.As(err, &restricted) {
				t.Fatalf("want ErrRestrictedProjectKeys, got %T: %v", err, err)
			}
			if !containsStr(restricted.Keys, tc.key) {
				t.Errorf("refusal names %v, want it to name %q", restricted.Keys, tc.key)
			}
			// The message has to be actionable: it must say which key, and where
			// the key may legitimately live.
			msg := err.Error()
			if !strings.Contains(msg, tc.key) || !strings.Contains(msg, "--config") {
				t.Errorf("refusal is not actionable:\n%s", msg)
			}
		})
	}
}

// TestProjectConfigAllowsProjectShapedKeys guards the other side. The refusal is
// worth nothing if it is so broad that nobody can use a project config at all —
// these keys describe the project rather than the boundary and must keep working.
func TestProjectConfigAllowsProjectShapedKeys(t *testing.T) {
	yaml := "hostname: devbox\n" +
		"cache:\n  enabled: true\n"

	dir := t.TempDir()
	writeProjectConfig(t, dir, yaml)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "empty-xdg"))

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatalf("project-shaped keys must still load: %v", err)
	}
	if cfg.Hostname != "devbox" {
		t.Errorf("Hostname = %q, want devbox", cfg.Hostname)
	}
	if !cfg.Cache.IsEnabled() {
		t.Error("cache.enabled must be settable from a project config")
	}
}

// TestProjectConfigNetworkDirectionOfTravel pins the rule for the keys whose
// legitimacy depends on which way they move. A project asking for *stricter*
// confinement is the reason project configs exist; the same key asking for less
// is how a hostile repo switched off an allowlist the user had configured.
func TestProjectConfigNetworkDirectionOfTravel(t *testing.T) {
	withAllowlistUser := func(t *testing.T) {
		t.Helper()
		withUserConfig(t, "network:\n  mode: allowlist\n")
	}

	t.Run("weakening allowlist to default is refused", func(t *testing.T) {
		withAllowlistUser(t)
		dir := t.TempDir()
		writeProjectConfig(t, dir, "network:\n  mode: default\n")
		if _, err := Load(dir, ""); err == nil {
			t.Error("a project config must not turn off an allowlist the user configured")
		}
	})

	t.Run("strengthening allowlist to none is allowed", func(t *testing.T) {
		withAllowlistUser(t)
		dir := t.TempDir()
		writeProjectConfig(t, dir, "network:\n  mode: none\n")
		cfg, err := Load(dir, "")
		if err != nil {
			t.Fatalf("tightening must be allowed: %v", err)
		}
		if cfg.Network.Mode != "none" {
			t.Errorf("Network.Mode = %q, want none", cfg.Network.Mode)
		}
	})

	t.Run("raising default to allowlist is allowed", func(t *testing.T) {
		dir := t.TempDir()
		writeProjectConfig(t, dir, "network:\n  mode: allowlist\n")
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "empty-xdg"))
		cfg, err := Load(dir, "")
		if err != nil {
			t.Fatalf("a project asking for an allowlist must be allowed: %v", err)
		}
		if cfg.Network.Mode != "allowlist" {
			t.Errorf("Network.Mode = %q, want allowlist", cfg.Network.Mode)
		}
	})
}

// TestExplicitConfigIsTrusted pins the escape hatch. Naming a path is the
// deliberate act that discovery never involves, so --config may carry the keys a
// discovered file may not — otherwise a legitimate checked-in config becomes
// unusable and people work around the refusal in worse ways.
func TestExplicitConfigIsTrusted(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "trusted.yaml")
	if err := os.WriteFile(p, []byte("user: root\nmounts:\n  - {host: "+dir+", container: /data, mode: rw}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "empty-xdg"))

	cfg, err := Load(t.TempDir(), p)
	if err != nil {
		t.Fatalf("an explicitly named config must be trusted: %v", err)
	}
	if cfg.User != "root" {
		t.Errorf("User = %q, want root", cfg.User)
	}
	if len(cfg.Mounts) != 1 {
		t.Errorf("Mounts = %v, want the explicitly-loaded one", cfg.Mounts)
	}
}

// TestUserConfigIsTrusted guards against over-reach in the other direction: the
// restriction must apply to the discovered project layer only. The user's own
// config is the place these keys are supposed to live, so if it were caught too
// the refusal would have no escape at all.
func TestUserConfigIsTrusted(t *testing.T) {
	withUserConfig(t, "user: root\nruntime: runsc\nsecrets:\n  TOK:\n    command: echo hi\n")
	cfg, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatalf("the user's own config must be trusted: %v", err)
	}
	if cfg.User != "root" || cfg.Runtime != "runsc" || cfg.Secrets["TOK"].Command != "echo hi" {
		t.Errorf("user config did not apply: %+v", cfg)
	}
}

// TestFindProjectConfig_BoundedWalk pins where discovery stops. Walking to the
// filesystem root made any ancestor's .sandbox.yaml win silently — and on Linux
// that turns a world-writable shared directory into a config-injection point for
// everyone working beneath it.
func TestFindProjectConfig_BoundedWalk(t *testing.T) {
	t.Run("stops at the repository root", func(t *testing.T) {
		outer := t.TempDir()
		// A config above the repo must NOT be found...
		if err := os.WriteFile(filepath.Join(outer, projectFileName), []byte("hostname: outer\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		repo := filepath.Join(outer, "repo")
		sub := filepath.Join(repo, "a", "b")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := findProjectConfig(sub); got != "" {
			t.Errorf("findProjectConfig = %q, want none: the search must stop at the repo root", got)
		}
		// ...but one at the repo root is.
		want := filepath.Join(repo, projectFileName)
		if err := os.WriteFile(want, []byte("hostname: inner\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := findProjectConfig(sub); got != want {
			t.Errorf("findProjectConfig = %q, want %q", got, want)
		}
	})

	t.Run("outside a repo and outside home, only the start dir counts", func(t *testing.T) {
		// t.TempDir is under /var/folders on darwin and /tmp on linux — neither a
		// repository nor inside the home directory, which is exactly the shared-
		// scratch-space case.
		outer := t.TempDir()
		if err := os.WriteFile(filepath.Join(outer, projectFileName), []byte("hostname: injected\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		sub := filepath.Join(outer, "work")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := findProjectConfig(sub); got != "" {
			t.Errorf("findProjectConfig = %q, want none: a config in a shared parent must not be picked up", got)
		}
	})
}
