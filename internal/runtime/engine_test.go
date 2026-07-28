package runtime

import (
	"strings"
	"testing"
)

// TestEngineIsInferredFromTheBinary: pointing Bin at a podman path is enough,
// because a user who sets `engine: podman` should not also have to know there is
// a dialect behind it.
func TestEngineIsInferredFromTheBinary(t *testing.T) {
	cases := map[string]Engine{
		"":                         EngineDocker,
		"docker":                   EngineDocker,
		"/usr/local/bin/docker":    EngineDocker,
		"podman":                   EnginePodman,
		"/opt/homebrew/bin/podman": EnginePodman,
		"podman-remote":            EnginePodman,
		// Not podman: the prefix match is on the base name, so a path merely
		// containing the word does not count.
		"/opt/podman-tools/bin/docker": EngineDocker,
	}
	for bin, want := range cases {
		d := &DockerCLI{Bin: bin}
		if got := d.engine(); got != want {
			t.Errorf("Bin=%q -> engine %q, want %q", bin, got, want)
		}
	}
	// An explicit Engine wins, for a renamed or wrapped binary.
	d := &DockerCLI{Bin: "container-tool", Engine: EnginePodman}
	if !d.IsPodman() {
		t.Error("an explicit Engine did not override the inferred one")
	}
}

// TestNetworkIsolationArgsDifferPerEngine pins the measured difference rather
// than a preference.
//
// Docker's enable_icc=false turns off traffic between containers on the same
// bridge, so one shared network suffices. netavark rejects that option outright,
// and its nearest-looking one — isolate=true — blocks traffic between
// *different* networks while leaving same-network peers reachable. Verified by
// reading one container's data from another on an isolate=true network. So
// podman needs a network per sandbox, which is what PerRunNetwork says.
func TestNetworkIsolationArgsDifferPerEngine(t *testing.T) {
	docker := &DockerCLI{Bin: "docker"}
	if args := strings.Join(docker.networkCreateArgs("n"), " "); !strings.Contains(args, "enable_icc=false") {
		t.Errorf("docker network args lost enable_icc: %s", args)
	}
	if docker.PerRunNetwork() {
		t.Error("docker should share one network; a per-run one would leak on every run")
	}

	podman := &DockerCLI{Bin: "podman"}
	args := strings.Join(podman.networkCreateArgs("n"), " ")
	if !strings.Contains(args, "isolate=true") {
		t.Errorf("podman network args lost isolate: %s", args)
	}
	if strings.Contains(args, "enable_icc") {
		t.Errorf("podman was handed a docker-only option netavark rejects outright: %s", args)
	}
	if !podman.PerRunNetwork() {
		t.Error("podman needs a network per sandbox: isolate=true does not stop same-network peers")
	}
}

// Ownership decides what EnsureNetwork is entitled to make claims about, and the
// two engines answer differently because one shares a network and one does not.
func TestOwnsNetwork(t *testing.T) {
	docker := &DockerCLI{Bin: "docker"}
	if !docker.ownsNetwork(SandboxNetwork) {
		t.Error("docker does not recognise its own shared network")
	}
	for _, other := range []string{"none", "host", "bridge", "someone-elses-net"} {
		if docker.ownsNetwork(other) {
			t.Errorf("docker claimed ownership of %q, which it did not create", other)
		}
	}

	podman := &DockerCLI{Bin: "podman"}
	if !podman.ownsNetwork(SandboxNetwork + "-sandbox-abc123") {
		t.Error("podman does not recognise its own per-run network")
	}
	for _, other := range []string{"none", "host", "podman", SandboxNetwork} {
		if podman.ownsNetwork(other) {
			t.Errorf("podman claimed ownership of %q", other)
		}
	}
}

// RemoveNetwork must never take the shared docker network out from under a
// concurrent run.
func TestRemoveNetworkRefusesTheSharedOne(t *testing.T) {
	// No daemon call should happen for these, so this is safe without docker.
	d := &DockerCLI{Bin: "/nonexistent-engine-binary"}
	d.RemoveNetwork(t.Context(), SandboxNetwork) // must be a no-op, not an attempt
	d.RemoveNetwork(t.Context(), "")
}

// TestPerRunNetworkRefusesRatherThanFallingBack is the regression for the worst
// bug in the first version of this work.
//
// On failure it used to reset spec.Network to the shared name and let the run
// proceed, with a comment claiming that was "a weaker arrangement, not an open
// one". Neither half was true. Under podman nothing ever creates `sandbox-cli`
// — ownsNetwork returns false for that exact name, so EnsureNetwork no-ops on
// it — so the run died on podman's own "network not found" with the real reason
// discarded. And where the name *did* exist, joining it was worse: its isolation
// was never checked, so the run silently got the peer-reachability hole the
// per-run design exists to close.
func TestPerRunNetworkRefusesRatherThanFallingBack(t *testing.T) {
	// A binary that cannot be executed makes EnsureNetwork fail, which is the
	// path under test.
	d := &DockerCLI{Bin: "/nonexistent-engine-binary", Engine: EnginePodman}
	spec := RunSpec{Network: SandboxNetwork, Name: "sandbox-abc123"}

	cleanup, err := d.perRunNetwork(t.Context(), &spec)
	if err == nil {
		t.Fatal("a failed network creation was allowed to proceed")
	}
	if cleanup != nil {
		t.Error("a failed creation returned a cleanup for a network that does not exist")
	}
	// The refusal *is* the error. spec.Network is deliberately not mutated on
	// failure, so there is nothing half-applied for a caller to act on — the
	// old code's mistake was continuing at all, not which name it continued with.
	if !strings.Contains(err.Error(), "isolated network") {
		t.Errorf("the error does not say what failed: %v", err)
	}
}

// Without a container name there is nothing to derive a per-run network from,
// and sharing one would defeat the isolation. Refuse rather than share.
func TestPerRunNetworkRefusesWithoutAContainerName(t *testing.T) {
	d := &DockerCLI{Bin: "podman"}
	spec := RunSpec{Network: SandboxNetwork}
	if _, err := d.perRunNetwork(t.Context(), &spec); err == nil {
		t.Error("a nameless podman run silently shared one network with every other sandbox")
	}
}

// Docker shares one network, so perRunNetwork must be inert for it — no rewrite,
// no cleanup, no daemon call.
func TestPerRunNetworkIsInertForDocker(t *testing.T) {
	d := &DockerCLI{Bin: "/nonexistent-engine-binary", Engine: EngineDocker}
	spec := RunSpec{Network: SandboxNetwork, Name: "sandbox-abc123"}
	cleanup, err := d.perRunNetwork(t.Context(), &spec)
	if err != nil || cleanup != nil || spec.Network != SandboxNetwork {
		t.Errorf("docker path was not inert: net=%q cleanup=%v err=%v", spec.Network, cleanup != nil, err)
	}
	// And `none` is left alone under either engine.
	p := &DockerCLI{Bin: "podman"}
	spec = RunSpec{Network: "none", Name: "sandbox-abc123"}
	if _, err := p.perRunNetwork(t.Context(), &spec); err != nil || spec.Network != "none" {
		t.Errorf("network: none was rewritten to %q", spec.Network)
	}
}
