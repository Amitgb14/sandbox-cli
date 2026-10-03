package runtime

import (
	"strings"
	"testing"
)

// The remedy names candidates, never a diagnosis.
//
// It used to say "remove \"seccomp-profile\": \"unconfined\"" flatly, and a user
// met that on a machine with no such key anywhere — not in daemon.json, not in
// Docker Desktop's settings store, not in admin policy — while the daemon
// reported profile=unconfined all the same. The refusal was right and its advice
// was not, which is worse than saying less: being sent to delete a line that is
// not there reads as the tool having misdiagnosed, and makes a correct refusal
// look wrong too.
func TestSeccompRemedyDoesNotAssertACause(t *testing.T) {
	got := SeccompRemedy(EngineDocker)

	// Conditional, not imperative. "if daemon.json sets … remove it" stays true
	// on a machine where it does not.
	if !strings.Contains(got, "if daemon.json sets") {
		t.Errorf("the daemon.json advice is not conditional:\n%s", got)
	}
	// And it does not stop there, because the case that produced this bug is the
	// one where that file is clean.
	if !strings.Contains(got, "if it does not") {
		t.Errorf("no advice for a daemon.json that sets nothing:\n%s", got)
	}
	// Reported, not asserted — the same rule creds.Classify keeps, so the
	// sentence survives the cause turning out to be something else.
	if !strings.Contains(got, "has been reported to") {
		t.Errorf("the second cause is stated as fact rather than as a report:\n%s", got)
	}
}

// Podman gets podman's answer. The remedy used to assume Docker Desktop, which
// is not even the common case on Linux.
func TestSeccompRemedyFollowsTheEngine(t *testing.T) {
	docker, podman := SeccompRemedy(EngineDocker), SeccompRemedy(EnginePodman)
	if docker == podman {
		t.Fatal("both engines got the same advice")
	}
	if !strings.Contains(podman, "containers.conf") {
		t.Errorf("podman remedy does not name its own config: %s", podman)
	}
	if strings.Contains(podman, "Docker Desktop") {
		t.Errorf("podman remedy sends the user to Docker Desktop: %s", podman)
	}
}
