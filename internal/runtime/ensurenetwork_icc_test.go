//go:build docker_integration

package runtime

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestEnsureNetworkRefusesAnICCEnabledNetwork is the regression for a silent
// fail-open: EnsureNetwork used to accept any network of the right name, so one
// created by hand, by an older build, or by a compose file was used as-is with
// inter-container communication left on — while the entire reason the network
// exists is enable_icc=false.
func TestEnsureNetworkRefusesAnICCEnabledNetwork(t *testing.T) {
	const name = "sandbox-cli-icc-regression"
	d := &DockerCLI{}
	ctx := context.Background()

	exec.Command("docker", "network", "rm", name).Run()
	// A network exactly as a hand-rolled or compose-created one would be: right
	// name, ICC left at its default (on).
	if out, err := exec.Command("docker", "network", "create", name).CombinedOutput(); err != nil {
		t.Skipf("cannot create a test network: %s", out)
	}
	defer exec.Command("docker", "network", "rm", name).Run()

	err := d.EnsureNetwork(ctx, name)
	if err == nil {
		t.Fatal("EnsureNetwork accepted a network with inter-container communication enabled")
	}
	if !strings.Contains(err.Error(), "inter-container communication") {
		t.Errorf("error does not explain the problem: %v", err)
	}

	// And the one it creates itself passes.
	exec.Command("docker", "network", "rm", name).Run()
	if err := d.EnsureNetwork(ctx, name); err != nil {
		t.Fatalf("EnsureNetwork rejected the network it created itself: %v", err)
	}
	if err := d.EnsureNetwork(ctx, name); err != nil {
		t.Fatalf("EnsureNetwork is not idempotent: %v", err)
	}
}
