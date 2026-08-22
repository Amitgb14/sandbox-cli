package runtime

import "testing"

// TestPlanForNetwork pins the three states a per-run network can be in, and the
// middle one is the bug: `podman network rm` refuses while an **exited**
// container is attached, which the first reaper read as "a running container
// holds it open, so this moves on". Measured on podman 6.0.2:
//
//	$ podman network rm sandbox-cli-leaktest
//	Error: "sandbox-cli-leaktest" has associated containers with it.
//	Use -f to forcibly delete containers and pods: network is being used
func TestPlanForNetwork(t *testing.T) {
	for _, tc := range []struct {
		name string
		on   []attachment
		want networkPlan
	}{
		{"nothing attached — the state that wedges reload --all", nil, planRemove},
		{"an exited husk still holds it", []attachment{{Name: "c0", State: "exited", Sandbox: true}}, planForce},
		{"a launch that never started", []attachment{{Name: "c0", State: "created", Sandbox: true}}, planForce},
		{"dead counts as finished too", []attachment{{Name: "c0", State: "dead", Sandbox: true}, {Name: "c1", State: "exited", Sandbox: true}}, planForce},
		{"a live agent is on it", []attachment{{Name: "c0", State: "running", Sandbox: true}}, planSkip},
		{"one live among husks is still live", []attachment{{Name: "c0", State: "exited", Sandbox: true}, {Name: "c1", State: "running", Sandbox: true}}, planSkip},
		{"paused is somebody's run in an odd moment", []attachment{{Name: "c0", State: "paused", Sandbox: true}}, planSkip},
		{"an unreadable state is not a licence", []attachment{{Name: "c0", State: "", Sandbox: true}}, planSkip},
		{"a state this code has never heard of", []attachment{{Name: "c0", State: "reprovisioning", Sandbox: true}}, planSkip},
		{"case and spacing come from a template, not a contract", []attachment{{Name: "c0", State: " Exited ", Sandbox: true}}, planForce},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := planForNetwork(tc.on); got != tc.want {
				t.Errorf("planForNetwork(%+v) = %v, want %v", tc.on, got, tc.want)
			}
		})
	}
}

// TestReapIsPerRunEnginesOnly: docker shares one network, so nothing carrying the
// per-run prefix can be attributed to a finished run there — and sweeping those
// names on an engine this run was never configured for is a broader licence than
// the docker path takes.
func TestReapIsPerRunEnginesOnly(t *testing.T) {
	d := &DockerCLI{Bin: "docker"}
	if got := d.ReapPerRunNetworks(t.Context(), "sandbox.cli"); got != nil {
		t.Errorf("docker reaped %v, want nothing: it has no per-run networks", got)
	}
}

// TestForeignContainersAreNeverForced: `-f` removes the containers as well as
// the network, and somebody else's stopped container is not `clean`'s to delete
// — even sitting on a network sandbox-cli made.
func TestForeignContainersAreNeverForced(t *testing.T) {
	on := []attachment{{Name: "theirs", State: "exited", Sandbox: false}}
	if got := planForNetwork(on); got != planSkip {
		t.Errorf("planForNetwork(foreign husk) = %v, want planSkip", got)
	}
	if got := skipReason(on); got != "a container this command does not own is on it: theirs" {
		t.Errorf("skipReason = %q, want it to name the container", got)
	}
	mixed := []attachment{{Name: "ours", State: "exited", Sandbox: true}, {Name: "theirs", State: "exited"}}
	if got := planForNetwork(mixed); got != planSkip {
		t.Errorf("planForNetwork(one foreign among ours) = %v, want planSkip", got)
	}
}

// TestRefusalReasonIsNeverEmpty: "kept network sandbox-cli-x ()" reads as a
// truncated bug. An engine that fails silently is exactly when the person
// reading has least to go on.
func TestRefusalReasonIsNeverEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n"} {
		if got := refusalReason(in); got != "the engine refused and gave no reason" {
			t.Errorf("refusalReason(%q) = %q, want the fallback sentence", in, got)
		}
	}
	// When it does say something, the last line is the specific one — engine
	// errors put the general complaint first.
	got := refusalReason("Error: something\n\"sandbox-cli-x\" has associated containers with it")
	if got != `"sandbox-cli-x" has associated containers with it` {
		t.Errorf("refusalReason kept the wrong line: %q", got)
	}
}
