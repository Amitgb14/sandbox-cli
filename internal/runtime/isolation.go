package runtime

// What kind of boundary a run actually got.
//
// The OCI runtime is the one setting that changes the *kind* of isolation
// rather than its degree: everything else sandbox-cli does — dropped
// capabilities, no-new-privileges, seccomp, a non-root user, a default-deny
// egress firewall — narrows what an agent can do *through* the host kernel, and
// none of it changes the fact that a kernel vulnerability is a host compromise.
// A microVM gives the sandbox its own kernel; gVisor puts a userspace kernel in
// front of the host's.
//
// So "which runtime ran this" is worth recording and worth showing, and it is
// the question `docs/roadmap/task-3-stronger-isolation.md` is built around.

// strongerRuntimes are the OCI runtimes that do not simply hand the container
// the host kernel.
//
// A list of names rather than a property we can ask about, because there is
// nothing to ask: docker reports the runtimes registered with the daemon and
// says nothing about what they are. Kept deliberately short and made of names
// that are documented and stable, for the same reason internal/creds keeps its
// prefix list short — a list of other people's product names can be neither
// completed nor kept current, so it should only contain entries that are
// distinctive and unlikely to be reused for something weaker.
//
// The consequence of the list being incomplete is the mild one: an unrecognised
// stronger runtime is reported by name and simply not *called* stronger. It is
// never the other way round — nothing here can claim a boundary a run did not
// get.
var strongerRuntimes = map[string]bool{
	"runsc":        true, // gVisor
	"runsc-kvm":    true, // gVisor on KVM
	"kata":         true, // Kata Containers
	"kata-runtime": true,
	"kata-qemu":    true,
	"kata-fc":      true, // Firecracker shim
	"kata-clh":     true, // Cloud Hypervisor shim
	"crun-vm":      true, // crun's microVM mode
}

// StrongerRuntime reports whether the named OCI runtime gives the container a
// kernel of its own rather than sharing the host's.
//
// An empty name means the daemon's default, which is runc on every host this
// tool has met — shared-kernel, so false.
func StrongerRuntime(name string) bool { return strongerRuntimes[name] }

// StrongerIsolation reports whether this container is running on such a runtime,
// as read back from the engine rather than as requested.
func (c ContainerInfo) StrongerIsolation() bool { return StrongerRuntime(c.Runtime) }

// sharedKernelRuntimes are the names that mean "the ordinary, shared-kernel
// default". Several, because the default is not one name: docker ships runc,
// Fedora and podman default to crun, and youki is a drop-in for both.
var sharedKernelRuntimes = map[string]bool{
	"":            true, // the engine's default, unnamed
	"runc":        true,
	"crun":        true,
	"youki":       true,
	"docker-runc": true, // the old vendored fork
}

// NotTheHostDefault reports whether a container is on a runtime worth naming in
// a listing — anything that is not one of the ordinary shared-kernel ones.
//
// Deliberately the complement of a *short* list rather than membership of
// strongerRuntimes, and the two lists fail in opposite directions on purpose.
// strongerRuntimes answers "may I call this a kernel of its own", so an
// unrecognised name must not qualify. This answers "is this worth showing the
// reader", so an unrecognised name must — gVisor's own daemon.json registers
// runsc-hostnet and runsc-debug, an admin may register a runtime under any name
// at all, and a run on one of those rendering byte-for-byte like a runc run is
// precisely the confusion the column exists to remove.
//
// So a name nobody here has heard of is shown and not characterised, which is
// the honest pair of answers.
func (c ContainerInfo) NotTheHostDefault() bool { return notHostDefault(c.Runtime) }

// notHostDefault is the same question about a name on its own.
//
// It is the complement of a short list rather than membership of
// strongerRuntimes, and that direction is load-bearing in both callers. The
// listing shows a name it does not recognise rather than hiding it; the prod
// gate *accepts* a name it does not recognise rather than refusing a run whose
// operator deliberately selected something — gVisor registers itself as
// runsc-hostnet and runsc-debug, and an admin may use any name at all. A short
// list of names we are sure are shared-kernel can be kept honest; a list of
// every strong runtime anyone will ever register cannot.
func notHostDefault(name string) bool { return !sharedKernelRuntimes[name] }

// containerRuntime picks the runtime name out of an engine's inspect output.
//
// Two fields because the two engines answer differently, and podman's answer to
// the docker-shaped question is a placeholder rather than a name: it fills
// HostConfig.Runtime with the literal "oci" for compatibility and reports the
// real runtime in OCIRuntime, which docker does not emit at all. Preferring the
// podman-only field when it is present needs no engine flag to be threaded
// here, and an engine that reports neither yields "" — unknown, which the
// listing renders as a dash rather than as a claim.
func containerRuntime(ociRuntime, hostConfigRuntime string) string {
	if ociRuntime != "" {
		return ociRuntime
	}
	if hostConfigRuntime == podmanRuntimePlaceholder {
		return ""
	}
	return hostConfigRuntime
}

// podmanRuntimePlaceholder is what podman puts in the docker-compatible field.
const podmanRuntimePlaceholder = "oci"

// RuntimeGap says whether a run got a kernel of its own, or why it might not
// have.
//
// Three outcomes, and the shape of them is the lesson from getting this wrong
// once: **only one of them is provable, so only one of them refuses.** The first
// design tried to decide whether a host *could* have been given a stronger
// runtime, and every signal available for that turned out to be wrong somewhere
// — a product name that colima and OrbStack do not report, a "remote" flag that
// is true for a podman client talking to bare metal, and a registered-runtime
// list that podman answers with one name. Guessing wrong refused hosts that were
// fine and waived the demand on hosts that were not.
//
// So the classification now rests on evidence the engine gives directly, and
// where there is none it says so instead of assuming.
type RuntimeGap int

const (
	// GapNone: the run named a runtime that is not one of the ordinary
	// shared-kernel ones. Whether the engine really has it is settled at launch,
	// which refuses an unregistered runtime — so a container that starts got
	// what it named.
	GapNone RuntimeGap = iota
	// GapNotSelected: the engine reports a stronger runtime and nothing selected
	// it. The one provable gap, and the only one that refuses: the boundary was
	// there and unused.
	GapNotSelected
	// GapUnverified: nothing stronger was reported, or the engine could not be
	// asked. This is not "there is no boundary" and not "there is one" — it is
	// the absence of evidence either way, which is worth saying out loud and not
	// worth refusing over, since the tool cannot tell a Linux host that could
	// install Kata from a VM image the user does not compose.
	GapUnverified
)

// RuntimeSupport is what an engine said about kernels of their own.
//
// Registered is the stronger runtimes it reported. It is evidence when
// non-empty and nothing when empty: docker lists every registered runtime,
// podman answers with the *active* one only, so an empty list means "none
// reported", never "none exist". That asymmetry is why an empty list warns
// rather than refuses.
type RuntimeSupport struct {
	Registered []string
	Known      bool
}

// ClassifyRuntimeGap is the verdict `doctor` explains and the prod profile
// enforces, computed once so the preflight and the launch cannot disagree.
func ClassifyRuntimeGap(selected string, s RuntimeSupport) RuntimeGap {
	if notHostDefault(selected) {
		return GapNone
	}
	if s.Known && len(s.Registered) > 0 {
		return GapNotSelected
	}
	return GapUnverified
}
