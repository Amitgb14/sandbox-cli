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
		"runsc", "runsc-kvm", "kata-fc", "kata-clh", "crun-vm",
	} {
		if !StrongerRuntime(name) {
			t.Errorf("StrongerRuntime(%q) = false, want true", name)
		}
	}
	// The default, the shared-kernel alternatives, and anything unrecognised —
	// including a name that only looks like one of ours. Nothing here may claim
	// a boundary a run did not get.
	//
	// The three Kata names are here rather than above on purpose: a VM boundary
	// is only as strong as its device model, and none of these three names says
	// which one it got. See strongerRuntimes.
	for _, name := range []string{
		"", "runc", "crun", "youki", "docker-runc", "RUNSC", "runsc-hostnet",
		"kata", "kata-runtime", "kata-qemu",
	} {
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
			// Shown, and deliberately not vouched for. A bare kata name resolves to
			// whatever hypervisor configuration.toml selects — QEMU by default — so
			// it does not say which device model the boundary actually has. This is
			// the pair of answers strongerRuntimes and notHostDefault exist to give
			// separately: worth naming in a listing, not a claim about the boundary.
			name: "docker, kata-runtime", hostConfig: "kata-runtime", want: "kata-runtime",
			wantStronger: false, wantWorthShowing: true,
		},
		{
			// The regression: without the OCIRuntime branch this yielded "oci",
			// which is neither stronger nor the default, so podman users saw a
			// RUNTIME column full of a placeholder — or, before that, none at all.
			// A name that says which hypervisor it got, so it is vouched for.
			name: "podman, kata-fc", oci: "kata-fc", hostConfig: "oci", want: "kata-fc",
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
