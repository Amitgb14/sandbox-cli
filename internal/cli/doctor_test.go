package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/doctor"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// fakeHost stands in for a docker daemon so every combination can be exercised,
// including ones no single machine can produce.
type fakeHost struct {
	unavailable error
	seccompOff  bool
	seccompKnow bool
	runtimes    []string
	runtimesErr error
	firewall    runtime.FirewallProbe
	firewallWhy string
	imageThere  bool
	imageKnown  bool
	dns         runtime.DNSProbe
	dnsWhy      string
}

func (f fakeHost) Available(context.Context) error { return f.unavailable }
func (f fakeHost) SeccompUnavailable(context.Context) (bool, bool) {
	return f.seccompOff, f.seccompKnow
}
func (f fakeHost) Runtimes(context.Context) ([]string, error) { return f.runtimes, f.runtimesErr }
func (f fakeHost) FirewallProgrammable(context.Context, string) (runtime.FirewallProbe, string) {
	return f.firewall, f.firewallWhy
}
func (f fakeHost) ResolvesNames(context.Context, string) (runtime.DNSProbe, string) {
	return f.dns, f.dnsWhy
}
func (f fakeHost) ImagePresent(context.Context, string) (bool, bool) {
	return f.imageThere, f.imageKnown
}

func withHost(t *testing.T, h fakeHost) {
	t.Helper()
	orig := doctor.NewRuntime
	t.Cleanup(func() { doctor.NewRuntime = orig })
	doctor.NewRuntime = func(string) doctor.Runtime { return h }
}

// healthy is a host that satisfies everything.
func healthy() fakeHost {
	return fakeHost{
		seccompKnow: true,
		firewall:    runtime.FirewallOK,
		dns:         runtime.DNSOK,
		runtimes:    []string{"runc"},
		imageThere:  true,
		imageKnown:  true,
	}
}

// TestDoctorVerdictFollowsTheProfile is the whole point of the command: the same
// host passes dev and fails prod, because a control that cannot be satisfied is
// something a developer can act on and something an unattended run cannot.
func TestDoctorVerdictFollowsTheProfile(t *testing.T) {
	h := healthy()
	h.seccompOff = true // the condition this machine is actually in
	withHost(t, h)

	if err := reportDoctor(config.ProfileDev, runDoctorChecks(context.Background(), config.ProfileDev, "docker")); err != nil {
		t.Errorf("dev failed on a host it should merely warn about: %v", err)
	}
	err := reportDoctor(config.ProfileProd, runDoctorChecks(context.Background(), config.ProfileProd, "docker"))
	if err == nil {
		t.Fatal("prod accepted a host that applies no syscall filter")
	}
	if !strings.Contains(err.Error(), "seccomp") {
		t.Errorf("the refusal does not name the check that failed: %v", err)
	}
}

// A question that could not be asked is not the same as a satisfied one. prod
// does not get to assume the answer it would prefer.
func TestDoctorTreatsAnUnansweredQuestionAsFailureUnderProdOnly(t *testing.T) {
	h := healthy()
	h.seccompKnow = false // the daemon could not be asked
	withHost(t, h)

	if err := reportDoctor(config.ProfileDev, runDoctorChecks(context.Background(), config.ProfileDev, "docker")); err != nil {
		t.Errorf("dev failed on a question it merely could not ask: %v", err)
	}
	if err := reportDoctor(config.ProfileProd, runDoctorChecks(context.Background(), config.ProfileProd, "docker")); err == nil {
		t.Error("prod assumed seccomp was fine when the daemon could not be asked")
	}
}

// The check that changed meaning when the allowlist became the default: every
// run now needs NET_ADMIN, so a daemon that cannot grant it affects everybody.
func TestDoctorFailsProdWhenTheFirewallCannotBeProgrammed(t *testing.T) {
	h := healthy()
	h.firewall = runtime.FirewallBlocked
	h.firewallWhy = "operation not permitted"
	withHost(t, h)

	checks := runDoctorChecks(context.Background(), config.ProfileProd, "docker")
	if err := reportDoctor(config.ProfileProd, checks); err == nil {
		t.Fatal("prod accepted a host where the egress firewall cannot be programmed")
	}
	// And it must say what to do, since --network default is the way out.
	var found bool
	for _, c := range checks {
		if c.Name == "egress firewall" && strings.Contains(c.Remedy, "--network default") {
			found = true
		}
	}
	if !found {
		t.Error("the firewall check does not name the escape hatch")
	}
}

// An unbuilt image is "cannot tell", not "broken" — dev must not warn about it
// as though the host were at fault.
func TestDoctorReportsAnUnbuiltImageAsUnknown(t *testing.T) {
	h := healthy()
	// The typed outcome, not a substring of the reason: classification used to
	// hinge on strings.Contains against a literal from another package, so a
	// reworded message would silently have become a prod failure.
	h.firewall = runtime.FirewallUnknown
	h.firewallWhy = "the base image is not built yet"
	withHost(t, h)

	for _, c := range runDoctorChecks(context.Background(), config.ProfileDev, "docker") {
		if c.Name == "egress firewall" && c.Status != doctor.StatusUnknown {
			t.Errorf("an unbuilt image was reported as status %v, want unknown", c.Status)
		}
	}
}

// TestDoctorReportsAMissingBaseImageWithoutFailingAnything.
//
// A machine that has not built the image yet is not broken — the first run
// builds it, which is the design. What the reader wants is the heads-up that
// their first run will take a few minutes, so the check reports it as ok and
// says so, and prod does not refuse over it.
func TestDoctorReportsAMissingBaseImageWithoutFailingAnything(t *testing.T) {
	h := healthy()
	h.imageThere = false
	withHost(t, h)

	checks := runDoctorChecks(context.Background(), config.ProfileProd, "docker")
	var saw bool
	for _, c := range checks {
		if c.Name != "base image" {
			continue
		}
		saw = true
		if c.Status != doctor.StatusOK {
			t.Errorf("an unbuilt base image was reported as status %v, want ok", c.Status)
		}
		if !strings.Contains(c.Detail, "not built yet") {
			t.Errorf("the detail should say the first run will build it: %q", c.Detail)
		}
	}
	if !saw {
		t.Fatal("no base image check ran")
	}
	if err := reportDoctor(config.ProfileProd, checks); err != nil {
		t.Errorf("prod refused a host whose only gap was an unbuilt image: %v", err)
	}
}

// TestDoctorCannotTellWhetherTheImageIsThere: absent and unreachable are
// different answers, and prod does not get to assume the one it would prefer.
func TestDoctorCannotTellWhetherTheImageIsThere(t *testing.T) {
	h := healthy()
	h.imageThere, h.imageKnown = false, false
	withHost(t, h)

	checks := runDoctorChecks(context.Background(), config.ProfileProd, "docker")
	if err := reportDoctor(config.ProfileProd, checks); err == nil {
		t.Error("prod should refuse a question that could not be asked")
	}
	if err := reportDoctor(config.ProfileDev, checks); err != nil {
		t.Errorf("dev should stay quiet about a question it could not ask: %v", err)
	}
}

// A stronger runtime is reported when present, and its absence is *not* a
// failure even under prod — sandbox-cli does not yet select one, and failing a
// check for something the tool does not do would be theatre.
func TestDoctorReportsStrongerRuntimesWithoutRequiringThem(t *testing.T) {
	h := healthy()
	h.runtimes = []string{"runc", "runsc"}
	withHost(t, h)
	var saw bool
	for _, c := range runDoctorChecks(context.Background(), config.ProfileProd, "docker") {
		if c.Name == "isolation runtime" {
			saw = true
			if c.Status != doctor.StatusOK || !strings.Contains(c.Detail, "runsc") {
				t.Errorf("runsc present but not reported: %+v", c)
			}
		}
	}
	if !saw {
		t.Fatal("no isolation runtime check ran")
	}

	h.runtimes = []string{"runc"}
	withHost(t, h)
	if err := reportDoctor(config.ProfileProd, runDoctorChecks(context.Background(), config.ProfileProd, "docker")); err != nil {
		t.Errorf("prod failed for a runtime sandbox-cli does not yet select: %v", err)
	}
}

// Every containerd-backed daemon reports runc as io.containerd.runc.v2 — the
// Rocky Linux 10.2 host this repo's own comments cite, and every modern Docker
// install. Classifying that as unrecognised told the reader plain runc "may
// still be stronger", which is the one direction nothing here may fail in.
func TestDoctorDoesNotCallShimSpelledRuncStronger(t *testing.T) {
	h := healthy()
	h.runtimes = []string{"io.containerd.runc.v2", "runc"}
	withHost(t, h)
	for _, c := range runDoctorChecks(context.Background(), config.ProfileDev, "docker") {
		if c.Name != "isolation runtime" {
			continue
		}
		if strings.Contains(c.Detail, "may still be stronger") {
			t.Errorf("the shim spelling of runc was reported as possibly stronger: %q", c.Detail)
		}
	}
}

// An unrecognised runtime is a different case and must still be named, rather
// than folded into "only the default runtime is registered" — that sentence
// would be false, and a reader concludes the runtime is absent.
func TestDoctorNamesAnUnrecognisedRuntimeWithoutVouchingForIt(t *testing.T) {
	h := healthy()
	h.runtimes = []string{"io.containerd.runc.v2", "kata-runtime"}
	withHost(t, h)
	var saw bool
	for _, c := range runDoctorChecks(context.Background(), config.ProfileDev, "docker") {
		if c.Name != "isolation runtime" {
			continue
		}
		saw = true
		if strings.Contains(c.Detail, "only the default runtime") {
			t.Errorf("an unrecognised runtime was reported as the default: %q", c.Detail)
		}
		if !strings.Contains(c.Detail, "kata-runtime") {
			t.Errorf("the unrecognised runtime is not named: %q", c.Detail)
		}
		// Named, and still not characterised as a boundary.
		if strings.Contains(c.Detail, "stronger isolation available") {
			t.Errorf("a name that does not say its hypervisor was vouched for: %q", c.Detail)
		}
	}
	if !saw {
		t.Fatal("no isolation runtime check ran")
	}
}

// With no daemon there is one fact worth printing, not six unknowns.
func TestDoctorSaysOneThingWhenDockerIsAbsent(t *testing.T) {
	withHost(t, fakeHost{unavailable: errors.New("cannot reach the docker daemon")})
	checks := runDoctorChecks(context.Background(), config.ProfileDev, "docker")
	if len(checks) != 1 || checks[0].Name != "docker daemon" {
		t.Errorf("expected a single docker-daemon finding, got %d checks", len(checks))
	}
}

// TestInitScaffoldsAConfigThatLoads is the regression for a break that made
// every sandbox-cli command fail in a scaffolded project.
//
// The template set `network: mode: default`, which matched the built-in default
// and was therefore a harmless no-op. When egress became default-denied it
// turned into a *weakening*, and a project config may not weaken — so every
// command in any directory `sandbox-cli init` had ever touched failed hard,
// including read-only ones like `config` and `doctor`.
//
// The scaffold is the one project config the tool writes itself, so it has to
// load.
func TestInitScaffoldsAConfigThatLoads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, ".sandbox.yaml"), []byte(scaffoldConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(dir, ""); err != nil {
		t.Fatalf("the config `sandbox-cli init` writes is refused by the trust rules: %v", err)
	}
}

// TestInitScaffoldsInstructionsThatWork covers what the test above cannot: the
// scaffold as written has no uncommented keys, so loading it proves almost
// nothing. What matters is whether following its instructions produces the
// setting the user asked for.
//
// It did not. The parent `network:` key was commented out while its children
// were left at the old indentation, so uncommenting the child alone yielded a
// stray top-level key — and yaml.Unmarshal drops unknown keys without
// complaining, so a user asking to *tighten* to `none` silently got the
// allowlist instead. Silently discarding a request for more confinement is the
// wrong direction for this file to fail in.
func TestInitScaffoldsInstructionsThatWork(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// The structural property that makes the instruction followable: the child
	// must be indented *under* the commented parent, so uncommenting both yields
	// valid YAML. It previously sat at the parent's own indentation, which meant
	// uncommenting produced a stray top-level `mode:` — and yaml.Unmarshal drops
	// unknown keys silently, so a request to tighten to `none` became the
	// allowlist with no error at all.
	if !strings.Contains(scaffoldConfig, "\n# network:\n#   mode: none") {
		t.Error("the commented `mode:` is not nested under the commented `network:`; " +
			"uncommenting as instructed would produce a stray top-level key that is silently dropped")
	}

	// And following the instruction really produces the setting.
	strict := uncomment(scaffoldConfig, "# network:", "#   mode: none")
	if err := os.WriteFile(filepath.Join(dir, ".sandbox.yaml"), []byte(strict), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir, "")
	if err != nil {
		t.Fatalf("the scaffold's own stricter variant does not load: %v", err)
	}
	if cfg.Network.Mode != "none" {
		t.Errorf("network.mode = %q, want none — the tightening the scaffold documents was dropped", cfg.Network.Mode)
	}
}

// TestEverySuggestionInTheScaffoldIsAcceptable generalises the principle the
// test above applies to one key: following the scaffold's instructions must
// produce what the user asked for.
//
// The live section offered three keys a project config may not set —
// network.allow, ports and snapshot — so uncommenting any of them made every
// sandbox-cli command in the directory fail. The snapshot one was the sharpest:
// the key is refused because "interval: 1ms" turns the host into a `git add -A`
// loop, and the scaffold advertised "interval: 2m" two screens below the header
// listing it as refused.
//
// This walks the live section rather than naming keys, so the next key added to
// the refused list is caught even if nobody remembers this file exists. Anything
// below the "For reference" marker is deliberately excluded: that block exists to
// show what belongs elsewhere.
func TestEverySuggestionInTheScaffoldIsAcceptable(t *testing.T) {
	live, _, found := strings.Cut(scaffoldConfig, "# For reference")
	if !found {
		t.Fatal("the scaffold has no reference block; this test cannot tell live suggestions from examples")
	}
	for _, block := range topLevelSuggestions(live) {
		t.Run(block.key, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			if err := os.WriteFile(filepath.Join(dir, ".sandbox.yaml"), []byte(block.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(dir, ""); err != nil {
				t.Errorf("the scaffold offers %q, but uncommenting it breaks every command here:\n%v\n\n%s",
					block.key, err, block.yaml)
			}
		})
	}
}

// suggestion is one uncommentable top-level block from the scaffold.
type suggestion struct {
	key  string
	yaml string
}

// topLevelSuggestions finds each commented top-level key in the live section and
// returns it uncommented, with its indented children.
//
// Comment-only lines inside a block (the long explanatory notes) are dropped:
// they are prose, not settings, and a user uncommenting a block would not
// uncomment those. Values whose sample text is a placeholder are left alone —
// the question here is whether the *key* is permitted, not whether the example
// value parses to something meaningful.
func topLevelSuggestions(live string) []suggestion {
	var out []suggestion
	lines := strings.Split(live, "\n")
	for i := 0; i < len(lines); i++ {
		key, ok := commentedTopLevelKey(lines[i])
		if !ok {
			continue
		}
		body := []string{strings.TrimPrefix(lines[i], "# ")}
		for j := i + 1; j < len(lines); j++ {
			child := lines[j]
			if !strings.HasPrefix(child, "#   ") && !strings.HasPrefix(child, "#     ") {
				break
			}
			if uncommented := strings.TrimPrefix(child, "# "); strings.Contains(uncommented, ":") ||
				strings.HasPrefix(strings.TrimSpace(uncommented), "-") {
				body = append(body, uncommented)
			}
		}
		if len(body) > 1 || strings.Contains(body[0], ": ") {
			out = append(out, suggestion{key: key, yaml: strings.Join(body, "\n") + "\n"})
		}
	}
	return out
}

// commentedTopLevelKey matches `# key:` or `# key: value` at column zero.
func commentedTopLevelKey(line string) (string, bool) {
	if !strings.HasPrefix(line, "# ") {
		return "", false
	}
	rest := strings.TrimPrefix(line, "# ")
	if rest == "" || rest[0] == ' ' || rest[0] == '#' {
		return "", false
	}
	key, _, ok := strings.Cut(rest, ":")
	if !ok || strings.ContainsAny(key, " \t") {
		return "", false
	}
	return key, true
}

// uncomment strips the leading "# " from the named lines, which is what a user
// does by hand.
func uncomment(doc string, lines ...string) string {
	out := doc
	for _, l := range lines {
		out = strings.Replace(out, l, strings.Replace(l, "# ", "", 1), 1)
	}
	return out
}

// The runtime check is an "ok" that still has something to say under prod, and
// its remedy used to be dropped because remedies only printed for non-OK checks
// — so the actionable half never reached the screen.
func TestDoctorPrintsTheRuntimeRemedyEvenThoughTheCheckPasses(t *testing.T) {
	h := healthy()
	h.runtimes = []string{"runc"}
	withHost(t, h)

	var c check
	for _, got := range runDoctorChecks(context.Background(), config.ProfileProd, "docker") {
		if got.Name == "isolation runtime" {
			c = got
		}
	}
	if c.Remedy == "" {
		t.Fatal("prod says nothing about the missing stronger runtime")
	}
	if c.Status != doctor.StatusOK {
		t.Errorf("status = %v; the runtime gap is reported, not failed", c.Status)
	}
	// A newline inside the detail would end the tabwriter column block, so a
	// check added after this one would silently misalign.
	if strings.Contains(c.Detail, "\n") {
		t.Error("detail contains a newline, which breaks the tabwriter column block")
	}
}

// A probe that timed out answered nothing, so it must not read as "this host
// cannot program the firewall" — that would fail prod for a question never
// asked, against the command's own rule.
func TestDoctorTreatsATimedOutProbeAsUnknown(t *testing.T) {
	h := healthy()
	h.firewall = runtime.FirewallUnknown
	h.firewallWhy = "the probe timed out"
	withHost(t, h)

	for _, c := range runDoctorChecks(context.Background(), config.ProfileDev, "docker") {
		if c.Name == "egress firewall" && c.Status != doctor.StatusUnknown {
			t.Errorf("a timed-out probe was reported as %v, want unknown", c.Status)
		}
	}
}

// TestDoctorSeparatesABrokenSandboxResolverFromNoDNSAtAll.
//
// The distinction is the whole value of the check. sandbox-cli *chooses* the
// network a sandbox runs on — under podman a per-run one, which resolves through
// aardvark-dns where the default rootless network does not — so a resolver
// broken there is the tool's own doing and has a remedy. A host that cannot
// resolve anywhere would fail whatever sandbox-cli did, and telling that user to
// reload podman's networks sends them after the wrong thing.
//
// The symptom this exists to shorten was an agent hanging at login with
// `getaddrinfo ETIMEOUT`, four layers from the cause.
func TestDoctorSeparatesABrokenSandboxResolverFromNoDNSAtAll(t *testing.T) {
	broken := healthy()
	broken.dns, broken.dnsWhy = runtime.DNSSandboxBroken, "could not resolve registry.npmjs.org"
	withHost(t, broken)
	c := findCheck(t, runDoctorChecks(context.Background(), config.ProfileDev, "docker"), "container DNS")
	if c.Status != doctor.StatusWeak {
		t.Errorf("status = %v, want a warning", c.Status)
	}
	if !strings.Contains(c.Remedy, "podman network reload") {
		t.Errorf("a sandbox-network resolver failure should name its remedy, got %q", c.Remedy)
	}

	none := healthy()
	none.dns, none.dnsWhy = runtime.DNSNoResolver, "could not resolve registry.npmjs.org"
	withHost(t, none)
	c = findCheck(t, runDoctorChecks(context.Background(), config.ProfileDev, "docker"), "container DNS")
	if strings.Contains(c.Remedy, "podman network reload") {
		t.Errorf("a host with no DNS must not be sent to reload podman's networks: %q", c.Remedy)
	}
	if !strings.Contains(c.Detail, "no container on this host") {
		t.Errorf("the detail should say the host cannot resolve at all, got %q", c.Detail)
	}
}

// TestDoctorFailsProdWhenTheSandboxNetworkCannotResolve: same asymmetry as every
// other check — a developer can act on a warning, an unattended run cannot.
func TestDoctorFailsProdWhenTheSandboxNetworkCannotResolve(t *testing.T) {
	h := healthy()
	h.dns, h.dnsWhy = runtime.DNSSandboxBroken, "could not resolve registry.npmjs.org"
	withHost(t, h)

	if err := reportDoctor(config.ProfileDev, runDoctorChecks(context.Background(), config.ProfileDev, "docker")); err != nil {
		t.Errorf("dev must warn and pass: %v", err)
	}
	if err := reportDoctor(config.ProfileProd, runDoctorChecks(context.Background(), config.ProfileProd, "docker")); err == nil {
		t.Error("prod must refuse a host whose sandbox network cannot resolve names")
	}
}

// findCheck fails rather than returning a zero Check, so a renamed check shows up
// as a missing check instead of a silently passing assertion.
func findCheck(t *testing.T, checks []doctor.Check, name string) doctor.Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q check in %v", name, checks)
	return doctor.Check{}
}
