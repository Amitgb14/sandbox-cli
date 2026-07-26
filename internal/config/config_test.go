package config

import (
	"os"
	"path/filepath"
	"testing"
)

// withUserConfig points XDG_CONFIG_HOME at a scratch dir and writes content as
// the user's own config, returning that file's path.
//
// Several tests below use this where they once wrote a project .sandbox.yaml:
// the keys they exercise — security, mounts, secrets, runtime, image, user — are
// privilege-relevant and a project file may no longer set them (see trust.go).
// The merge semantics under test are unchanged; only the layer that may express
// them moved.
func withUserConfig(t *testing.T, content string) string {
	t.Helper()
	xdg := t.TempDir()
	dir := filepath.Join(xdg, "sandbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)
	return p
}

// writeProjectConfig writes a .sandbox.yaml into dir and makes dir look like a
// repository root, so findProjectConfig's boundary permits the walk.
func writeProjectConfig(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, projectFileName)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefault(t *testing.T) {
	c := Default()
	if c.Workdir != "/workspace" || c.Home != "/sandbox/home" || c.User != "sandbox" {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("default config should validate: %v", err)
	}
}

func TestDefault_Security(t *testing.T) {
	s := Default().Security
	if !s.NoNewPriv() {
		t.Error("no-new-privileges should default on")
	}
	if len(s.CapDrop) != 1 || s.CapDrop[0] != "ALL" {
		t.Errorf("CapDrop default = %v, want [ALL]", s.CapDrop)
	}
	if s.Pids() != 1024 {
		t.Errorf("PidsLimit default = %d, want 1024", s.Pids())
	}
	// Resource limits are opt-in (unlimited by default).
	if s.Memory != "" || s.CPUs != "" {
		t.Errorf("expected no default memory/cpu limits, got mem=%q cpu=%q", s.Memory, s.CPUs)
	}
}

func TestLoad_SecurityOverride(t *testing.T) {
	// A config can both disable a default-on setting and clear an inherited slice.
	// User-level, because security.* is not settable from a project file.
	withUserConfig(t, "security:\n  no_new_privileges: false\n  cap_drop: []\n  pids_limit: 4096\n  memory: 4g\n")

	cfg, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.NoNewPriv() {
		t.Error("expected no_new_privileges disabled by config")
	}
	if len(cfg.Security.CapDrop) != 0 {
		t.Errorf("expected cap_drop cleared to empty, got %v", cfg.Security.CapDrop)
	}
	if cfg.Security.Pids() != 4096 {
		t.Errorf("PidsLimit = %d, want 4096", cfg.Security.Pids())
	}
	if cfg.Security.Memory != "4g" {
		t.Errorf("Memory = %q, want 4g", cfg.Security.Memory)
	}
}

func TestLoad_NetworkAllowlistFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, projectFileName)
	content := "network:\n  mode: allowlist\n  allow:\n    - internal.example.com\n"
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "empty-xdg"))

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network.Mode != "allowlist" {
		t.Fatalf("Network.Mode = %q, want allowlist", cfg.Network.Mode)
	}
	domains := cfg.Network.EgressDomains()
	if !containsStr(domains, "internal.example.com") {
		t.Errorf("configured domain missing: %v", domains)
	}
	if !containsStr(domains, "api.anthropic.com") {
		t.Errorf("baseline domain missing: %v", domains)
	}
}

// TestBaselineEnabled pins the tri-state: only an explicit false turns the
// built-in domains off, so every config written before the field existed keeps
// the baseline it has always had.
func TestBaselineEnabled(t *testing.T) {
	if !(NetworkSpec{}).BaselineEnabled() {
		t.Error("unset baseline must mean enabled — omitting the field cannot change behavior")
	}
	if !(NetworkSpec{Baseline: boolPtr(true)}).BaselineEnabled() {
		t.Error("baseline: true must mean enabled")
	}
	if (NetworkSpec{Baseline: boolPtr(false)}).BaselineEnabled() {
		t.Error("baseline: false must mean disabled")
	}
}

// TestEgressDomains_BaselineOff proves `allow` becomes the whole list: the point
// of the field is that github.com — a write endpoint, and so an exfiltration
// channel for any token in the container — can actually be declined.
func TestEgressDomains_BaselineOff(t *testing.T) {
	n := NetworkSpec{
		Mode:     "allowlist",
		Allow:    []string{"internal.example.com", "api.anthropic.com"},
		Baseline: boolPtr(false),
	}
	got := n.EgressDomains()
	want := []string{"internal.example.com", "api.anthropic.com"}
	if len(got) != len(want) {
		t.Fatalf("EgressDomains = %v, want exactly %v", got, want)
	}
	for i, d := range want {
		if got[i] != d {
			t.Errorf("EgressDomains[%d] = %q, want %q (order follows allow)", i, got[i], d)
		}
	}
	for _, d := range []string{"github.com", "registry.npmjs.org", "pypi.org"} {
		if containsStr(got, d) {
			t.Errorf("baseline domain %q survived baseline: false: %v", d, got)
		}
	}
	// api.anthropic.com is both a baseline domain and explicitly listed here; it
	// belongs because the user asked for it, and exactly once.
	if countStr(got, "api.anthropic.com") != 1 {
		t.Errorf("api.anthropic.com not carried through exactly once: %v", got)
	}
}

// TestEgressDomains_BaselineOffEmptyIsEmpty pins the sharp edge: the resolved
// list is empty rather than falling back to the baseline. sandbox.BuildSpec is
// what turns that into a refusal — see TestBuildSpec_EmptyAllowlistRefuses.
func TestEgressDomains_BaselineOffEmptyIsEmpty(t *testing.T) {
	got := (NetworkSpec{Mode: "allowlist", Baseline: boolPtr(false)}).EgressDomains()
	if len(got) != 0 {
		t.Fatalf("EgressDomains = %v, want empty — an implicit baseline fallback would silently undo baseline: false", got)
	}
}

// TestLoad_BaselineFalseFromConfig runs the field through the real YAML path,
// since a struct literal would not catch a mistyped tag.
func TestLoad_BaselineFalseFromConfig(t *testing.T) {
	dir := t.TempDir()
	content := "network:\n  mode: allowlist\n  baseline: false\n  allow:\n    - internal.example.com\n"
	if err := os.WriteFile(filepath.Join(dir, projectFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "empty-xdg"))

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network.BaselineEnabled() {
		t.Fatal("baseline: false did not survive the YAML round trip")
	}
	domains := cfg.Network.EgressDomains()
	if containsStr(domains, "github.com") {
		t.Errorf("baseline leaked through Load: %v", domains)
	}
	if !containsStr(domains, "internal.example.com") {
		t.Errorf("configured domain missing: %v", domains)
	}
}

// TestLoad_BaselineIsTriStateAcrossLayers proves a nearer config can turn the
// baseline back *on* — the reason the field is a pointer rather than a bool. A
// plain bool would make the user-level `false` unrecoverable, since a project
// config that never mentions the field is indistinguishable from one that set
// it to the zero value.
func TestLoad_BaselineIsTriStateAcrossLayers(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	userCfg := "network:\n  mode: allowlist\n  baseline: false\n  allow:\n    - user.example.com\n"
	if err := os.WriteFile(filepath.Join(xdg, "sandbox", "config.yaml"), []byte(userCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)

	proj := t.TempDir()

	// The project config is silent on baseline: the user-level false stands.
	if err := os.WriteFile(filepath.Join(proj, projectFileName), []byte("network:\n  allow:\n    - proj.example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(proj, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network.BaselineEnabled() {
		t.Error("a config that never mentions baseline must inherit the user-level false")
	}

	// Turning it back *on* widens the egress boundary, so a project file may not
	// do it — that is the whole point of the direction-of-travel rule in trust.go.
	if err := os.WriteFile(filepath.Join(proj, projectFileName), []byte("network:\n  baseline: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(proj, ""); err == nil {
		t.Error("a project config re-enabling the baseline must be refused: it widens what the container can reach")
	}

	// From a trusted layer it works, which is what makes the field tri-state
	// rather than one-way. A plain bool could not express this at all: a config
	// that never mentions baseline would be indistinguishable from one setting it
	// to the zero value, so an inherited false could never be undone.
	explicit := filepath.Join(t.TempDir(), "trusted.yaml")
	if err := os.WriteFile(explicit, []byte("network:\n  baseline: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(proj, explicit)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Network.BaselineEnabled() {
		t.Error("an explicit baseline: true must override an inherited false")
	}
	if !containsStr(cfg.Network.EgressDomains(), "github.com") {
		t.Error("re-enabling the baseline must bring the built-in domains back")
	}
}

func TestCacheVolumeName(t *testing.T) {
	cases := map[string]string{
		"/sandbox/home/.npm":            "sandbox-cache-npm",
		"/sandbox/home/.cache/pip":      "sandbox-cache-cache-pip",
		"/sandbox/home/.cargo/registry": "sandbox-cache-cargo-registry",
		"/sandbox/home/go/pkg/mod":      "sandbox-cache-go-pkg-mod",
	}
	for path, want := range cases {
		if got := CacheVolumeName(path); got != want {
			t.Errorf("CacheVolumeName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestCachePathsAndEnabled(t *testing.T) {
	// Disabled by default.
	if (CacheSpec{}).IsEnabled() {
		t.Error("cache should be disabled when Enabled is nil")
	}
	on := true
	c := CacheSpec{Enabled: &on, Paths: []string{"/sandbox/home/.cache/pip", "/opt/custom"}}
	if !c.IsEnabled() {
		t.Error("cache should be enabled")
	}
	paths := c.CachePaths()
	if !containsStr(paths, "/sandbox/home/.npm") {
		t.Errorf("defaults missing from CachePaths: %v", paths)
	}
	if !containsStr(paths, "/opt/custom") {
		t.Errorf("configured extra path missing: %v", paths)
	}
	// A configured path that duplicates a default must not appear twice.
	if countStr(paths, "/sandbox/home/.cache/pip") != 1 {
		t.Errorf("duplicate cache path: %v", paths)
	}
}

func TestLoad_CacheOverride(t *testing.T) {
	// cache.paths aims a writable volume at a container path, so it is user-level.
	withUserConfig(t, "cache:\n  enabled: true\n  paths:\n    - /opt/extra-cache\n")

	cfg, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Cache.IsEnabled() {
		t.Error("expected cache enabled from config")
	}
	if !containsStr(cfg.Cache.CachePaths(), "/opt/extra-cache") {
		t.Errorf("configured cache path missing: %v", cfg.Cache.CachePaths())
	}
}

func TestLoad_RuntimeFromConfig(t *testing.T) {
	// runtime selects the strength of the isolation boundary: user-level only.
	withUserConfig(t, "runtime: kata-runtime\n")

	cfg, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runtime != "kata-runtime" {
		t.Errorf("Runtime = %q, want kata-runtime", cfg.Runtime)
	}
	// Unset stays empty (docker default).
	if config2 := Default(); config2.Runtime != "" {
		t.Errorf("default Runtime = %q, want empty", config2.Runtime)
	}
}

func TestLoad_PortsFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, projectFileName)
	if err := os.WriteFile(cfgFile, []byte("ports:\n  - 3000:3000\n  - 5173\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "empty-xdg"))

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Ports) != 2 || cfg.Ports[0] != "3000:3000" || cfg.Ports[1] != "5173" {
		t.Errorf("Ports = %v, want [3000:3000 5173]", cfg.Ports)
	}
	// Nothing is published unless a config or flag says so.
	if d := Default(); len(d.Ports) != 0 {
		t.Errorf("default Ports = %v, want none", d.Ports)
	}
}

// TestLoad_PortsReplaceNotAppend: publishing opens the boundary inward, so a
// project must be able to narrow an inherited set — including to nothing.
func TestLoad_PortsReplaceNotAppend(t *testing.T) {
	xdg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdg, "sandbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	userCfg := filepath.Join(xdg, "sandbox", "config.yaml")
	if err := os.WriteFile(userCfg, []byte("ports:\n  - 9000:9000\n  - 9001:9001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, projectFileName), []byte("ports:\n  - 3000:3000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Ports) != 1 || cfg.Ports[0] != "3000:3000" {
		t.Errorf("project ports must replace the user's, got %v", cfg.Ports)
	}

	// An explicit empty list clears the inherited set rather than being ignored.
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, projectFileName), []byte("ports: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := Load(dir2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg2.Ports) != 0 {
		t.Errorf("`ports: []` must publish nothing, got %v", cfg2.Ports)
	}
}

func TestValidate_BadNetwork(t *testing.T) {
	c := Default()
	c.Network.Mode = "bogus"
	if err := c.Validate(); err == nil {
		t.Error("expected validation error for bad network mode")
	}
}

func TestValidate_AllowlistNetworkOK(t *testing.T) {
	c := Default()
	c.Network.Mode = "allowlist"
	c.Network.Allow = []string{"example.com"}
	if err := c.Validate(); err != nil {
		t.Errorf("allowlist mode should validate: %v", err)
	}
	// Allowlist is bridge networking (no --network flag).
	if c.NetworkArg() != "" {
		t.Errorf("NetworkArg = %q, want empty (bridge) for allowlist", c.NetworkArg())
	}
}

func TestEgressDomains(t *testing.T) {
	// Non-allowlist modes contribute no domains.
	for _, mode := range []string{"", "default", "none"} {
		if got := (NetworkSpec{Mode: mode, Allow: []string{"x.com"}}).EgressDomains(); got != nil {
			t.Errorf("mode %q: EgressDomains = %v, want nil", mode, got)
		}
	}
	// Allowlist mode unions the baseline with Allow, de-duped, baseline first.
	got := (NetworkSpec{Mode: "allowlist", Allow: []string{"example.com", "api.anthropic.com", " "}}).EgressDomains()
	if len(got) == 0 || got[0] != "api.anthropic.com" {
		t.Fatalf("EgressDomains = %v, want baseline first", got)
	}
	if !containsStr(got, "example.com") {
		t.Errorf("EgressDomains missing example.com: %v", got)
	}
	// api.anthropic.com is in the baseline; listing it again must not duplicate.
	if n := countStr(got, "api.anthropic.com"); n != 1 {
		t.Errorf("api.anthropic.com appears %d times, want 1: %v", n, got)
	}
	// The blank entry must be dropped.
	if containsStr(got, "") {
		t.Errorf("empty domain leaked into %v", got)
	}
}

func containsStr(s []string, v string) bool {
	return countStr(s, v) > 0
}

func countStr(s []string, v string) int {
	n := 0
	for _, x := range s {
		if x == v {
			n++
		}
	}
	return n
}

func TestValidate_Secrets(t *testing.T) {
	c := Default()
	// Exactly one source is valid.
	c.Secrets = map[string]SecretSpec{"TOK": {Env: "HOST_TOK"}}
	if err := c.Validate(); err != nil {
		t.Errorf("single-source secret should validate: %v", err)
	}
	// Two sources is invalid.
	c.Secrets = map[string]SecretSpec{"TOK": {Env: "HOST_TOK", File: "/x"}}
	if err := c.Validate(); err == nil {
		t.Error("expected error for a secret with two sources")
	}
	// Zero sources is invalid.
	c.Secrets = map[string]SecretSpec{"TOK": {}}
	if err := c.Validate(); err == nil {
		t.Error("expected error for a secret with no source")
	}
}

func TestLoad_SecretsMergePerKey(t *testing.T) {
	// secrets run host commands and read host files: user-level only.
	withUserConfig(t, "secrets:\n  GITHUB_TOKEN:\n    command: gh auth token\n  API_KEY:\n    file: ~/.secrets/api\n")

	cfg, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Secrets["GITHUB_TOKEN"].Command != "gh auth token" {
		t.Errorf("GITHUB_TOKEN command = %q", cfg.Secrets["GITHUB_TOKEN"].Command)
	}
	if cfg.Secrets["API_KEY"].File != "~/.secrets/api" {
		t.Errorf("API_KEY file = %q", cfg.Secrets["API_KEY"].File)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("loaded secrets should validate: %v", err)
	}
}

func TestValidate_BadMountMode(t *testing.T) {
	c := Default()
	c.Mounts = []MountSpec{{Host: "/a", Container: "/b", Mode: "xx"}}
	if err := c.Validate(); err == nil {
		t.Error("expected validation error for bad mount mode")
	}
}

func TestLoad_ProjectOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	// image and user are user-level; network.mode: none is left in the project
	// file because it *strengthens* the posture, which a project may do.
	withUserConfig(t, "image: my-image:9\nuser: sandbox\n")
	writeProjectConfig(t, dir, "network:\n  mode: none\n")

	cfg, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Image != "my-image:9" {
		t.Errorf("Image = %q, want my-image:9", cfg.Image)
	}
	if cfg.User != "sandbox" {
		t.Errorf("User = %q, want sandbox", cfg.User)
	}
	if cfg.NetworkArg() != "none" {
		t.Errorf("NetworkArg = %q, want none", cfg.NetworkArg())
	}
	// Unset field falls back to default.
	if cfg.Workdir != "/workspace" {
		t.Errorf("Workdir = %q, want default /workspace", cfg.Workdir)
	}
}

func TestLoad_RelativeMountResolvedAgainstConfigDir(t *testing.T) {
	// mounts are user-level, so the "relative to the declaring file" rule is
	// exercised against the user config's own directory.
	userCfg := withUserConfig(t, "mounts:\n  - { host: ./data, container: /workspace/data, mode: rw }\n")
	dir := filepath.Dir(userCfg)

	cfg, err := Load(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(cfg.Mounts))
	}
	want := filepath.Join(dir, "data")
	if cfg.Mounts[0].Host != want {
		t.Errorf("mount host = %q, want %q", cfg.Mounts[0].Host, want)
	}
}

func TestFindProjectConfig_WalksUp(t *testing.T) {
	dir := t.TempDir()
	cfgFile := writeProjectConfig(t, dir, "hostname: walked\n")
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findProjectConfig(sub); got != cfgFile {
		t.Errorf("findProjectConfig = %q, want %q", got, cfgFile)
	}
}
