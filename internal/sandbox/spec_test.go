package sandbox

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"path/filepath"
)

// baseCfg is the configuration these tests mean when they are not testing the
// egress allowlist: Default() plus an explicit "no allowlist".
//
// Default() now selects the allowlist, which changes the shape of the spec —
// the container starts as root so the firewall can program itself, then drops.
// That is correct, and it is what TestBuildSpec_AllowlistIsTheDefault covers;
// the cases below are about mounts, users, resources and the rest, and they read
// wrongly if every one of them is also an allowlist case.
func baseCfg() config.Config {
	c := config.Default()
	c.Network.Mode = "default"
	return c
}

func TestBuildSpec_WorkspaceMountAndHome(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Home != "/sandbox/home" {
		t.Errorf("Home = %q, want /sandbox/home", spec.Home)
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Target != "/workspace" {
		t.Fatalf("expected single /workspace mount, got %+v", spec.Mounts)
	}
	// No mount may point at a HOME-like location; only the temp project dir.
	for _, m := range spec.Mounts {
		if strings.HasSuffix(m.Target, "home") {
			t.Errorf("unexpected home mount: %+v", m)
		}
	}
}

func TestBuildSpec_ExplicitEnv(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Env: []string{"FOO=bar"}, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want bar", spec.Env["FOO"])
	}
}

func TestBuildSpec_EnvAllowOnlyIfPresent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRESENT_KEY", "v")
	spec, err := BuildSpec(baseCfg(), Options{
		Project:  dir,
		EnvAllow: []string{"PRESENT_KEY", "ABSENT_KEY_XYZ"},
		Command:  []string{"sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(spec.EnvNames, "PRESENT_KEY") {
		t.Errorf("expected PRESENT_KEY forwarded, got %v", spec.EnvNames)
	}
	if contains(spec.EnvNames, "ABSENT_KEY_XYZ") {
		t.Errorf("did not expect ABSENT_KEY_XYZ, got %v", spec.EnvNames)
	}
}

func TestBuildSpec_FlagMounts(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{
		Project:     dir,
		ExtraMounts: []string{"/h/data:/workspace/data:rw", "/h/cfg:/etc/cfg"},
		Command:     []string{"sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var rw, ro *runtime.Mount
	for i := range spec.Mounts {
		switch spec.Mounts[i].Target {
		case "/workspace/data":
			rw = &spec.Mounts[i]
		case "/etc/cfg":
			ro = &spec.Mounts[i]
		}
	}
	if rw == nil || rw.RO {
		t.Errorf("expected /workspace/data as rw, got %+v", rw)
	}
	if ro == nil || !ro.RO {
		t.Errorf("expected /etc/cfg as ro (default), got %+v", ro)
	}
}

func TestBuildSpec_BadMount(t *testing.T) {
	dir := t.TempDir()
	_, err := BuildSpec(baseCfg(), Options{Project: dir, ExtraMounts: []string{"onlyone"}, Command: []string{"sh"}})
	if err == nil {
		t.Fatal("expected error for malformed mount")
	}
}

func TestBuildSpec_ImageAndUserOverride(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Image: "custom:1", User: "sandbox", Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Image != "custom:1" {
		t.Errorf("Image = %q, want custom:1", spec.Image)
	}
	if spec.User != "sandbox" {
		t.Errorf("User = %q, want sandbox", spec.User)
	}
}

func TestBuildSpec_SecurityDefaults(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if !spec.NoNewPrivileges {
		t.Error("expected NoNewPrivileges on by default")
	}
	if len(spec.CapDrop) != 1 || spec.CapDrop[0] != "ALL" {
		t.Errorf("CapDrop = %v, want [ALL]", spec.CapDrop)
	}
	if spec.PidsLimit != 1024 {
		t.Errorf("PidsLimit = %d, want 1024", spec.PidsLimit)
	}
}

func TestBuildSpec_NoHardeningAndResourceFlags(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{
		Project:     dir,
		Command:     []string{"sh"},
		NoHardening: true,
		Memory:      "2g",
		CPUs:        "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.NoNewPrivileges {
		t.Error("--no-hardening should disable NoNewPrivileges")
	}
	if len(spec.CapDrop) != 0 {
		t.Errorf("--no-hardening should clear CapDrop, got %v", spec.CapDrop)
	}
	if spec.PidsLimit != 0 {
		t.Errorf("--no-hardening should clear PidsLimit, got %d", spec.PidsLimit)
	}
	// Resource limits are independent of --no-hardening.
	if spec.Memory != "2g" || spec.CPUs != "1" {
		t.Errorf("resource flags not mapped: mem=%q cpu=%q", spec.Memory, spec.CPUs)
	}
}

func TestBuildSpec_EgressAllowlistFromFlag(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{
		Project: dir,
		Allow:   []string{"internal.example.com"},
		Command: []string{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Firewall must run as root, drop back to the sandbox user via the entrypoint.
	if spec.User != "root" {
		t.Errorf("User = %q, want root when the firewall is active", spec.User)
	}
	if spec.Entrypoint != "/usr/local/bin/sandbox-firewall" {
		t.Errorf("Entrypoint = %q, want the firewall wrapper", spec.Entrypoint)
	}
	if spec.Env["SANDBOX_RUN_AS"] != "sandbox" {
		t.Errorf("SANDBOX_RUN_AS = %q, want sandbox", spec.Env["SANDBOX_RUN_AS"])
	}
	allow := spec.Env["SANDBOX_EGRESS_ALLOW"]
	if !strings.Contains(allow, "internal.example.com") {
		t.Errorf("SANDBOX_EGRESS_ALLOW missing the flag domain: %q", allow)
	}
	if !strings.Contains(allow, "api.anthropic.com") {
		t.Errorf("SANDBOX_EGRESS_ALLOW missing a baseline domain: %q", allow)
	}
	// Every capability the sandbox-firewall entrypoint needs: NET_ADMIN/NET_RAW to
	// program iptables, SETUID/SETGID for the setpriv drop, and KILL so tini (PID 1
	// as root under --init) can forward signals to the dropped-privilege agent.
	// Missing KILL aborts the container on the first signal with
	// "[FATAL tini] Unexpected error when forwarding signal: Operation not permitted".
	for _, c := range []string{"NET_ADMIN", "NET_RAW", "SETUID", "SETGID", "KILL"} {
		if !contains(spec.CapAdd, c) {
			t.Errorf("CapAdd missing %s: %v", c, spec.CapAdd)
		}
	}
	// Allowlist implies bridge networking, never "none".
	if spec.Network == "none" {
		t.Error("allowlist must not run with --network none")
	}
}

func TestBuildSpec_AllowlistOverridesNetworkNone(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg()
	cfg.Network.Mode = "none"
	spec, err := BuildSpec(cfg, Options{Project: dir, Allow: []string{"x.com"}, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Network == "none" {
		t.Error("--allow should switch networking from none to bridge so the allowlist is reachable")
	}
	if spec.Entrypoint == "" {
		t.Error("expected the firewall entrypoint to be set")
	}
}

// TestValidateMountTarget_ProtectsContainerBinaries pins the guard that stops a
// caller-supplied mount from replacing the files the container's own startup
// depends on. The live case was `workdir: /usr/local/bin` in a project config:
// it moved the workspace mount target onto the directory holding
// sandbox-firewall, so in allowlist mode root executed the repository's own file
// and no firewall was ever programmed.
func TestValidateMountTarget_ProtectsContainerBinaries(t *testing.T) {
	refuse := []string{
		"/usr/local/bin", // holds sandbox-firewall, the root entrypoint
		"/usr/local/bin/",
		"/usr/local/bin/.", // must not be normalizable past the check
		"/usr",             // shadows /usr/local/bin without naming it
		"/", "/bin", "/sbin", "/etc", "/proc", "/sys", "/dev", "/var/run",
	}
	for _, tgt := range refuse {
		if err := ValidateMountTarget(tgt); err == nil {
			t.Errorf("ValidateMountTarget(%q) = nil, want a refusal", tgt)
		}
	}
	allow := []string{"/workspace", "/app", "/sandbox/home", "/usr/local/share/x", "/srv/data"}
	for _, tgt := range allow {
		if err := ValidateMountTarget(tgt); err != nil {
			t.Errorf("ValidateMountTarget(%q) = %v, want nil", tgt, err)
		}
	}
	// A relative target is meaningless inside the container and must not pass.
	if err := ValidateMountTarget("workspace"); err == nil {
		t.Error("a relative mount target must be refused")
	}
}

// TestBuildSpec_RefusesWorkdirOverContainerBinaries is the end-to-end half: the
// refusal has to fire through BuildSpec, not just in the helper.
func TestBuildSpec_RefusesWorkdirOverContainerBinaries(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg()
	cfg.Workdir = "/usr/local/bin"
	cfg.Network.Mode = "allowlist" // the mode whose entrypoint lives there

	spec, err := BuildSpec(cfg, Options{Project: dir, Command: []string{"sh"}})
	if err == nil {
		t.Fatalf("expected a refusal; got a spec mounting the workspace at %q with entrypoint %q",
			cfg.Workdir, spec.Entrypoint)
	}
	for _, m := range spec.Mounts {
		if m.Target == "/usr/local/bin" {
			t.Errorf("refusal still produced a mount over the entrypoint directory: %+v", m)
		}
	}
}

// TestBuildSpec_RefusesMountsOverContainerBinaries covers the other two ways a
// target arrives: a config `mounts:` entry and a --mount flag.
func TestBuildSpec_RefusesMountsOverContainerBinaries(t *testing.T) {
	dir := t.TempDir()

	cfg := baseCfg()
	cfg.Mounts = []config.MountSpec{{Host: dir, Container: "/usr/local/bin", Mode: "rw"}}
	if _, err := BuildSpec(cfg, Options{Project: dir, Command: []string{"sh"}}); err == nil {
		t.Error("config mounts: a target over /usr/local/bin must be refused")
	}

	if _, err := BuildSpec(baseCfg(), Options{
		Project:     dir,
		ExtraMounts: []string{dir + ":/usr/local/bin:rw"},
		Command:     []string{"sh"},
	}); err == nil {
		t.Error("--mount: a target over /usr/local/bin must be refused")
	}
}

// TestBuildSpec_ReservedEnvNamespace pins that nothing outside sandbox-cli can
// occupy SANDBOX_*. Those variables instruct code running as root before the
// agent starts, so supplying one from outside is an off switch: SANDBOX_RUN_AS
// skips the privilege drop, an empty SANDBOX_EGRESS_ALLOW makes the firewall
// entrypoint a passthrough.
func TestBuildSpec_ReservedEnvNamespace(t *testing.T) {
	dir := t.TempDir()

	// Forwarded by name (--env-allow / config env_allow): dropped silently,
	// because that list is "forward it if it happens to be set" and a host that
	// exports a SANDBOX_* name should not fail the run.
	t.Setenv("SANDBOX_RUN_AS", "root")
	spec, err := BuildSpec(baseCfg(), Options{
		Project:  dir,
		EnvAllow: []string{"SANDBOX_RUN_AS"},
		Allow:    []string{"x.example.com"}, // so BuildSpec sets the real value
		Command:  []string{"sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range spec.EnvNames {
		if n == "SANDBOX_RUN_AS" {
			t.Error("SANDBOX_RUN_AS was forwarded by name; docker keeps the last -e, so the host value would win")
		}
	}
	if spec.Env["SANDBOX_RUN_AS"] != "sandbox" {
		t.Errorf("SANDBOX_RUN_AS = %q, want sandbox", spec.Env["SANDBOX_RUN_AS"])
	}

	// Set deliberately via --env: refused by name, because here the user is
	// asking rather than inheriting and a silent drop would mislead.
	if _, err := BuildSpec(baseCfg(), Options{
		Project: dir,
		Env:     []string{"SANDBOX_EGRESS_ALLOW=evil.example.com"},
		Command: []string{"sh"},
	}); err == nil {
		t.Error("--env SANDBOX_EGRESS_ALLOW=... must be refused")
	}

	// SANDBOX_UMASK is the one reserved name read *after* the drop, so it is worth
	// its own row: the reason it is reserved is not privilege but reach. 0000 makes
	// every file the agent writes world-writable, and those files land on a
	// bind-mounted host path.
	if _, err := BuildSpec(baseCfg(), Options{
		Project: dir,
		Env:     []string{"SANDBOX_UMASK=0000"},
		Command: []string{"sh"},
	}); err == nil {
		t.Error("--env SANDBOX_UMASK=... must be refused: it decides the mode of files written to a host path")
	}
}

// TestBuildSpec_ReservationDoesNotEatUserKnobs guards the narrowness of the
// reservation. It was briefly a whole-namespace `SANDBOX_*` ban, which silently
// broke `--env SANDBOX_STATUSLINE_NO_USAGE=1` — a user-facing opt-out documented
// in docs/AGENTS.md, read by an unprivileged script long after privileges are
// dropped. Variables consumed by the root-phase startup belong on the list; the
// only other admissible reason is SANDBOX_UMASK's, which is read after the drop
// but decides the mode of files written to a bind-mounted host path.
func TestBuildSpec_ReservationDoesNotEatUserKnobs(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{
		Project: dir,
		Env:     []string{"SANDBOX_STATUSLINE_NO_USAGE=1"},
		Command: []string{"sh"},
	})
	if err != nil {
		t.Fatalf("the documented statusline opt-out must still work: %v", err)
	}
	if spec.Env["SANDBOX_STATUSLINE_NO_USAGE"] != "1" {
		t.Errorf("SANDBOX_STATUSLINE_NO_USAGE = %q, want 1", spec.Env["SANDBOX_STATUSLINE_NO_USAGE"])
	}

	// And a plainly-named host variable still forwards, prefix or not.
	t.Setenv("SANDBOX_TEST_ALLOWED", "yes")
	spec, err = BuildSpec(baseCfg(), Options{
		Project:  dir,
		EnvAllow: []string{"SANDBOX_TEST_ALLOWED"},
		Command:  []string{"sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(spec.EnvNames, "SANDBOX_TEST_ALLOWED") {
		t.Errorf("a non-control variable must still forward: %v", spec.EnvNames)
	}
}

func falsePtr() *bool { b := false; return &b }

// TestBuildSpec_BaselineOffNarrowsTheAllowlist proves the firewall is still
// fully wired when the baseline is off — the point is a *narrower* allowlist,
// not a weaker one — and that the built-in domains really are gone.
func TestBuildSpec_BaselineOffNarrowsTheAllowlist(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg()
	cfg.Network.Mode = "allowlist"
	cfg.Network.Baseline = falsePtr()
	cfg.Network.Allow = []string{"internal.example.com"}

	spec, err := BuildSpec(cfg, Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Env["SANDBOX_EGRESS_ALLOW"]; got != "internal.example.com" {
		t.Errorf("SANDBOX_EGRESS_ALLOW = %q, want exactly the configured domain", got)
	}
	// github.com is the domain this feature exists to be able to decline: it is a
	// write endpoint, so a token in the container can be pushed out through it.
	for _, d := range []string{"github.com", "registry.npmjs.org", "api.anthropic.com"} {
		if strings.Contains(spec.Env["SANDBOX_EGRESS_ALLOW"], d) {
			t.Errorf("baseline domain %q survived baseline: false", d)
		}
	}
	// The firewall itself must be unchanged — same entrypoint, same privileges.
	if spec.Entrypoint != "/usr/local/bin/sandbox-firewall" {
		t.Errorf("Entrypoint = %q, want the firewall wrapper", spec.Entrypoint)
	}
	if spec.User != "root" || spec.Env["SANDBOX_RUN_AS"] != "sandbox" {
		t.Errorf("User = %q / SANDBOX_RUN_AS = %q, want root dropping to sandbox", spec.User, spec.Env["SANDBOX_RUN_AS"])
	}
	if !contains(spec.CapAdd, "NET_ADMIN") {
		t.Errorf("CapAdd missing NET_ADMIN: %v", spec.CapAdd)
	}
}

// TestBuildSpec_EmptyAllowlistRefuses is the fail-closed guard. The firewall is
// only wired when there are domains to permit, so an allowlist that resolved to
// nothing would otherwise produce a container with no egress filtering at all —
// the strictest request yielding the weakest result. It must refuse instead.
func TestBuildSpec_EmptyAllowlistRefuses(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg()
	cfg.Network.Mode = "allowlist"
	cfg.Network.Baseline = falsePtr() // and no Allow at all

	spec, err := BuildSpec(cfg, Options{Project: dir, Command: []string{"sh"}})
	if err == nil {
		t.Fatalf("expected a refusal; got a runnable spec with entrypoint %q and allow %q",
			spec.Entrypoint, spec.Env["SANDBOX_EGRESS_ALLOW"])
	}
	if !strings.Contains(err.Error(), "mode: none") {
		t.Errorf("error should point at the right way to reach nothing: %v", err)
	}
	// Nothing runnable may come back alongside the error.
	if spec.Entrypoint != "" || spec.Env["SANDBOX_EGRESS_ALLOW"] != "" {
		t.Errorf("refusal returned a partially-built spec: entrypoint %q, allow %q",
			spec.Entrypoint, spec.Env["SANDBOX_EGRESS_ALLOW"])
	}
}

// TestBuildSpec_BaselineOffHoldsForTheAllowFlag covers the case the old code got
// wrong. `--allow` seeds the baseline when it switches the allowlist on by
// itself; that seeding used to key off "the config produced no domains", which
// is exactly what `baseline: false` produces — so the flag would have silently
// reinstated the domains the config had just declined.
func TestBuildSpec_BaselineOffHoldsForTheAllowFlag(t *testing.T) {
	dir := t.TempDir()

	// The config declines the baseline but never names a mode: --allow alone is
	// what turns the allowlist on.
	cfg := baseCfg()
	cfg.Network.Baseline = falsePtr()

	spec, err := BuildSpec(cfg, Options{Project: dir, Allow: []string{"x.example.com"}, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Env["SANDBOX_EGRESS_ALLOW"]; got != "x.example.com" {
		t.Errorf("SANDBOX_EGRESS_ALLOW = %q, want only the flag domain — the baseline must stay declined", got)
	}
	if spec.Entrypoint == "" {
		t.Error("--allow must still switch the firewall on")
	}

	// And with a mode plus configured domains, the flag adds to them rather than
	// bringing the baseline back.
	cfg.Network.Mode = "allowlist"
	cfg.Network.Allow = []string{"a.example.com"}
	spec, err = BuildSpec(cfg, Options{Project: dir, Allow: []string{"b.example.com"}, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Env["SANDBOX_EGRESS_ALLOW"]; got != "a.example.com,b.example.com" {
		t.Errorf("SANDBOX_EGRESS_ALLOW = %q, want the config and flag domains only", got)
	}
}

// TestBuildSpec_AllowFlagStillSeedsTheBaseline guards the default from the
// change above: with no opinion in the config, --allow behaves as it always has.
func TestBuildSpec_AllowFlagStillSeedsTheBaseline(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Allow: []string{"x.example.com"}, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	allow := spec.Env["SANDBOX_EGRESS_ALLOW"]
	if !strings.Contains(allow, "github.com") || !strings.Contains(allow, "x.example.com") {
		t.Errorf("SANDBOX_EGRESS_ALLOW = %q, want the baseline plus the flag domain", allow)
	}
	if !strings.HasPrefix(allow, "api.anthropic.com") {
		t.Errorf("SANDBOX_EGRESS_ALLOW = %q, want baseline first", allow)
	}
}

// TestBuildSpec_AllowlistIsTheDefault pins the changed default. Dev's egress is
// bounded because dev is where the developer's own long-lived credential is in
// reach: the persisted agent HOME holds an OAuth refresh token the agent can
// read, and with unbounded egress nothing stopped it being posted anywhere.
func TestBuildSpec_AllowlistIsTheDefault(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(config.Default(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Entrypoint == "" {
		t.Error("the default config did not wire the firewall entrypoint")
	}
	if spec.Env["SANDBOX_EGRESS_ALLOW"] == "" {
		t.Error("the default config did not pass an egress allowlist")
	}
	// The baseline stays on, so npm, pip and git keep working out of the box.
	if !strings.Contains(spec.Env["SANDBOX_EGRESS_ALLOW"], "registry.npmjs.org") {
		t.Errorf("the baseline is missing from the default allowlist: %q", spec.Env["SANDBOX_EGRESS_ALLOW"])
	}
}

// And the escape hatch: --network default declines it for one run.
func TestBuildSpec_NoEgressWhenNetworkIsDefault(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Entrypoint != "" {
		t.Errorf("no egress requested but Entrypoint = %q", spec.Entrypoint)
	}
	if _, ok := spec.Env["SANDBOX_EGRESS_ALLOW"]; ok {
		t.Error("no egress requested but SANDBOX_EGRESS_ALLOW is set")
	}
	if spec.User != "sandbox" {
		t.Errorf("User = %q, want sandbox (unchanged) without egress", spec.User)
	}
	// None of the entrypoint's capabilities are granted back when there is no
	// allowlist: the default posture stays cap-drop ALL with nothing added.
	for _, c := range []string{"NET_ADMIN", "NET_RAW", "SETUID", "SETGID", "KILL"} {
		if contains(spec.CapAdd, c) {
			t.Errorf("unexpected %s without egress: %v", c, spec.CapAdd)
		}
	}
}

func TestBuildSpec_CacheVolumes(t *testing.T) {
	dir := t.TempDir()

	// Off by default: no volume mounts.
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range spec.Mounts {
		if m.Volume {
			t.Errorf("no cache requested but got a volume mount: %+v", m)
		}
	}

	// --cache adds a named volume per default cache path.
	spec, err = BuildSpec(baseCfg(), Options{Project: dir, Cache: true, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	var npm *runtime.Mount
	volCount := 0
	for i := range spec.Mounts {
		if spec.Mounts[i].Volume {
			volCount++
			if spec.Mounts[i].Target == "/sandbox/home/.npm" {
				npm = &spec.Mounts[i]
			}
		}
	}
	if volCount == 0 {
		t.Fatal("--cache should add cache volume mounts")
	}
	if npm == nil {
		t.Fatalf("expected an npm cache volume, got mounts %+v", spec.Mounts)
	}
	if npm.Source != "sandbox-cache-npm" {
		t.Errorf("npm cache volume name = %q, want sandbox-cache-npm", npm.Source)
	}
	// The workspace bind mount must still be present and singular.
	binds := 0
	for _, m := range spec.Mounts {
		if !m.Volume {
			binds++
		}
	}
	if binds != 1 {
		t.Errorf("expected exactly one bind mount (workspace), got %d: %+v", binds, spec.Mounts)
	}
}

func TestBuildSpec_SecretsForwardedByName(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg()
	cfg.Secrets = map[string]config.SecretSpec{"CONFIG_TOKEN": {Command: "gh auth token"}}
	spec, err := BuildSpec(cfg, Options{
		Project: dir,
		Secrets: []string{"FLAG_TOKEN=file:~/.secrets/x"},
		Command: []string{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Both secrets forwarded by name...
	if !contains(spec.EnvNames, "CONFIG_TOKEN") {
		t.Errorf("config secret not forwarded: %v", spec.EnvNames)
	}
	if !contains(spec.EnvNames, "FLAG_TOKEN") {
		t.Errorf("flag secret not forwarded: %v", spec.EnvNames)
	}
	// ...and their values must NOT appear as explicit env on the spec (which would
	// put them on the docker argv / dry-run). BuildSpec must not resolve values.
	if _, ok := spec.Env["CONFIG_TOKEN"]; ok {
		t.Error("secret value leaked into spec.Env (would hit the argv)")
	}
	if _, ok := spec.Env["FLAG_TOKEN"]; ok {
		t.Error("secret value leaked into spec.Env (would hit the argv)")
	}
}

func TestBuildSpec_BadSecretFlag(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"NOEQUALS", "NAME=", "NAME=bogus:x", "NAME=file:"} {
		_, err := BuildSpec(baseCfg(), Options{Project: dir, Secrets: []string{bad}, Command: []string{"sh"}})
		if err == nil {
			t.Errorf("expected error for malformed --secret %q", bad)
		}
	}
}

// TestForwardedValues_ResolvesWithoutTouchingOurEnv covers both halves of the
// contract: the values are produced, and this process's own environment is left
// alone. The second half is the security-relevant one — secrets used to be
// os.Setenv'd here, and sandbox-cli spawns docker and git *after* that point, so
// a secret named PATH or DOCKER_HOST redirected them (and the preflight ran
// before the injection, so nothing noticed).
func TestForwardedValues_ResolvesWithoutTouchingOurEnv(t *testing.T) {
	t.Setenv("SRC_ENV_SECRET", "topsecret")

	cfg := config.Default()
	cfg.Secrets = map[string]config.SecretSpec{"BROKERED_TOKEN": {Env: "SRC_ENV_SECRET"}}
	got, err := forwardedValues(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got["BROKERED_TOKEN"] != "topsecret" {
		t.Errorf("BROKERED_TOKEN = %q, want topsecret", got["BROKERED_TOKEN"])
	}
	if v, ok := os.LookupEnv("BROKERED_TOKEN"); ok {
		t.Errorf("resolving a secret set it in our own environment (=%q); it must reach the docker child only", v)
	}

	// A --secret flag with an env: source resolves the same way.
	got, err = forwardedValues(config.Default(), Options{Secrets: []string{"FLAG_TOKEN=env:SRC_ENV_SECRET"}})
	if err != nil {
		t.Fatal(err)
	}
	if got["FLAG_TOKEN"] != "topsecret" {
		t.Errorf("FLAG_TOKEN = %q, want topsecret", got["FLAG_TOKEN"])
	}
	if _, ok := os.LookupEnv("FLAG_TOKEN"); ok {
		t.Error("--secret leaked into our own environment")
	}

	// A process-controlling name is exactly the case that made this dangerous.
	got, err = forwardedValues(config.Default(), Options{Secrets: []string{"PATH=env:SRC_ENV_SECRET"}})
	if err != nil {
		t.Fatal(err)
	}
	if got["PATH"] != "topsecret" {
		t.Errorf("PATH = %q, want it carried to the child", got["PATH"])
	}
	if os.Getenv("PATH") == "topsecret" {
		t.Fatal("a secret named PATH replaced our own PATH; docker and git would no longer resolve")
	}

	// Nothing configured yields nothing, so the runtime inherits our env unchanged.
	if v, err := forwardedValues(config.Default(), Options{}); err != nil || v != nil {
		t.Errorf("forwardedValues with nothing configured = %v, %v; want nil, nil", v, err)
	}
}

func TestBuildSpec_HostGatewayAndAddHosts(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{
		Project:     dir,
		HostGateway: true,
		AddHosts:    []string{"db:10.0.0.5"},
		Command:     []string{"sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(spec.AddHosts, "host.docker.internal:host-gateway") {
		t.Errorf("--host-gateway missing: %v", spec.AddHosts)
	}
	if !contains(spec.AddHosts, "db:10.0.0.5") {
		t.Errorf("--add-host passthrough missing: %v", spec.AddHosts)
	}
	// By default the gateway is NOT mapped — and the host names are pointed at the
	// container's own loopback, because on Docker Desktop they resolve whether or
	// not the flag was given. See TestBuildSpec_HostNamesAreNeutralisedWithoutTheFlag.
	bare, _ := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	for _, h := range bare.AddHosts {
		if strings.Contains(h, "host-gateway") || strings.HasPrefix(h, "db:") {
			t.Errorf("unexpected AddHosts by default: %v", bare.AddHosts)
		}
	}
}

func TestBuildSpec_GitIdentity(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, GitIdentity: true, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	// Workspace-trust env is set explicitly (visible, not secret).
	if spec.Env["GIT_CONFIG_KEY_0"] != "safe.directory" || spec.Env["GIT_CONFIG_VALUE_0"] != "*" {
		t.Errorf("git safe.directory env not set: %v", spec.Env)
	}
	// Identity vars are forwarded by name (values resolved at run time, not here).
	for _, n := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_EMAIL"} {
		if !contains(spec.EnvNames, n) {
			t.Errorf("git identity var %s not forwarded by name: %v", n, spec.EnvNames)
		}
		if _, ok := spec.Env[n]; ok {
			t.Errorf("git identity value leaked into spec.Env for %s", n)
		}
	}
	// Off by default.
	bare, _ := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if _, ok := bare.Env["GIT_CONFIG_COUNT"]; ok {
		t.Error("git env set without --git")
	}
}

func TestBuildSpec_Runtime(t *testing.T) {
	dir := t.TempDir()

	// Default: no runtime (docker's default runc).
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Runtime != "" {
		t.Errorf("Runtime = %q, want empty by default", spec.Runtime)
	}

	// Flag sets it.
	spec, err = BuildSpec(baseCfg(), Options{Project: dir, Runtime: "runsc", Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Runtime != "runsc" {
		t.Errorf("Runtime = %q, want runsc", spec.Runtime)
	}

	// Flag overrides config.
	cfg := baseCfg()
	cfg.Runtime = "kata-runtime"
	spec, err = BuildSpec(cfg, Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Runtime != "kata-runtime" {
		t.Errorf("config Runtime not applied: %q", spec.Runtime)
	}
	spec, _ = BuildSpec(cfg, Options{Project: dir, Runtime: "runsc", Command: []string{"sh"}})
	if spec.Runtime != "runsc" {
		t.Errorf("flag should override config runtime, got %q", spec.Runtime)
	}
}

// TestBuildSpec_DetachResolvesAnUnattendedRun pins the three things --detach
// decides beyond passing -d to docker. Each of them is wrong by default for a
// container nobody is watching, and each is silent when it is wrong: a pty makes
// an agent draw a UI for no one, --rm throws away the exit code that says whether
// the work happened, and the gauge draws to a terminal this process is about to
// leave.
func TestBuildSpec_DetachResolvesAnUnattendedRun(t *testing.T) {
	dir := t.TempDir()
	wantTTY := true
	spec, err := BuildSpec(baseCfg(), Options{
		Project: dir,
		Command: []string{"claude", "-p", "do the thing"},
		Detach:  true,
		TTY:     &wantTTY, // even an explicit --tty cannot conjure a terminal
	})
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Detach {
		t.Error("Detach not carried into the spec")
	}
	if spec.TTY {
		t.Error("a detached run must not allocate a pty, even with --tty")
	}
	if spec.Remove {
		t.Error("a detached container must be retained: its logs and exit code are the whole record")
	}
	if spec.ShowMetrics || spec.ShowSummary {
		t.Error("a detached run has no terminal to draw the gauge or summary on")
	}

	// A foreground run is unaffected by any of it.
	fg, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if !fg.Remove || fg.Detach {
		t.Errorf("foreground run changed: Remove=%v Detach=%v", fg.Remove, fg.Detach)
	}
}

// TestBuildSpec_DetachedNameIsDeterministic covers the reason the name matters:
// docker refuses a duplicate container name atomically, so a stable
// sandbox-<repo>-<branch> is what enforces one agent per branch. A check-then-
// launch would have a window in between; a name has none.
func TestBuildSpec_DetachedNameIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	opts := Options{
		Project: dir,
		Command: []string{"sh"},
		Detach:  true,
		RepoID:  "app-1234abcd",
		Branch:  "feature/login",
	}
	first, err := BuildSpec(baseCfg(), opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildSpec(baseCfg(), opts)
	if err != nil {
		t.Fatal(err)
	}
	const want = "sandbox-app-1234abcd-feature-login"
	if first.Name != want {
		t.Errorf("Name = %q, want %q", first.Name, want)
	}
	if first.Name != second.Name {
		t.Errorf("detached name not stable: %q vs %q", first.Name, second.Name)
	}

	// With no repo/branch to build from there is nothing to be deterministic
	// about, so it falls back to the unique timestamped form rather than
	// colliding every run.
	bare, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}, Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	if bare.Name == want || !strings.HasPrefix(bare.Name, "sandbox-") {
		t.Errorf("unexpected fallback name %q", bare.Name)
	}

	// Foreground runs keep the timestamp: repeating one must not collide with the
	// run before it.
	a, _ := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}, RepoID: "app-1234abcd", Branch: "feature/login"})
	b, _ := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}, RepoID: "app-1234abcd", Branch: "feature/login"})
	if a.Name == b.Name {
		t.Errorf("foreground names must stay unique, got %q twice", a.Name)
	}
}

// TestBuildSpec_LabelsStampIdentity: docker is the state store, so whatever a
// later command needs to know about a run has to be stamped on at launch.
func TestBuildSpec_LabelsStampIdentity(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{
		Project: dir,
		Command: []string{"sh"},
		RepoID:  "app-1234abcd",
		Branch:  "feature-a",
		Agent:   "claude",
		Base:    "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"sandbox.repo":   "app-1234abcd",
		"sandbox.branch": "feature-a",
		"sandbox.agent":  "claude",
		"sandbox.base":   "main",
	}
	for k, v := range want {
		if spec.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, spec.Labels[k], v)
		}
	}

	// An unknown fact is left unstamped rather than stamped blank, so a label
	// that is present always means something. A plain `run` outside a repository
	// carries no labels at all.
	partial, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}, Branch: "feature-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := partial.Labels["sandbox.agent"]; ok {
		t.Errorf("empty agent should not be labelled: %v", partial.Labels)
	}
	// With nothing to stamp about the *work*, the identifying label still has to
	// be there. Those describe the run (repo, branch, agent) and are omitted when
	// there is nothing true to say — which meant a run outside a git repository
	// carried no labels at all and could not be found again by `sandbox-cli ps`.
	// A container nobody can list is one nobody can stop, and a killed sandbox-cli
	// leaves it running with the workspace still mounted.
	none, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if none.Labels["sandbox.cli"] != "1" {
		t.Errorf("every container must carry sandbox.cli so it can be found again, got %v", none.Labels)
	}
	for _, k := range []string{"sandbox.repo", "sandbox.branch", "sandbox.agent", "sandbox.base"} {
		if _, ok := none.Labels[k]; ok {
			t.Errorf("%s stamped with nothing to say: %v", k, none.Labels)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestBuildSpec_PublishFromFlagAndConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg()
	cfg.Ports = []string{"3000:3000"}

	spec, err := BuildSpec(cfg, Options{
		Project: dir,
		Publish: []string{"9229", "0.0.0.0:8080:80"},
		Command: []string{"sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1:3000:3000", "127.0.0.1:9229:9229", "0.0.0.0:8080:80"}
	if len(spec.Ports) != len(want) {
		t.Fatalf("Ports = %v, want %v", spec.Ports, want)
	}
	for i := range want {
		if spec.Ports[i] != want[i] {
			t.Errorf("Ports[%d] = %q, want %q", i, spec.Ports[i], want[i])
		}
	}
}

// TestBuildSpec_NoPublishByDefault: the container is unreachable from the host
// unless publishing was asked for. This is the inward half of the isolation
// story and belongs next to the mount invariants.
func TestBuildSpec_NoPublishByDefault(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Ports) != 0 {
		t.Errorf("expected no published ports by default, got %v", spec.Ports)
	}
}

func TestBuildSpec_PublishRejectsBadSpec(t *testing.T) {
	dir := t.TempDir()
	_, err := BuildSpec(baseCfg(), Options{Project: dir, Publish: []string{"not-a-port"}, Command: []string{"sh"}})
	if err == nil {
		t.Fatal("expected an error for a malformed port spec")
	}
	if !strings.Contains(err.Error(), "publish") {
		t.Errorf("error should name the flag, got %v", err)
	}
}

// A published port and `network: none` cannot both be honoured; say so instead
// of returning a container that looks configured and never answers.
func TestBuildSpec_PublishWithNetworkNoneFails(t *testing.T) {
	dir := t.TempDir()
	cfg := baseCfg()
	cfg.Network.Mode = "none"
	_, err := BuildSpec(cfg, Options{Project: dir, Publish: []string{"3000"}, Command: []string{"sh"}})
	if err == nil {
		t.Fatal("expected publishing under network:none to be refused")
	}
	if !strings.Contains(err.Error(), "none") {
		t.Errorf("error should mention the network mode, got %v", err)
	}
}

// The egress allowlist filters OUTPUT only, so publishing still works alongside
// it — and the two features must not quietly disable each other.
func TestBuildSpec_PublishWithAllowlist(t *testing.T) {
	dir := t.TempDir()
	spec, err := BuildSpec(baseCfg(), Options{
		Project: dir,
		Publish: []string{"3000"},
		Allow:   []string{"example.com"},
		Command: []string{"sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Ports) != 1 || spec.Ports[0] != "127.0.0.1:3000:3000" {
		t.Errorf("Ports = %v, want the published port kept under --allow", spec.Ports)
	}
	if spec.Network == "none" {
		t.Error("allowlist mode must keep bridge networking")
	}
}

// TestBuildSpec_NoHardeningWithAllowlistRefuses pins a combination that produced
// a container wider than either flag alone. --no-hardening is documented as
// reverting to the historical behavior, but the allowlist adds NET_ADMIN,
// NET_RAW, SETUID and SETGID and starts the container as root — and it is
// cap_drop ALL plus no-new-privileges that stop the guest keeping any of that
// after the drop to the sandbox user. Zeroing them left docker's full default
// capability set *plus* those four, with setuid binaries live again.
func TestBuildSpec_NoHardeningWithAllowlistRefuses(t *testing.T) {
	dir := t.TempDir()

	spec, err := BuildSpec(baseCfg(), Options{
		Project:     dir,
		NoHardening: true,
		Allow:       []string{"example.com"},
		Command:     []string{"sh"},
	})
	if err == nil {
		t.Fatalf("expected a refusal; got user=%q capAdd=%v capDrop=%v noNewPriv=%v",
			spec.User, spec.CapAdd, spec.CapDrop, spec.NoNewPrivileges)
	}
	// Config-driven allowlist reaches the same place.
	cfg := baseCfg()
	cfg.Network.Mode = "allowlist"
	if _, err := BuildSpec(cfg, Options{Project: dir, NoHardening: true, Command: []string{"sh"}}); err == nil {
		t.Error("network.mode: allowlist plus --no-hardening must be refused too")
	}

	// Each alone is unchanged: --no-hardening still drops the hardening...
	spec, err = BuildSpec(baseCfg(), Options{Project: dir, NoHardening: true, Command: []string{"sh"}})
	if err != nil {
		t.Fatalf("--no-hardening alone must still work: %v", err)
	}
	if spec.NoNewPrivileges || len(spec.CapDrop) != 0 {
		t.Errorf("--no-hardening alone should drop hardening: noNewPriv=%v capDrop=%v", spec.NoNewPrivileges, spec.CapDrop)
	}
	// ...and the allowlist alone still keeps it.
	spec, err = BuildSpec(baseCfg(), Options{Project: dir, Allow: []string{"example.com"}, Command: []string{"sh"}})
	if err != nil {
		t.Fatalf("--allow alone must still work: %v", err)
	}
	if !spec.NoNewPrivileges || !contains(spec.CapDrop, "ALL") {
		t.Errorf("--allow alone must keep the hardening: noNewPriv=%v capDrop=%v", spec.NoNewPrivileges, spec.CapDrop)
	}
}

// TestBuildSpec_WorkdirMovesTheMountOnlyWhenItLeavesIt covers a flag that used
// to produce a container the guest could not start in: --workdir set -w but left
// the project at /workspace, so the guest began in a directory that did not
// exist. Config `workdir:` moved both, so the two spellings of one setting also
// disagreed.
func TestBuildSpec_WorkdirMovesTheMountOnlyWhenItLeavesIt(t *testing.T) {
	dir := t.TempDir()

	// Outside the mount: the project comes with it, or there is nothing there.
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Workdir: "/app", Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Workdir != "/app" {
		t.Errorf("Workdir = %q, want /app", spec.Workdir)
	}
	if spec.Mounts[0].Target != "/app" {
		t.Errorf("workspace mounted at %q, want /app — the guest would start in an empty directory", spec.Mounts[0].Target)
	}

	// Inside the mount: starting in a subdirectory must not relocate the project.
	spec, err = BuildSpec(baseCfg(), Options{Project: dir, Workdir: "/workspace/sub", Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Mounts[0].Target != "/workspace" {
		t.Errorf("workspace moved to %q; a subdirectory workdir must keep the mount", spec.Mounts[0].Target)
	}
	if spec.Workdir != "/workspace/sub" {
		t.Errorf("Workdir = %q, want /workspace/sub", spec.Workdir)
	}
}

// TestBuildSpec_RefusesCommaInMountPaths covers a rendering hole: docker's
// --mount CSV reads a comma as the start of another option, so a directory named
// "a,b" produced source=/tmp/a,b,target=/data — with `b` as a field.
func TestBuildSpec_RefusesCommaInMountPaths(t *testing.T) {
	dir := t.TempDir()
	if _, err := BuildSpec(baseCfg(), Options{
		Project:     dir,
		ExtraMounts: []string{"/host/a,b:/data:ro"},
		Command:     []string{"sh"},
	}); err == nil {
		t.Error("a comma in a mount host path must be refused")
	}
	if err := ValidateMountTarget("/data,readonly"); err == nil {
		t.Error("a comma in a mount target must be refused")
	}
	if err := ValidateMountPath("host path", "/ordinary/path"); err != nil {
		t.Errorf("an ordinary path must pass: %v", err)
	}
}

// TestBuildSpec_RootWithAllowlistRefuses pins the second half of the pair that
// TestBuildSpec_NoHardeningWithAllowlistRefuses covers. The firewall starts the
// container as root and then drops — but the entrypoint skips the drop when the
// requested user resolves to uid 0, so the guest kept NET_ADMIN in its effective
// set and could `iptables -F OUTPUT` and reach anything. Confirmed by execution
// before the fix. An allowlist the guest can switch off is worse than none,
// because sandbox-cli reports it is enforcing one.
func TestBuildSpec_RootWithAllowlistRefuses(t *testing.T) {
	dir := t.TempDir()
	for _, user := range []string{"root", "0", "0:0"} {
		if _, err := BuildSpec(baseCfg(), Options{
			Project: dir, User: user, Allow: []string{"example.com"}, Command: []string{"sh"},
		}); err == nil {
			t.Errorf("--user %q with an allowlist must be refused", user)
		}
	}
	// Config-driven allowlist reaches the same place.
	cfg := baseCfg()
	cfg.Network.Mode = "allowlist"
	cfg.User = "root"
	if _, err := BuildSpec(cfg, Options{Project: dir, Command: []string{"sh"}}); err == nil {
		t.Error("user: root with network.mode: allowlist must be refused too")
	}

	// Each alone is unchanged: root without an allowlist is a supported choice...
	if _, err := BuildSpec(baseCfg(), Options{Project: dir, User: "root", Command: []string{"sh"}}); err != nil {
		t.Errorf("--user root alone must still work: %v", err)
	}
	// ...and the allowlist alone still runs as root for setup, dropping after.
	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Allow: []string{"example.com"}, Command: []string{"sh"}})
	if err != nil {
		t.Fatalf("--allow alone must still work: %v", err)
	}
	if spec.User != "root" || spec.Env["SANDBOX_RUN_AS"] != "sandbox" {
		t.Errorf("allowlist should start as root and drop to sandbox, got user=%q run_as=%q",
			spec.User, spec.Env["SANDBOX_RUN_AS"])
	}
}

// TestIsRootUser covers the spellings the entrypoint treats as root.
func TestIsRootUser(t *testing.T) {
	for _, u := range []string{"root", "0", "0:0", " root ", "0:1000"} {
		if !isRootUser(u) {
			t.Errorf("isRootUser(%q) = false, want true", u)
		}
	}
	for _, u := range []string{"sandbox", "1000", "1000:1000", ""} {
		if isRootUser(u) {
			t.Errorf("isRootUser(%q) = true, want false", u)
		}
	}
}

// TestBuildSpec_HooksAreMountedReadOnly pins the prevention half of the .git
// problem. An agent that writes .git/hooks/pre-commit is not editing the project
// — it is waiting for the user's next commit, which runs that file on the host as
// them. Confirmed as a live escape before this mount existed.
func TestBuildSpec_HooksAreMountedReadOnly(t *testing.T) {
	dir := t.TempDir()
	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}

	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	var found *runtime.Mount
	for i := range spec.Mounts {
		if spec.Mounts[i].Target == "/workspace/.git/hooks" {
			found = &spec.Mounts[i]
		}
	}
	if found == nil {
		t.Fatalf("no read-only mount over .git/hooks: %+v", spec.Mounts)
	}
	if !found.RO {
		t.Error("the hooks mount is writable, so a hook can still be planted")
	}
	// It has to land at the container path, not the host path — the workspace is
	// mounted at /workspace, so a host-path target would shadow nothing.
	// Compared against the symlink-resolved path: ResolveWorkspace resolves the
	// workspace (on macOS /var is /private/var), and the hooks mount is derived
	// from that resolved path so the two always agree.
	wantSrc, err := filepath.EvalSymlinks(hooks)
	if err != nil {
		wantSrc = hooks
	}
	if found.Source != wantSrc {
		t.Errorf("hooks mount source = %q, want %q", found.Source, wantSrc)
	}
	// And it must follow a relocated workspace rather than assuming /workspace.
	cfg := baseCfg()
	cfg.Workdir = "/app"
	spec, err = BuildSpec(cfg, Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	var moved bool
	for _, m := range spec.Mounts {
		if m.Target == "/app/.git/hooks" && m.RO {
			moved = true
		}
	}
	if !moved {
		t.Errorf("the hooks mount did not follow workdir: %+v", spec.Mounts)
	}
}

// TestBuildSpec_NoHooksMountWithoutAHooksDir pins that sandbox-cli does not
// invent one. Creating .git/hooks would be writing into the user's repository,
// which it does not do.
func TestBuildSpec_NoHooksMountWithoutAHooksDir(t *testing.T) {
	spec, err := BuildSpec(baseCfg(), Options{Project: t.TempDir(), Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range spec.Mounts {
		if strings.Contains(m.Target, ".git/hooks") {
			t.Errorf("mounted hooks for a non-repository: %+v", m)
		}
	}
}

// TestBuildSpec_HostNamesAreNeutralisedWithoutTheFlag pins that --host-gateway is
// a gate rather than a label.
//
// spec.go treated host.docker.internal as something the flag switched on. On
// Docker Desktop it resolves unconditionally, so it was never off: a sandbox with
// no flags read a file from a service bound to 127.0.0.1 on the host — bound
// there precisely so nothing else could reach it. Confirmed before this fix.
func TestBuildSpec_HostNamesAreNeutralisedWithoutTheFlag(t *testing.T) {
	dir := t.TempDir()

	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"host.docker.internal:127.0.0.1", "gateway.docker.internal:127.0.0.1"} {
		if !contains(spec.AddHosts, want) {
			t.Errorf("AddHosts = %v, want it to contain %q", spec.AddHosts, want)
		}
	}
	for _, h := range spec.AddHosts {
		if strings.Contains(h, "host-gateway") {
			t.Errorf("the gateway was mapped without --host-gateway: %q", h)
		}
	}

	// With the flag, the documented behaviour is unchanged — this is how an agent
	// reaches an MCP server running on the host.
	spec, err = BuildSpec(baseCfg(), Options{Project: dir, HostGateway: true, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(spec.AddHosts, "host.docker.internal:host-gateway") {
		t.Errorf("--host-gateway must map the gateway: %v", spec.AddHosts)
	}
	for _, h := range spec.AddHosts {
		if strings.HasSuffix(h, ":127.0.0.1") {
			t.Errorf("--host-gateway must not also neutralise the name: %q", h)
		}
	}
}

// TestBuildSpec_ExplicitAddHostWins guards against sandbox-cli overriding the
// user. /etc/hosts resolution takes the first match, so adding our own entry for
// a name the caller already mapped would silently discard what they asked for.
func TestBuildSpec_ExplicitAddHostWins(t *testing.T) {
	spec, err := BuildSpec(baseCfg(), Options{
		Project:  t.TempDir(),
		AddHosts: []string{"host.docker.internal:10.1.2.3"},
		Command:  []string{"sh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(spec.AddHosts, "host.docker.internal:10.1.2.3") {
		t.Errorf("the caller's own mapping was dropped: %v", spec.AddHosts)
	}
	if contains(spec.AddHosts, "host.docker.internal:127.0.0.1") {
		t.Errorf("sandbox-cli overrode an explicit --add-host: %v", spec.AddHosts)
	}
	// The name they did not map is still neutralised.
	if !contains(spec.AddHosts, "gateway.docker.internal:127.0.0.1") {
		t.Errorf("gateway.docker.internal should still be neutralised: %v", spec.AddHosts)
	}
}

// TestBuildSpec_JoinsTheIsolatedNetwork pins that sandboxes do not land on the
// default bridge, where every container can reach every other on any port — a
// peer container was confirmed reading workspace data out of a sandbox. Running
// several agents at once is the advertised workflow, so that meant a compromised
// agent in one repository could dial into the agent working on another.
func TestBuildSpec_JoinsTheIsolatedNetwork(t *testing.T) {
	dir := t.TempDir()

	spec, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Network != runtime.SandboxNetwork {
		t.Errorf("Network = %q, want %q — the default bridge is shared with everything",
			spec.Network, runtime.SandboxNetwork)
	}

	// The allowlist needs networking, and must get the isolated one too.
	spec, err = BuildSpec(baseCfg(), Options{Project: dir, Allow: []string{"x.example.com"}, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Network != runtime.SandboxNetwork {
		t.Errorf("allowlist mode Network = %q, want %q", spec.Network, runtime.SandboxNetwork)
	}

	// "none" still means none: asking for no network must not quietly get one.
	cfg := baseCfg()
	cfg.Network.Mode = "none"
	spec, err = BuildSpec(cfg, Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Network != "none" {
		t.Errorf("network: none produced %q", spec.Network)
	}
}

// TestPodmanMapsTheHostUserOntoTheWorkspace is the regression for a bug that
// only appears on the platform podman is most used on.
//
// It worked on macOS, where the podman machine VM virtualizes bind-mount
// ownership the way Docker Desktop does — so the first version of podman
// support looked complete. On native Linux rootless podman it did not: the host
// user maps to container uid 0, so the workspace appears root-owned and the
// sandbox user (1001) cannot write to it, and on SELinux-enforcing
// distributions the bind is denied outright so it cannot read it either.
// Measured on Fedora with SELinux enforcing: today's flags failed both, and
// relabelling alone fixed only the read.
func TestPodmanMapsTheHostUserOntoTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Engine = "podman"
	cfg.Network.Mode = "default" // keep this case about the mount, not the firewall

	spec, err := BuildSpec(cfg, Options{Project: dir, Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.HostUserMapping == "" {
		t.Fatal("podman spec carries no userns mapping; the workspace would be unwritable on Linux")
	}
	argv := strings.Join(runtime.BuildArgs(spec), " ")
	if !strings.Contains(argv, "--userns=keep-id:uid=1001,gid=1001") {
		t.Errorf("argv lacks the keep-id mapping:\n%s", argv)
	}
	if !strings.Contains(argv, "relabel=shared") {
		t.Errorf("argv lacks the SELinux relabel, so the mount is denied on Fedora/RHEL:\n%s", argv)
	}
	// Numeric user, so the *group* maps too. `--user sandbox` set the uid but
	// left the group at 0, and a file written into the workspace came back owned
	// by host-uid:subgid (501:100000) — the right user, a group the host has no
	// name for. `--user 1001:1001` lands it as the host's own uid:gid.
	if !strings.Contains(argv, "--user 1001:1001") {
		t.Errorf("podman user is not numeric uid:gid, so written files get a subgid:\n%s", argv)
	}
}

// Docker needs none of it — its daemon runs as root and Docker Desktop
// virtualizes ownership — and adding any of it would change the argv the golden
// --dry-run test pins.
func TestDockerGetsNoUserNamespaceRemapping(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Network.Mode = "default"

	spec, err := BuildSpec(cfg, Options{Project: dir, Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.HostUserMapping != "" {
		t.Errorf("docker spec carries a userns mapping: %q", spec.HostUserMapping)
	}
	argv := strings.Join(runtime.BuildArgs(spec), " ")
	for _, unwanted := range []string{"--userns", "relabel="} {
		if strings.Contains(argv, unwanted) {
			t.Errorf("docker argv contains %q, which it does not need:\n%s", unwanted, argv)
		}
	}
}

// An explicitly requested user is left exactly as written: the caller has said
// what they want, and rewriting it would be the surprise.
func TestPodmanLeavesAnExplicitUserAlone(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Engine = "podman"
	cfg.Network.Mode = "default"

	spec, err := BuildSpec(cfg, Options{Project: dir, User: "1234:5678", Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.User != "1234:5678" {
		t.Errorf("User = %q, want the caller's own value", spec.User)
	}
}

// The two labels a finished run cannot be asked about any other way.
//
// A container's capabilities and mounts say what it *got*, but not which profile
// it was launched under; and the prompt survives only inside an agent-specific
// argv, where reading it back means knowing which position holds it. Both are
// stamped for the same reason every other fact is: docker is the state store,
// and a fact not recorded is one no later command can recover.
func TestBuildSpecStampsProfileAndPrompt(t *testing.T) {
	cfg := config.Default()
	cfg.Profile = config.ProfileProd

	spec, err := BuildSpec(cfg, Options{
		Project: t.TempDir(),
		Agent:   "claude",
		Prompt:  "implement the login form",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Labels[LabelProfile]; got != config.ProfileProd {
		t.Errorf("%s = %q, want %q", LabelProfile, got, config.ProfileProd)
	}
	if got := spec.Labels[LabelPrompt]; got != "implement the login form" {
		t.Errorf("%s = %q", LabelPrompt, got)
	}
}

// A run that built its own argv has no prompt to record, and the label is then
// omitted rather than stamped blank — the rule every other label follows, so
// that a label which is present always carries a fact.
func TestBuildSpecOmitsAnEmptyPromptLabel(t *testing.T) {
	spec, err := BuildSpec(config.Default(), Options{
		Project: t.TempDir(),
		Command: []string{"npm", "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.Labels[LabelPrompt]; ok {
		t.Errorf("a run with no prompt must carry no %s label, got %q", LabelPrompt, spec.Labels[LabelPrompt])
	}
}

// A fleet prompt is routinely a page of instructions, and docker holds labels in
// the container config it writes on every inspect. Truncation is marked so no
// reader mistakes a prefix for the whole instruction.
func TestPromptLabelIsTruncatedAndSaysSo(t *testing.T) {
	long := strings.Repeat("a", maxPromptLabel*2)
	got := truncatePrompt(long)
	if len(got) > maxPromptLabel+len("…[truncated]") {
		t.Errorf("truncated prompt is %d bytes, want <= %d", len(got), maxPromptLabel+len("…[truncated]"))
	}
	if !strings.HasSuffix(got, "…[truncated]") {
		t.Errorf("a truncated prompt must say so, got %q", got[len(got)-20:])
	}
	// Short prompts pass through untouched.
	if got := truncatePrompt("do the thing"); got != "do the thing" {
		t.Errorf("short prompt was altered: %q", got)
	}
	// And the result stays valid UTF-8, since it becomes a JSON string.
	multibyte := strings.Repeat("é", maxPromptLabel)
	if !utf8.ValidString(truncatePrompt(multibyte)) {
		t.Error("truncation cut a rune in half")
	}
}
