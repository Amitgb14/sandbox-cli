package runtime

import "testing"

// The two runtime lists, pinned in the package that owns them.
//
// They answer different questions and fail in opposite directions, which is the
// property worth protecting: StrongerRuntime decides whether this tool may
// *call* a boundary a kernel of its own, so an unrecognised name must not
// qualify; NotTheHostDefault decides whether a name is worth showing a reader,
// so an unrecognised name must.

func TestStrongerRuntimeNamesOnlyWhatItKnows(t *testing.T) {
	for _, name := range []string{
		"runsc", "runsc-kvm", "kata", "kata-runtime", "kata-qemu", "kata-fc", "kata-clh", "crun-vm",
	} {
		if !StrongerRuntime(name) {
			t.Errorf("StrongerRuntime(%q) = false, want true", name)
		}
	}
	// The default, the shared-kernel alternatives, and anything unrecognised —
	// including a name that only looks like one of ours. Nothing here may claim
	// a boundary a run did not get.
	for _, name := range []string{"", "runc", "crun", "youki", "docker-runc", "RUNSC", "runsc-hostnet"} {
		if StrongerRuntime(name) {
			t.Errorf("StrongerRuntime(%q) = true, want false", name)
		}
	}
}

func TestNotTheHostDefaultShowsWhatItCannotVouchFor(t *testing.T) {
	// Ordinary shared-kernel runtimes: nothing to say.
	for _, name := range []string{"", "runc", "crun", "youki", "docker-runc"} {
		if (ContainerInfo{Runtime: name}).NotTheHostDefault() {
			t.Errorf("NotTheHostDefault(%q) = true, want false — an ordinary run should add no column", name)
		}
	}
	// Recognised or not, anything else is worth naming. runsc-hostnet and
	// runsc-debug are gVisor's own documented registrations, and an admin may
	// register a runtime under any name at all: a run on one of those must not
	// render byte-for-byte like a runc run.
	for _, name := range []string{"runsc", "kata-runtime", "runsc-hostnet", "runsc-debug", "gvisor", "something-else"} {
		if !(ContainerInfo{Runtime: name}).NotTheHostDefault() {
			t.Errorf("NotTheHostDefault(%q) = false, want true", name)
		}
	}
}

// TestContainerRuntimePrefersPodmansOwnField covers the dialect difference that
// made this field inert on podman: podman fills the docker-compatible
// HostConfig.Runtime with the literal "oci" — a placeholder, not a name — and
// reports the real runtime in OCIRuntime, which docker does not emit.
func TestContainerRuntimePrefersPodmansOwnField(t *testing.T) {
	cases := []struct {
		name             string
		oci, hostConfig  string
		want             string
		wantStronger     bool
		wantWorthShowing bool
	}{
		{name: "docker, default", hostConfig: "runc", want: "runc"},
		{
			name: "docker, kata", hostConfig: "kata-runtime", want: "kata-runtime",
			wantStronger: true, wantWorthShowing: true,
		},
		{
			// The regression: without the OCIRuntime branch this yielded "oci",
			// which is neither stronger nor the default, so podman users saw a
			// RUNTIME column full of a placeholder — or, before that, none at all.
			name: "podman, kata", oci: "kata-runtime", hostConfig: "oci", want: "kata-runtime",
			wantStronger: true, wantWorthShowing: true,
		},
		{name: "podman, crun", oci: "crun", hostConfig: "oci", want: "crun"},
		{
			// An engine that reports neither: unknown, and unknown is not a claim.
			// The listing renders this as a dash.
			name: "neither field", want: "",
		},
		{
			// The placeholder alone, from a podman version that omits OCIRuntime.
			// "oci" names nothing, so it must not reach the reader as if it did.
			name: "placeholder only", hostConfig: "oci", want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := containerRuntime(tc.oci, tc.hostConfig)
			if got != tc.want {
				t.Fatalf("containerRuntime(%q, %q) = %q, want %q", tc.oci, tc.hostConfig, got, tc.want)
			}
			c := ContainerInfo{Runtime: got}
			if c.StrongerIsolation() != tc.wantStronger {
				t.Errorf("StrongerIsolation() = %v, want %v (runtime %q)", c.StrongerIsolation(), tc.wantStronger, got)
			}
			if c.NotTheHostDefault() != tc.wantWorthShowing {
				t.Errorf("NotTheHostDefault() = %v, want %v (runtime %q)", c.NotTheHostDefault(), tc.wantWorthShowing, got)
			}
		})
	}
}

// ClassifyRuntimeGap is the verdict `doctor` explains and the prod profile
// enforces, so it is pinned here rather than twice in two callers' terms.
//
// Each case is a shape of evidence this function has been wrong about at least
// once: an engine whose *default* is already strong, a name no list knows, a
// name the engine does not have, and the difference between an engine that
// reported nothing and one that could not be asked.
func TestClassifyRuntimeGap(t *testing.T) {
	docker := func(def string, all ...string) RuntimeSupport {
		return RuntimeSupport{All: all, Registered: strongerOnly(all), Default: def, Complete: true, Known: true}
	}
	// podman names only the runtime it is using, so All is not a registered set.
	podman := func(active string) RuntimeSupport {
		return RuntimeSupport{All: []string{active}, Registered: strongerOnly([]string{active}),
			Default: active, Complete: false, Known: true}
	}

	cases := []struct {
		name     string
		selected string
		support  RuntimeSupport
		want     RuntimeGap
	}{
		{"selected and registered", "runsc", docker("runc", "runc", "runsc"), GapNone},
		// The host that had already done the work, and was refused for it.
		{"the engine's default is strong", "", docker("runsc", "runc", "runsc"), GapNone},
		{"podman's default is strong", "", podman("kata"), GapNone},
		// podman cannot list what it is not running, so an absent name proves
		// nothing and must not refuse.
		{"podman, selected but not its active one", "kata-runtime", podman("crun"), GapNone},
		// Docker's list is complete, so an absent name is evidence.
		{"docker, selected but not registered", "kata-runtime", docker("runc", "runc"), GapMissing},
		// Deliberate, unrecognised, permitted — and not called a kernel of its own.
		{"an unrecognised name", "sysbox-runc", docker("runc", "runc", "sysbox-runc"), GapUnrecognised},
		{"registered and unused", "", docker("runc", "runc", "kata-runtime"), GapNotSelected},
		{"a shared-kernel name is not a selection", "runc", docker("runc", "runc", "kata-runtime"), GapNotSelected},
		{"nothing stronger reported", "", docker("runc", "runc"), GapUnverified},
		{"the engine could not be asked", "", RuntimeSupport{}, GapUnknown},
		{"could not be asked, name selected", "kata-runtime", RuntimeSupport{}, GapUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyRuntimeGap(tc.selected, tc.support); got != tc.want {
				t.Errorf("ClassifyRuntimeGap(%q, %+v) = %v, want %v", tc.selected, tc.support, got, tc.want)
			}
		})
	}
}

func TestEffectiveRuntimeFallsBackToTheEnginesDefault(t *testing.T) {
	s := RuntimeSupport{Default: "runsc", Known: true}
	if got := s.EffectiveRuntime(""); got != "runsc" {
		t.Errorf("EffectiveRuntime(\"\") = %q, want the engine default", got)
	}
	if got := s.EffectiveRuntime("kata-runtime"); got != "kata-runtime" {
		t.Errorf("EffectiveRuntime = %q, want what the run selected", got)
	}
}

// TestClassifyRuntimeGapSeesThroughShimNames: the gate's one membership test
// decides a refusal, and a containerd-backed daemon writes the list in a
// different vocabulary from the one a user types.
//
// Observed on Rocky Linux 10.2, where `docker info` reports DefaultRuntime
// `runc` and keys .Runtimes `io.containerd.runc.v2` in the same breath.
func TestClassifyRuntimeGapSeesThroughShimNames(t *testing.T) {
	// A host with Kata registered under its shim name, nothing selecting it.
	shimHost := RuntimeSupport{
		All:        []string{"io.containerd.runc.v2", "io.containerd.kata.v2"},
		Registered: strongerOnly([]string{"io.containerd.runc.v2", "io.containerd.kata.v2"}),
		Default:    "runc",
		Complete:   true,
		Known:      true,
	}
	if len(shimHost.Registered) != 1 {
		t.Fatalf("Registered = %v, want the kata shim recognised as a kernel of its own", shimHost.Registered)
	}
	if got := ClassifyRuntimeGap("", shimHost); got != GapNotSelected {
		t.Errorf("gap = %v, want GapNotSelected: the boundary is installed and unused", got)
	}

	// Asking for it by the name a user would type must not read as missing.
	if got := ClassifyRuntimeGap("kata", shimHost); got != GapNone {
		t.Errorf("gap = %v, want GapNone: the host has kata, however it spells the key", got)
	}
	// And by the daemon's own spelling.
	if got := ClassifyRuntimeGap("io.containerd.kata.v2", shimHost); got != GapNone {
		t.Errorf("gap = %v, want GapNone for the engine's own name", got)
	}

	// The permissive direction is unchanged: a runtime no entry means is still
	// missing, so prod still refuses before the launch fails.
	if got := ClassifyRuntimeGap("runsc", shimHost); got != GapMissing {
		t.Errorf("gap = %v, want GapMissing: this host has no gVisor under any spelling", got)
	}
}
