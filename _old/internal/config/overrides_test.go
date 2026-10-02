package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes a config file for --config, the layer that outranks the
// profile and caused the bug, and isolates the real user layer from the test.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	// The user's real ~/.config/sandbox/config.yaml is merged before the explicit
	// path, so without this these tests read the machine they run on: a developer
	// whose own config sets `sync: true` (or persist_auth, ports, cap_add, a root
	// user) would see prod refuse for a reason unrelated to the change, while CI
	// with a clean HOME stayed green. Every other test in this package pins it.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestNetworkOverrideReachesTheProfileCheck is the regression for a documented
// escape hatch that could not be used.
//
// prod requires network.mode allowlist. A user config saying `mode: default`
// outranks the profile, so prod refuses — correctly. `--network allowlist` is
// documented as outranking the profile's default and is the way to say "I do
// want the allowlist for this run", but it was applied *after* LoadProfile had
// already validated and refused, so it could never take effect.
func TestNetworkOverrideReachesTheProfileCheck(t *testing.T) {
	cfgPath := writeConfig(t, "network:\n  mode: default\n")

	if _, err := LoadProfile(t.TempDir(), cfgPath, ProfileProd); err == nil {
		t.Fatal("prod must refuse a config whose mode is default; without that there is no bug to fix")
	}

	cfg, err := LoadProfileWith(t.TempDir(), cfgPath, ProfileProd,
		Overrides{NetworkMode: "allowlist"})
	if err != nil {
		t.Fatalf("--network allowlist must be able to satisfy prod: %v", err)
	}
	if cfg.Network.Mode != "allowlist" {
		t.Errorf("mode = %q, want allowlist", cfg.Network.Mode)
	}
}

// TestNetworkOverrideCannotEscapeTheProfile: the flag outranks the *files*, not
// the profile's own demands. Overriding to a mode prod forbids must still be
// refused, or the escape hatch would be a way out of prod entirely.
func TestNetworkOverrideCannotEscapeTheProfile(t *testing.T) {
	cfgPath := writeConfig(t, "network:\n  mode: allowlist\n")

	for _, mode := range []string{"default", "none"} {
		if _, err := LoadProfileWith(t.TempDir(), cfgPath, ProfileProd,
			Overrides{NetworkMode: mode}); err == nil {
			t.Errorf("--network %s must still be refused under prod: the flag beats a file, not the profile", mode)
		}
	}
}

// TestNoOverrideChangesNothing: an absent flag must leave the resolved mode
// exactly as the layers left it, on every profile.
func TestNoOverrideChangesNothing(t *testing.T) {
	cfgPath := writeConfig(t, "network:\n  mode: none\n")

	plain, err := LoadProfile(t.TempDir(), cfgPath, ProfileDev)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := LoadProfileWith(t.TempDir(), cfgPath, ProfileDev, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Network.Mode != "none" || empty.Network.Mode != "none" {
		t.Errorf("modes = %q / %q, want none unchanged", plain.Network.Mode, empty.Network.Mode)
	}
}
