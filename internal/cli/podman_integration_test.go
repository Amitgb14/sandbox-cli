//go:build podman_integration

// Podman end-to-end tests. Separate from docker_integration because they need a
// different engine present, and because a machine with one rarely has both:
//
//	go test -tags podman_integration ./internal/cli/
package cli

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// podmanImage builds the base image in podman's own store. Podman keeps images
// separately from docker, so the first run here rebuilds what looks like an
// image you already have — worth knowing, and worth the wait in CI.
func podmanImage(t *testing.T, d *runtime.DockerCLI) string {
	t.Helper()
	image.Register(d)
	ref := image.Ref()
	if err := d.EnsureImage(context.Background(), ref, false); err != nil {
		t.Skipf("cannot prepare the base image under podman: %v", err)
	}
	return ref
}

func podmanAvailable(t *testing.T) *runtime.DockerCLI {
	t.Helper()
	d := runtime.NewEngine("podman")
	if err := d.Available(context.Background()); err != nil {
		t.Skipf("podman not available: %v", err)
	}
	return d
}

// TestPodmanCanProgramTheEgressFirewall is the finding the whole design rests
// on, kept as a test so it cannot quietly stop being true.
//
// The open question in the issue was whether a *rootless* engine can program
// iptables from inside the container — if it cannot, the egress allowlist has no
// meaning under podman and needs a different design. It can, for every rule kind
// the entrypoint uses, so the firewall applies unchanged.
func TestPodmanCanProgramTheEgressFirewall(t *testing.T) {
	d := podmanAvailable(t)
	ref := podmanImage(t, d)
	if probe, reason := d.FirewallProgrammable(context.Background(), ref); probe != runtime.FirewallOK {
		t.Fatalf("rootless podman cannot program the firewall (%v): %s\n"+
			"if this is now true, the egress allowlist needs a podman-specific design", probe, reason)
	}
}

// TestPodmanInfoDialect covers the two questions whose answers are shaped
// differently under podman. Both used to fail with "can't evaluate field",
// which reads as "the daemon could not be asked" — and prod refuses on those,
// so every prod run on podman would have been refused for a reason that was not
// true.
func TestPodmanInfoDialect(t *testing.T) {
	d := podmanAvailable(t)
	ctx := context.Background()

	if _, known := d.SeccompUnavailable(ctx); !known {
		t.Error("podman's seccomp state could not be determined; prod refuses on unknowns")
	}
	names, err := d.Runtimes(ctx)
	if err != nil || len(names) == 0 {
		t.Fatalf("podman reported no OCI runtime: %v", err)
	}
	t.Logf("podman OCI runtime: %v", names)
}

// TestPodmanGivesEachSandboxItsOwnNetwork is the isolation property, and it is
// the one that needed a different mechanism rather than a different spelling:
// netavark has no enable_icc, and isolate=true leaves same-network peers
// reachable.
func TestPodmanGivesEachSandboxItsOwnNetwork(t *testing.T) {
	d := podmanAvailable(t)
	if !d.PerRunNetwork() {
		t.Fatal("podman must not share one network between sandboxes")
	}

	proj := t.TempDir()
	cfg := config.Default()
	cfg.Engine = "podman"
	sess := sandbox.New(cfg)

	name, err := sess.Start(context.Background(), sandbox.Options{
		Project: proj,
		Command: []string{"sleep", "20"},
	}, false)
	if err != nil {
		t.Fatalf("podman run did not start: %v", err)
	}
	// LIFO: the network must be removed *after* the container, or podman refuses
	// (it is still attached) and the test leaks the network it created.
	defer exec.Command("podman", "network", "rm", runtime.SandboxNetwork+"-"+name).Run()
	defer exec.Command("podman", "rm", "-f", name).Run()

	out, err := exec.Command("podman", "inspect", name,
		"--format", "{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(out))
	if !strings.HasPrefix(got, runtime.SandboxNetwork+"-") {
		t.Errorf("container joined %q; want a per-run network so no peer shares it", got)
	}
}
