package sandbox

import (
	"strings"
	"testing"
)

// The key that pairs a detached run's two audit lines must be unique per *run*.
//
// The obvious candidates are both wrong, which is why this is minted rather than
// borrowed. A detached container's name is deterministic — sandbox-<repo>-<branch>,
// so docker's duplicate-name refusal can enforce one agent per branch — so every
// run on a branch would share one, and a reader grouping by it would fold
// unrelated runs into each other. The routing supervisor also hands that name to
// a retry, which would collapse both halves of a failover into one record.
func TestARunIDIsUniquePerRun(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := newRunID()
		if id == "" {
			t.Fatal("empty run id: a detached run's ending would have nothing to attach to")
		}
		if !strings.HasPrefix(id, "run-") {
			t.Errorf("run id %q is not recognisable as one", id)
		}
		if seen[id] {
			t.Fatalf("run id %q was minted twice", id)
		}
		seen[id] = true
	}
}

// And it is not the container name, which is what makes the above true.
func TestARunIDIsNotTheContainerName(t *testing.T) {
	name := ContainerName(Options{Detach: true, RepoID: "repo", Branch: "feat/x"})
	if id := newRunID(); id == name {
		t.Errorf("the run id is the container name (%q) — two runs on one branch would share it", name)
	}
}
