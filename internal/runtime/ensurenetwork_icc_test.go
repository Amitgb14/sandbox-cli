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

	// Point "the network we own" at a scratch name: the real one normally has
	// running sandboxes attached, and this test creates and removes its subject.
	orig := ownedNetwork
	ownedNetwork = name
	defer func() { ownedNetwork = orig }()

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

// TestEnsureNetworkLeavesNetworksItDoesNotOwnAlone is the regression for a
// functional break the ICC check introduced.
//
// spec.Network only ever holds "none" or SandboxNetwork, and both reach
// EnsureNetwork. `none` is a *predefined* docker network: inspect succeeds and
// its Options map is empty, so an unconditional ICC check read that as "enabled"
// and refused the run — advising `docker network rm none`, which docker declines
// because the network is predefined. That broke the strictest posture the tool
// offers, and no test caught it because every `none` test stops at BuildArgs.
func TestEnsureNetworkLeavesNetworksItDoesNotOwnAlone(t *testing.T) {
	d := &DockerCLI{}
	ctx := context.Background()

	if err := d.EnsureNetwork(ctx, "none"); err != nil {
		t.Errorf("EnsureNetwork refused the predefined \"none\" network: %v", err)
	}
	if err := d.EnsureNetwork(ctx, "host"); err != nil {
		t.Errorf("EnsureNetwork refused the predefined \"host\" network: %v", err)
	}

	// A user-named network is theirs to configure, even with ICC on.
	const named = "sandbox-cli-user-named-net"
	exec.Command("docker", "network", "rm", named).Run()
	if out, err := exec.Command("docker", "network", "create", named).CombinedOutput(); err != nil {
		t.Skipf("cannot create a test network: %s", out)
	}
	defer exec.Command("docker", "network", "rm", named).Run()
	if err := d.EnsureNetwork(ctx, named); err != nil {
		t.Errorf("EnsureNetwork refused a network it did not create: %v", err)
	}
}
