//go:build docker_integration

package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// probeImage returns the base image, building it if this is the first run: the
// probe needs a real image with iptables in it.
func probeImage(t *testing.T, d *runtime.DockerCLI) string {
	t.Helper()
	image.Register(d)
	ref := image.Ref()
	if err := d.EnsureImage(context.Background(), ref, false); err != nil {
		t.Skipf("cannot prepare the base image: %v", err)
	}
	return ref
}

// TestFirewallProbeExercisesWhatTheEntrypointNeeds covers the one function in
// the doctor work that actually touches docker, and had no test.
//
// It must probe by *writing*. `iptables -L` only proves the table can be read,
// and a host that lists rules perfectly well can still lack the nat table,
// xt_owner or conntrack — all of which the real entrypoint uses. A read-only
// probe that passes and a run that then fails is precisely the 3am failure
// doctor exists to prevent.
func TestFirewallProbeExercisesWhatTheEntrypointNeeds(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	d := runtime.NewDockerCLI()
	ref := probeImage(t, d)

	probe, reason := d.FirewallProgrammable(context.Background(), ref)
	if probe != runtime.FirewallOK {
		t.Fatalf("probe = %v (%s); this daemon can run the allowlist, so it should pass", probe, reason)
	}
}

// An image that is not there is "cannot tell", never "this host is broken" —
// the distinction prod depends on, since it fails on unknowns.
func TestFirewallProbeReportsAMissingImageAsUnknown(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	d := runtime.NewDockerCLI()
	probe, reason := d.FirewallProgrammable(context.Background(), "sandbox-cli-no-such-image:0")
	if probe != runtime.FirewallUnknown {
		t.Errorf("probe = %v (%s), want FirewallUnknown", probe, reason)
	}
	if probe, _ := d.FirewallProgrammable(context.Background(), ""); probe != runtime.FirewallUnknown {
		t.Errorf("an empty image ref = %v, want FirewallUnknown", probe)
	}
}

// A probe that ran out of time answered nothing. Classifying it as Blocked would
// fail prod for a question never asked.
func TestFirewallProbeReportsATimeoutAsUnknown(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	d := runtime.NewDockerCLI()
	ref := probeImage(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	probe, reason := d.FirewallProgrammable(ctx, ref)
	if probe == runtime.FirewallBlocked {
		t.Errorf("a timed-out probe reported the host as blocked: %s", reason)
	}
}

// Runtimes must at least report the runtime the daemon is using.
func TestRuntimesListsTheDaemonsRuntimes(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	names, err := runtime.NewDockerCLI().Runtimes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("no runtimes reported")
	}
	var sawRunc bool
	for _, n := range names {
		if strings.Contains(n, "runc") {
			sawRunc = true
		}
	}
	if !sawRunc {
		t.Errorf("runc not among the reported runtimes: %v", names)
	}
}
