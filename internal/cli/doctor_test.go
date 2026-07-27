package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
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
}

func (f fakeHost) Available(context.Context) error { return f.unavailable }
func (f fakeHost) SeccompUnavailable(context.Context) (bool, bool) {
	return f.seccompOff, f.seccompKnow
}
func (f fakeHost) Runtimes(context.Context) ([]string, error) { return f.runtimes, f.runtimesErr }
func (f fakeHost) FirewallProgrammable(context.Context, string) (runtime.FirewallProbe, string) {
	return f.firewall, f.firewallWhy
}

func withHost(t *testing.T, h fakeHost) {
	t.Helper()
	orig := newDoctorRuntime
	t.Cleanup(func() { newDoctorRuntime = orig })
	newDoctorRuntime = func() doctorRuntime { return h }
}

// healthy is a host that satisfies everything.
func healthy() fakeHost {
	return fakeHost{seccompKnow: true, firewall: runtime.FirewallOK, runtimes: []string{"runc"}}
}

// TestDoctorVerdictFollowsTheProfile is the whole point of the command: the same
// host passes dev and fails prod, because a control that cannot be satisfied is
// something a developer can act on and something an unattended run cannot.
func TestDoctorVerdictFollowsTheProfile(t *testing.T) {
	h := healthy()
	h.seccompOff = true // the condition this machine is actually in
	withHost(t, h)

	if err := reportDoctor(config.ProfileDev, runDoctorChecks(context.Background(), config.ProfileDev)); err != nil {
		t.Errorf("dev failed on a host it should merely warn about: %v", err)
	}
	err := reportDoctor(config.ProfileProd, runDoctorChecks(context.Background(), config.ProfileProd))
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

	if err := reportDoctor(config.ProfileDev, runDoctorChecks(context.Background(), config.ProfileDev)); err != nil {
		t.Errorf("dev failed on a question it merely could not ask: %v", err)
	}
	if err := reportDoctor(config.ProfileProd, runDoctorChecks(context.Background(), config.ProfileProd)); err == nil {
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

	checks := runDoctorChecks(context.Background(), config.ProfileProd)
	if err := reportDoctor(config.ProfileProd, checks); err == nil {
		t.Fatal("prod accepted a host where the egress firewall cannot be programmed")
	}
	// And it must say what to do, since --network default is the way out.
	var found bool
	for _, c := range checks {
		if c.name == "egress firewall" && strings.Contains(c.remedy, "--network default") {
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

	for _, c := range runDoctorChecks(context.Background(), config.ProfileDev) {
		if c.name == "egress firewall" && c.status != statusUnknown {
			t.Errorf("an unbuilt image was reported as status %v, want unknown", c.status)
		}
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
	for _, c := range runDoctorChecks(context.Background(), config.ProfileProd) {
		if c.name == "isolation runtime" {
			saw = true
			if c.status != statusOK || !strings.Contains(c.detail, "runsc") {
				t.Errorf("runsc present but not reported: %+v", c)
			}
		}
	}
	if !saw {
		t.Fatal("no isolation runtime check ran")
	}

	h.runtimes = []string{"runc"}
	withHost(t, h)
	if err := reportDoctor(config.ProfileProd, runDoctorChecks(context.Background(), config.ProfileProd)); err != nil {
		t.Errorf("prod failed for a runtime sandbox-cli does not yet select: %v", err)
	}
}

// With no daemon there is one fact worth printing, not six unknowns.
func TestDoctorSaysOneThingWhenDockerIsAbsent(t *testing.T) {
	withHost(t, fakeHost{unavailable: errors.New("cannot reach the docker daemon")})
	checks := runDoctorChecks(context.Background(), config.ProfileDev)
	if len(checks) != 1 || checks[0].name != "docker daemon" {
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

// The runtime check is an "ok" that still has something to say under prod, and
// its remedy used to be dropped because remedies only printed for non-OK checks
// — so the actionable half never reached the screen.
func TestDoctorPrintsTheRuntimeRemedyEvenThoughTheCheckPasses(t *testing.T) {
	h := healthy()
	h.runtimes = []string{"runc"}
	withHost(t, h)

	var c check
	for _, got := range runDoctorChecks(context.Background(), config.ProfileProd) {
		if got.name == "isolation runtime" {
			c = got
		}
	}
	if c.remedy == "" {
		t.Fatal("prod says nothing about the missing stronger runtime")
	}
	if c.status != statusOK {
		t.Errorf("status = %v; the runtime gap is reported, not failed", c.status)
	}
	// A newline inside the detail would end the tabwriter column block, so a
	// check added after this one would silently misalign.
	if strings.Contains(c.detail, "\n") {
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

	for _, c := range runDoctorChecks(context.Background(), config.ProfileDev) {
		if c.name == "egress firewall" && c.status != statusUnknown {
			t.Errorf("a timed-out probe was reported as %v, want unknown", c.status)
		}
	}
}
