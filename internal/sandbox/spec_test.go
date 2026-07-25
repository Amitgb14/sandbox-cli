package sandbox

import (
	"os"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

func baseCfg() config.Config {
	c := config.Default()
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
	if !contains(spec.CapAdd, "NET_ADMIN") {
		t.Errorf("CapAdd missing NET_ADMIN: %v", spec.CapAdd)
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

func TestBuildSpec_NoEgressByDefault(t *testing.T) {
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
	if contains(spec.CapAdd, "NET_ADMIN") {
		t.Errorf("unexpected NET_ADMIN without egress: %v", spec.CapAdd)
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

func TestInjectSecrets_SetsEnvFromSources(t *testing.T) {
	t.Setenv("SRC_ENV_SECRET", "topsecret")
	// Register cleanup for the target var so the test doesn't leak process env.
	t.Setenv("BROKERED_TOKEN", "placeholder")

	cfg := config.Default()
	cfg.Secrets = map[string]config.SecretSpec{"BROKERED_TOKEN": {Env: "SRC_ENV_SECRET"}}
	if err := injectSecrets(cfg, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("BROKERED_TOKEN"); got != "topsecret" {
		t.Errorf("injectSecrets set BROKERED_TOKEN=%q, want topsecret", got)
	}

	// A --secret flag with env: source also resolves.
	t.Setenv("FLAG_TOKEN", "placeholder")
	if err := injectSecrets(config.Default(), Options{Secrets: []string{"FLAG_TOKEN=env:SRC_ENV_SECRET"}}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("FLAG_TOKEN"); got != "topsecret" {
		t.Errorf("injectSecrets(flag) set FLAG_TOKEN=%q, want topsecret", got)
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
	// None by default.
	bare, _ := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if len(bare.AddHosts) != 0 {
		t.Errorf("unexpected AddHosts by default: %v", bare.AddHosts)
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
	none, err := BuildSpec(baseCfg(), Options{Project: dir, Command: []string{"sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Labels) != 0 {
		t.Errorf("expected no labels with nothing to stamp, got %v", none.Labels)
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
