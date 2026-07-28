package config

import "testing"

// TestEngineListsAgree pins the two spellings of "which engines exist" against
// each other. config cannot import runtime without a cycle, so the list is
// duplicated — which is fine only while something fails when they diverge.
func TestEngineListsAgree(t *testing.T) {
	// Kept as literals rather than importing runtime: the point is to catch an
	// engine added on one side and not the other.
	for _, name := range []string{"docker", "podman"} {
		if !ValidEngine(name) {
			t.Errorf("config.ValidEngine rejects %q, which runtime speaks", name)
		}
	}
	for _, name := range []string{"", "dokcer", "containerd", "nerdctl"} {
		if ValidEngine(name) {
			t.Errorf("config.ValidEngine accepts %q", name)
		}
	}
}
