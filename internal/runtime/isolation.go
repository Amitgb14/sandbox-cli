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
func (c ContainerInfo) NotTheHostDefault() bool { return !sharedKernelRuntimes[c.Runtime] }

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

// RuntimeGap says why a run does not have a kernel of its own, or that it does.
//
// The classification is here, and pure, because two callers have to reach the
// same verdict from the same evidence: `doctor` explains it before a run, and
// the prod profile enforces it at launch. When those two disagree, the preflight
// is worse than useless — it tells an operator a machine is fine that then
// refuses, or clears one that should not have run.
type RuntimeGap int

const (
	// GapNone: the run has a kernel of its own, or there is nothing to ask for.
	GapNone RuntimeGap = iota
	// GapNotSelected: this daemon has a stronger runtime registered and nothing
	// selected it. The sharpest case — the boundary was available and unused.
	GapNotSelected
	// GapMissing: the runtime the config names is not registered with this
	// daemon. The launch would fail; this is the earlier place to find out.
	GapMissing
	// GapNotInstalled: no stronger runtime is registered, but this daemon could
	// have one.
	GapNotInstalled
	// GapNotRegistrable: this engine cannot be given a runtime of its own at
	// all. Docker Desktop keeps every container in its own VM instead, which is
	// a boundary — just not a per-run, selectable one.
	GapNotRegistrable
	// GapUnknown: the daemon could not be asked. Distinct from every answer
	// above, because prod does not get to assume the one it would prefer.
	GapUnknown
)

// RuntimeSupport is what a daemon says about kernels of their own: which
// stronger runtimes it has registered, whether it could be given one, and
// whether it could be asked at all.
type RuntimeSupport struct {
	Registered  []string
	Registrable bool
	Known       bool
}

// ClassifyRuntimeGap turns the selected runtime and the daemon's answer into the
// one verdict both callers use.
//
// Order matters and is the point: the daemon's own evidence is read before any
// assumption about the platform. A host with a stronger runtime registered is
// asked to use it wherever it is, and only a host with none registered gets the
// "could you install one" question — which is the question a client's own
// operating system cannot answer, since the daemon may be somewhere else
// entirely.
func ClassifyRuntimeGap(selected string, s RuntimeSupport) RuntimeGap {
	if !s.Known {
		return GapUnknown
	}
	if StrongerRuntime(selected) {
		for _, n := range s.Registered {
			if n == selected {
				return GapNone
			}
		}
		return GapMissing
	}
	if len(s.Registered) > 0 {
		return GapNotSelected
	}
	if s.Registrable {
		return GapNotInstalled
	}
	return GapNotRegistrable
}
