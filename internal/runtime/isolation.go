package runtime

import "strings"

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
func StrongerRuntime(name string) bool { return strongerRuntimes[runtimeName(name)] }

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
// It looks the name up **raw**, and that asymmetry with StrongerRuntime is the
// point rather than an oversight. The two lists fail in opposite directions, so
// normalisation helps one and hurts the other:
//
//   - strongerRuntimes must not miss a real boundary, and a shim name is still
//     that runtime, so reducing io.containerd.kata.v2 to kata recovers a true
//     positive it would otherwise have thrown away.
//   - sharedKernelRuntimes must not *hide* something unusual. An engine lets an
//     admin point any key at any binary — daemon.json may map
//     io.containerd.runc.v2 at sysbox-runc — so folding every shim-shaped runc
//     name into the defaults would suppress the RUNTIME column for exactly the
//     sessions it exists to reveal.
//
// So a name this file has not seen literally is still shown and still not
// characterised, which is the pair of answers the comment above argues for.
// Both callers depend on that direction. The listing shows a name it does not
// recognise rather than hiding it, and the prod gate *accepts* one rather than
// refusing a run whose operator deliberately selected something — gVisor
// registers itself as runsc-hostnet and runsc-debug, and an admin may use any
// name at all. A short list of names we are sure are shared-kernel can be kept
// honest; a list of every strong runtime anyone will ever register cannot.
func notHostDefault(name string) bool { return !sharedKernelRuntimes[name] }

// runtimeName reduces a containerd **shim** name to the runtime name the lists
// above are written in, and returns anything else untouched.
//
// One daemon answers the same question in two vocabularies, in one command:
//
//	$ docker info --format '{{.DefaultRuntime}}'
//	runc
//	$ docker info --format '{{json .Runtimes}}'
//	{"io.containerd.runc.v2":{"path":"runc", …
//
// Observed on Rocky Linux 10.2 — the first host looked at that was not Docker
// Desktop. Both names mean runc, and comparing them as plain strings said the
// host had no runc: `--runtime runc` was refused as "not registered with the
// Docker daemon", listing `io.containerd.runc.v2` as what it had instead.
//
// The pattern is fixed by containerd — `io.containerd.<runtime>.v<major>` — so
// this matches it exactly rather than splitting on dots: a runtime genuinely
// named with dots is left alone, and only a trailing `.v<digits>` after that
// prefix is treated as a version.
//
// Reducing rather than expanding, because the mapping only goes one way. A shim
// name yields exactly one runtime name; a runtime name does not tell you which
// shim major version a host registered, so the raw name is what callers keep for
// display and for handing back to the engine.
func runtimeName(name string) string {
	const prefix = "io.containerd."
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return name
	}
	base, version, ok := cutLast(rest, ".v")
	if !ok || base == "" || version == "" {
		return name
	}
	for _, r := range version {
		if r < '0' || r > '9' {
			return name
		}
	}
	return base
}

// cutLast is strings.Cut around the *last* occurrence of sep, so a runtime whose
// own name contains the separator keeps it: io.containerd.kata.v2.v2 reduces to
// kata.v2 rather than to kata.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// SameRuntime reports whether two names denote the same runtime, in whichever
// vocabulary each was written. It is what `--runtime runc` has to be matched
// with against a daemon that lists io.containerd.runc.v2.
func SameRuntime(a, b string) bool { return runtimeName(a) == runtimeName(b) }

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

// RuntimeGap says whether a run gets a kernel of its own, or why it might not.
//
// Six outcomes, because the evidence really does come in six shapes, and the
// history of this function is a list of what happens when they are collapsed.
// It has been wrong by inferring a host's capabilities from a product name, by
// treating a short list of known-strong names as a gate on refusal, and by
// reading "nothing selected" without first asking what the engine runs by
// *default* — which refused the hosts that had already done the right thing.
type RuntimeGap int

const (
	// GapNone: the run gets a runtime this tool recognises as having a kernel of
	// its own — named by the run, or the engine's own default.
	GapNone RuntimeGap = iota
	// GapUnrecognised: the run gets a non-default runtime nobody here has heard
	// of. Permitted, because an operator who names one has chosen deliberately
	// and gVisor's own installer produces names this list will never hold — but
	// **not called a kernel of its own**, because sysbox-runc is also a
	// non-default name and it shares the host kernel.
	GapUnrecognised
	// GapMissing: the run names a runtime and the engine's *complete* list does
	// not have it. The launch would fail; this is the cheaper place to learn it.
	GapMissing
	// GapNotSelected: the engine reports a runtime with its own kernel, and
	// neither the run nor the engine's default uses it. The boundary was there
	// and unused.
	GapNotSelected
	// GapUnverified: no stronger runtime was reported. Not proof there is none —
	// podman names only its active runtime — and nothing distinguishes a host
	// that could install one from a VM image its user does not compose.
	GapUnverified
	// GapUnknown: the engine could not be asked. Prod does not get to assume the
	// answer it would prefer, so this refuses rather than warns; the run path and
	// the preflight agree on that, which they did not when this was folded into
	// GapUnverified.
	GapUnknown
)

// RuntimeSupport is what an engine said about the runtimes it has.
//
// Complete is the field that keeps the two engines honest about different
// answers to the same question: docker lists every registered runtime, podman
// names only the one it is using. Membership of All is therefore evidence on
// docker and nothing on podman, and a check that forgot the difference refused
// every prod run on a podman host that had Kata configured.
type RuntimeSupport struct {
	All        []string // every runtime name the engine reported
	Registered []string // those of them with a kernel of their own
	Default    string   // what the engine runs when nothing selects a runtime
	Complete   bool     // All is the full registered set, not just the active one
	Known      bool     // the engine answered at all
}

// EffectiveRuntime is what a run will actually be given: what it selected, or
// the engine's default when it selected nothing.
//
// Reading the default is the difference between "you did not ask for a kernel of
// its own" and "you did not have to" — a host whose daemon.json sets
// default-runtime: runsc gives every container one without a word in any config
// of ours, and refusing it for "nothing selected one" punished the setup that
// had already done the work.
func (s RuntimeSupport) EffectiveRuntime(selected string) string {
	if selected != "" {
		return selected
	}
	return s.Default
}

// ClassifyRuntimeGap is the verdict `doctor` explains and the prod profile
// enforces, computed once so the preflight and the launch cannot disagree.
func ClassifyRuntimeGap(selected string, s RuntimeSupport) RuntimeGap {
	if !s.Known {
		return GapUnknown
	}
	effective := s.EffectiveRuntime(selected)
	if notHostDefault(effective) {
		if s.Complete && !containsRuntime(s.All, effective) {
			return GapMissing
		}
		if StrongerRuntime(effective) {
			return GapNone
		}
		return GapUnrecognised
	}
	if len(s.Registered) > 0 {
		return GapNotSelected
	}
	return GapUnverified
}

// containsRuntime reports whether the engine's list has the requested runtime,
// in whichever vocabulary each was written.
//
// By runtime rather than by spelling, and this is the one comparison in the gate
// where the difference decides a refusal. A containerd-backed daemon keys
// .Runtimes by shim name — `io.containerd.runc.v2` on Rocky Linux 10.2 — so a
// string match read `--runtime kata` against a host listing
// `io.containerd.kata.v2` as GapMissing, and prod refused a machine that had the
// kernel it was demanding.
//
// The permissive direction, which is the one to be careful about, is unchanged:
// this only ever finds a runtime the engine really listed. It cannot invent one,
// and GapMissing still fires for a name no entry means.
func containsRuntime(names []string, want string) bool {
	for _, n := range names {
		if SameRuntime(n, want) {
			return true
		}
	}
	return false
}
