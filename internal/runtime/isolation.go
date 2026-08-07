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
