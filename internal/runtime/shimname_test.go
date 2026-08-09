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
	if !StrongerRuntime("io.containerd.kata.v2") {
		t.Error("a kata shim must be recognised as a kernel of its own; missing it is a boundary reported as absent")
	}
	if !StrongerRuntime("io.containerd.runsc.v1") {
		t.Error("a gVisor shim must be recognised as a kernel of its own")
	}
	if StrongerRuntime("io.containerd.runc.v2") {
		t.Error("a runc shim shares the host kernel; calling it stronger would claim a boundary no run got")
	}
	if notHostDefault("io.containerd.runc.v2") {
		t.Error("a runc shim IS the ordinary host default and must not be highlighted as unusual")
	}
	if !notHostDefault("io.containerd.kata.v2") {
		t.Error("a kata shim is worth naming in a listing")
	}
	// An unrecognised name is still shown and still not characterised — the
	// property the two lists have always had, unchanged by normalisation.
	if !notHostDefault("sysbox-runc") || StrongerRuntime("sysbox-runc") {
		t.Error("an unrecognised runtime must be shown and not vouched for")
	}
}

// TestRuntimeHintMatchesByRuntimeNotSpelling is the regression for the reported
// symptom: `--runtime runc` refused on a host whose default runtime is runc.
func TestRuntimeHintMatchesByRuntimeNotSpelling(t *testing.T) {
	avail := []string{"io.containerd.runc.v2"} // exactly what Rocky 10.2 reports

	if err := runtimeHint("runc", avail); err != nil {
		t.Errorf("--runtime runc must be accepted on a daemon that lists it as a shim: %v", err)
	}
	if err := runtimeHint("io.containerd.runc.v2", avail); err != nil {
		t.Errorf("the name the daemon itself printed must also be accepted: %v", err)
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
