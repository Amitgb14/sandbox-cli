package runtime

import "testing"

// The list decides whether a run gets the host's resolvers instead of the
// engine's. It fails in the direction that costs a broken run rather than a
// silent change: an unlisted runtime resolves nothing, which is loud; a wrongly
// listed one would quietly swap out a working resolver.
func TestEmbeddedResolverUnreachableNamesOnlyWhatWasMeasured(t *testing.T) {
	for _, name := range []string{"runsc", "runsc-kvm", "io.containerd.runsc.v1"} {
		if !EmbeddedResolverUnreachable(name) {
			t.Errorf("EmbeddedResolverUnreachable(%q) = false, want true", name)
		}
	}
	// The host default, the ordinary alternatives, and the runtimes whose answer
	// has not been measured. Kata boots a real kernel, so "it is also isolated" is
	// not evidence about its DNS path.
	for _, name := range []string{
		"", "runc", "crun", "youki", "kata-fc", "kata-clh", "kata-runtime", "crun-vm", "RUNSC",
	} {
		if EmbeddedResolverUnreachable(name) {
			t.Errorf("EmbeddedResolverUnreachable(%q) = true, want false", name)
		}
	}
}
