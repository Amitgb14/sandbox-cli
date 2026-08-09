package sandbox

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// Whether the container user can write the host paths a run depends on, asked
// before the run rather than discovered during it.
//
// hostgroup.go gives the container the host's primary group, and sandbox-init
// gives it a umask that keeps what it *creates* group-writable. Neither does
// anything for what the host created **earlier**, and nothing repairs those:
// ShareWithSandboxGroup is deliberately scoped to sandbox-owned directories —
// the persisted HOME, the history bucket, the levels EnsureGuestDir makes — and
// the workspace is the user's own tree, where chowning would break the host side
// for the reasons hostgroup.go records.
//
// So whether the agent can write /workspace comes down to the host's umask,
// which nothing surfaces:
//
//	umask 002 (user-private groups)  dirs 0775  the group has write   works
//	umask 022                        dirs 0755  the group has r-x     read-only workspace
//
// An agent with a read-only workspace does not fail cleanly. It tries to edit,
// reports that it could not, and nobody suspects a host permission bit — the
// same failure reached through git looks like this, and names the object store
// without naming which of its 256 fan-out directories is at fault:
//
//	error: insufficient permission for adding an object to repository database .git/objects
//
// This detects and reports; it never repairs. That is the same bargain
// EnsureGuestDir makes with a foreign-owned level, and for the same reason —
// the state can be described exactly, and fixing it would mean writing to a tree
// the user owns.
//
// The answer is computed from the mode and the ids rather than by starting a
// container to try it. That is how the kernel decides, it costs a stat, and it
// can be given for every mount rather than for one. It does not account for
// POSIX ACLs, which can grant access the mode bits deny — so this can report a
// problem where there is none, and says "cannot" only about what the mode says.

// unwritablePath is one host directory the container user cannot create files
// in, with the facts that decided it. Rendering is the caller's job: the run
// path and doctor say it differently.
type unwritablePath struct {
	Path string
	Mode fs.FileMode
	UID  int
	GID  int
}

// String is the one-line form both callers build on.
func (u unwritablePath) String() string {
	return fmt.Sprintf("%s is mode %#o owned by %d:%d", u.Path, u.Mode.Perm(), u.UID, u.GID)
}

// unwritableMounts returns the read-write host directories this run will mount
// that the container user cannot write, or nil when the question does not arise.
//
// Three cases answer "does not arise", and each is a place where the ids do not
// meet the way this check assumes:
//
//   - Off Linux. Docker Desktop virtualizes bind-mount ownership, so the mode on
//     the host never decides anything and every path here would be judged by
//     numbers the container will not see.
//   - Under podman. HostUserMapping maps the host user onto the container user,
//     so a directory that user owns is writable by construction.
//   - When the guest runs as root, or as a user this process cannot resolve to
//     numbers. uid 0 bypasses the mode bits, and a name only the image's passwd
//     database defines is not something to guess at from out here.
func unwritableMounts(spec runtime.RunSpec, engine string) []unwritablePath {
	if goruntime.GOOS != "linux" || engine == string(runtime.EnginePodman) {
		return nil
	}
	uid, gid, ok := guestIDs(spec)
	if !ok {
		return nil
	}

	var out []unwritablePath
	seen := map[string]bool{}
	for _, m := range spec.Mounts {
		// A named volume has no host path to judge, and a read-only mount is not
		// one the agent was promised it could write.
		if m.Volume || m.RO || seen[m.Source] {
			continue
		}
		seen[m.Source] = true
		out = append(out, checkDir(m.Source, uid, gid)...)
	}
	return out
}

// checkDir reports dir when it is unwritable, and — when dir is a git directory
// — its object store as well.
//
// The object store gets its own look because a writable .git with an unwritable
// objects/ fan-out is the case that produced the opaque git error above: git
// creates those 256 directories on demand, under whatever umask the host had at
// the time, so a repository can be years old and half of them wrong.
func checkDir(dir string, uid, gid int) []unwritablePath {
	var out []unwritablePath
	if u, bad := unwritableBy(dir, uid, gid); bad {
		out = append(out, u)
	}
	objects := filepath.Join(dir, "objects")
	if !isExistingDir(filepath.Join(dir, "refs")) || !isExistingDir(objects) {
		return out // not a git directory; nothing further to ask
	}
	if u, bad := unwritableBy(objects, uid, gid); bad {
		out = append(out, u)
	}
	// The fan-out, first offender only: a repository where the umask was wrong
	// has dozens, and a list of dozens is one nobody reads. The remedy is the
	// same for all of them.
	entries, err := os.ReadDir(objects)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if u, bad := unwritableBy(filepath.Join(objects, e.Name()), uid, gid); bad {
			out = append(out, u)
			break
		}
	}
	return out
}

// unwritableBy reports whether uid/gid can create a file in dir, by the rule the
// kernel uses: the first of owner/group/other that matches decides, and creating
// an entry needs both write and search on the directory.
//
// A path that cannot be read at all is not reported. This is a courtesy check on
// the way into a run, and a stat that fails is a question that could not be
// asked rather than an answer of "no" — the same distinction doctor draws
// between StatusUnknown and StatusWeak.
func unwritableBy(dir string, uid, gid int) (unwritablePath, bool) {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return unwritablePath{}, false
	}
	dUID, okU := ownerUID(fi)
	dGID, okG := ownerGID(fi)
	if !okU || !okG {
		return unwritablePath{}, false
	}
	perm := fi.Mode().Perm()

	const writeAndSearch = 0o3 // w|x, in whichever class applies
	var bits fs.FileMode
	switch {
	case dUID == uid:
		bits = perm >> 6
	case dGID == gid:
		bits = perm >> 3
	default:
		bits = perm
	}
	if bits&writeAndSearch == writeAndSearch {
		return unwritablePath{}, false
	}
	return unwritablePath{Path: dir, Mode: fi.Mode(), UID: dUID, GID: dGID}, true
}

// guestIDs resolves the uid and gid the guest command will actually run as.
//
// SANDBOX_RUN_AS before spec.User, and that order is the whole of it: in
// allowlist mode docker's --user is **root**, because the entrypoint has to
// program iptables before it drops. Reading spec.User there would answer for the
// root phase and conclude that everything is writable — on the mode that is now
// the dev default, so the check would be silently useless for most runs.
func guestIDs(spec runtime.RunSpec) (uid, gid int, ok bool) {
	user := spec.Env["SANDBOX_RUN_AS"]
	if user == "" {
		user = spec.User
	}
	switch user {
	case "", "root", "0":
		// Empty means the image default, which for the base image is root. Either
		// way uid 0 is not subject to the mode bits.
		return 0, 0, false
	case defaultRunAsUser:
		// The one name this process may resolve without reading the image: it is
		// the image's own user, created by assets/Dockerfile.
		u, _ := strconv.Atoi(sandboxUID)
		g, _ := strconv.Atoi(sandboxGID)
		return u, g, true
	}
	// Numeric uid or uid:gid, which is what sharedGroupUser renders and what
	// `--user 1000:1000` gives. A bare uid reuses itself as the gid, matching
	// docker's own reading.
	idPart, gidPart, split := strings.Cut(user, ":")
	u, err := strconv.Atoi(idPart)
	if err != nil {
		return 0, 0, false // a name from the image's passwd database; not ours to guess
	}
	g := u
	if split {
		if g, err = strconv.Atoi(gidPart); err != nil {
			return 0, 0, false
		}
	}
	if u == 0 {
		return 0, 0, false
	}
	return u, g, true
}

// unwritableReport renders the facts and the fix, shared by the two callers so
// the warning and the refusal describe the same host in the same words.
//
// The remedy names `chmod g+w` rather than a recursive chown, and
// core.sharedRepository for a repository, because those are the two that leave
// the host side working — the alternatives are the ones hostgroup.go already
// rejected, one directory further out.
func unwritableReport(bad []unwritablePath, uid, gid int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the container runs as uid %d gid %d, which cannot write:", uid, gid)
	git := false
	for _, u := range bad {
		fmt.Fprintf(&b, "\n  %s", u)
		if strings.Contains(u.Path, string(filepath.Separator)+"objects") {
			git = true
		}
	}
	b.WriteString("\n  the agent will report that it cannot edit files, and git will refuse to write objects")
	b.WriteString("\n  fix: chmod g+w " + bad[0].Path)
	if git {
		b.WriteString("\n  and, so git keeps making them that way: git config core.sharedRepository group")
	}
	return b.String()
}
