package runtime

import (
	"strings"
	"testing"
)

// One daemon answers the same question in two vocabularies. Observed on Rocky
// Linux 10.2, the first host looked at that was not Docker Desktop:
//
//	docker info --format '{{.DefaultRuntime}}'   ->  runc
//	docker info --format '{{json .Runtimes}}'    ->  {"io.containerd.runc.v2": …}
//
// Everything here is a pure function of a name, so the whole matrix is testable
// on a machine with neither Kata nor gVisor installed — which is the point,
// since the hosts that have them are the hosts nobody has.
func TestRuntimeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"io.containerd.runc.v2", "runc"},
		{"io.containerd.kata.v2", "kata"},
		{"io.containerd.runsc.v1", "runsc"},
		{"io.containerd.kata-qemu.v2", "kata-qemu"},

		// Already a runtime name: untouched.
		{"runc", "runc"},
		{"runsc", "runsc"},
		{"kata-runtime", "kata-runtime"},
		{"", ""},

		// Not the shim pattern. The prefix alone is not enough — the version
		// suffix has to be there and has to be digits — so a runtime that merely
		// starts with the prefix keeps its whole name rather than being cut at a
		// dot that means nothing.
		{"io.containerd.runc", "io.containerd.runc"},
		{"io.containerd.runc.vX", "io.containerd.runc.vX"},
		{"io.containerd.runc.v", "io.containerd.runc.v"},
		{"io.containerd..v2", "io.containerd..v2"},
		{"com.example.runc.v2", "com.example.runc.v2"},
		{"my.runtime", "my.runtime"},

		// The separator taken from the right, so a runtime whose own name ends in
		// something version-shaped keeps it.
		{"io.containerd.kata.v2.v2", "kata.v2"},
	}
	for _, tc := range tests {
		if got := runtimeName(tc.in); got != tc.want {
			t.Errorf("runtimeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestClassificationSeesThroughShimNames: both lists are written in runtime
// names, so a daemon that speaks shim names was invisible to both — and the two
// failure directions are not equally harmless. Being unable to see runc is a
// wrong label; being unable to see kata is a boundary that goes unnoticed.
func TestClassificationSeesThroughShimNames(t *testing.T) {
	if !StrongerRuntime("io.containerd.kata-fc.v2") {
		t.Error("a kata-fc shim must be recognised as a kernel of its own; missing it is a boundary reported as absent")
	}
	if !StrongerRuntime("io.containerd.runsc.v1") {
		t.Error("a gVisor shim must be recognised as a kernel of its own")
	}
	// Normalisation is not the same as admission. The bare kata shim reduces to
	// "kata" correctly (TestRuntimeName pins that) and is still not vouched for,
	// because the name says nothing about which hypervisor is underneath.
	if StrongerRuntime("io.containerd.kata.v2") {
		t.Error("a bare kata name does not say which device model the VM has; see strongerRuntimes")
	}
	if StrongerRuntime("io.containerd.runc.v2") {
		t.Error("a runc shim shares the host kernel; calling it stronger would claim a boundary no run got")
	}
	// notHostDefault deliberately does NOT normalise. An admin can point
	// io.containerd.runc.v2 at any binary, so a name this file has not seen
	// literally must still be shown rather than folded into the defaults — the
	// opposite direction to strongerRuntimes, and the reason the two differ.
	if !notHostDefault("io.containerd.runc.v2") {
		t.Error("a shim-shaped name must still be shown: daemon.json may point it at anything")
	}
	if !notHostDefault("io.containerd.kata.v2") {
		t.Error("a kata shim is worth naming in a listing")
	}
	if notHostDefault("runc") || notHostDefault("crun") {
		t.Error("the plain shared-kernel names are still the ordinary case")
	}
	// An unrecognised name is still shown and still not characterised — the
	// property the two lists have always had, unchanged by normalisation.
	if !notHostDefault("sysbox-runc") || StrongerRuntime("sysbox-runc") {
		t.Error("an unrecognised runtime must be shown and not vouched for")
	}
}

// TestMatchRuntimeReturnsTheEnginesOwnName is the regression for the reported
// symptom — `--runtime runc` refused on a host whose default runtime is runc —
// and for the trap in fixing it: accepting a spelling is only safe if the
// spelling actually sent is one the engine knows.
func TestMatchRuntimeReturnsTheEnginesOwnName(t *testing.T) {
	avail := []string{"io.containerd.runc.v2"} // exactly what Rocky 10.2 reports

	got, ok := matchRuntime("runc", avail)
	if !ok {
		t.Fatal("--runtime runc must be accepted on a daemon that lists it as a shim")
	}
	if got != "io.containerd.runc.v2" {
		t.Errorf("matched %q, want the daemon's own key: passing the user's spelling back to "+
			"docker trades a wrong refusal for `unknown or invalid runtime name`", got)
	}
	if got, ok := matchRuntime("io.containerd.runc.v2", avail); !ok || got != "io.containerd.runc.v2" {
		t.Errorf("the name the daemon printed must be accepted unchanged, got %q ok=%v", got, ok)
	}
	if _, ok := matchRuntime("runsc", avail); ok {
		t.Error("a runtime the host does not have must still be refused")
	}

	// Exact wins over normalised: if the user typed a spelling the engine itself
	// uses, that is the one to send.
	both := []string{"io.containerd.runsc.v1", "runsc"}
	if got, _ := matchRuntime("runsc", both); got != "runsc" {
		t.Errorf("matched %q, want the literal match %q", got, "runsc")
	}

	// A shim major the host does not have resolves to the one it does, rather
	// than being waved through and failing at launch.
	if got, ok := matchRuntime("io.containerd.runsc.v1", []string{"io.containerd.runsc.v2"}); !ok ||
		got != "io.containerd.runsc.v2" {
		t.Errorf("matched %q ok=%v, want the registered shim major %q",
			got, ok, "io.containerd.runsc.v2")
	}

	err := runtimeHint("runsc", avail)
	if err == nil {
		t.Fatal("a runtime the host does not have must still be refused")
	}
	// The message keeps the daemon's own spelling: it is what the user will see
	// in `docker info`, and what they must write in daemon.json.
	if got := err.Error(); !strings.Contains(got, "io.containerd.runc.v2") {
		t.Errorf("the refusal should list what the daemon reported, got %q", got)
	}
}
