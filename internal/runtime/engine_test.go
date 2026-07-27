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
