package sandbox

import (
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// Native Linux is the one place where a bind mount carries real uids, and that
// is what broke persisted logins there.
//
// The container user is uid 1001 — the base node image already holds 1000, so
// the image's `useradd` took the next free number. sandbox-cli's own state dirs
// are created on the host by the user running it, at 0700: the persisted agent
// HOME (config.AgentStateDir) and, for claude, this project's history bucket.
// Bind-mount one into the container and the kernel does the obvious thing:
//
//	$ docker run --rm -u 1001:1001 -v <persisted HOME>:/sandbox/home … ls /sandbox/home
//	ls: cannot open directory '/sandbox/home': Permission denied
//
// So the agent found no credentials ("not logged in"), and the login it then
// completed could not be written back — the token lived in memory and died with
// the container, which is why the next run asked again. macOS never showed it:
// Docker Desktop virtualizes bind ownership, so every file simply appears to
// belong to whoever is looking.
//
// The fix is a shared **group**, and the alternatives are worth recording
// because both are worse:
//
//   - chown the dirs to 1001. The container works; the host stops. Those dirs
//     are read from the host on purpose — `context list` and `usage` read the
//     persisted HOME, and the history bucket belongs to the host's own Claude
//     Code, which would lose write access to its own project history.
//   - run the container as the host uid. That is the other half of the same
//     coin, and it means an unwritable HOME for any run that does *not* mount a
//     persisted one, plus cache volumes seeded from 1001-owned image dirs.
//
// A group costs neither. The dirs keep their owner and gain group access
// (shareWithSandboxGroup, below); the container keeps uid 1001 and takes the
// host's primary gid as its own (sharedGroupUser). Both sides then reach the
// same files, and nothing else about the container moves — the image is
// untouched, /workspace ownership is what it always was, and podman is left
// alone because HostUserMapping already answers this for it.

// hostPrimaryGID is a var so tests can pin it. BuildSpec must be deterministic,
// and this is the one input that differs per machine — the same reason
// hostTimezone is a var.
var hostPrimaryGID = resolveHostPrimaryGID

// resolveHostPrimaryGID returns the gid the container should join, or "" when
// there is nothing to solve.
//
// Empty off Linux: Docker Desktop and podman machines virtualize bind
// ownership, so the ids never meet and rendering a gid would only make the
// argv differ by platform for no gain.
func resolveHostPrimaryGID() string {
	if goruntime.GOOS != "linux" {
		return ""
	}
	gid := os.Getgid()
	if gid <= 0 {
		// gid 0 means sandbox-cli itself is running as root, and a container
		// joining the root group is not a fix, it is a widening.
		return ""
	}
	return strconv.Itoa(gid)
}

// sharedGroupUser gives the default container user the host's primary group.
//
// Applied to the docker `--user` and to SANDBOX_RUN_AS alike, since in
// allowlist mode the container starts as root and the entrypoint drops to the
// second one — a fix applied to only one of them would hold for exactly half of
// the runs.
//
// Three things are left exactly as they were. An explicitly requested user is
// the caller saying what they want, and second-guessing it here would be the
// surprise. podman has HostUserMapping, which already maps container 1001 to
// the host user. And a host gid that already equals the image's own group
// changes nothing, so it renders nothing.
func sharedGroupUser(engine, user string) string {
	if engine == string(runtime.EnginePodman) || user != defaultRunAsUser {
		return user
	}
	gid := hostPrimaryGID()
	if gid == "" || gid == sandboxGID {
		return user
	}
	return sandboxUID + ":" + gid
}

// ShareWithSandboxGroup makes one sandbox-owned host directory reachable by the
// container user, by moving it to the group that user will run with and opening
// the group bits. A no-op wherever the ids do not meet (everywhere but Linux).
//
// The setgid bit is the half that keeps working after today: entries the
// container creates inside inherit the group, so the host can still read what
// the agent wrote without a second pass.
//
// Deliberately **not recursive**. The persisted agent HOME can hold a
// node_modules tree, and walking it on every run to fix files the container
// itself created — and therefore already owns — would be a real cost for no
// gain. What needs fixing is the boundary: the directory, which the host
// created, and the files directly inside it, which for the claude history
// bucket is what the host's own Claude Code writes and the sandbox has to be
// able to resume.
//
// Best-effort by design: a directory that cannot be adjusted is a worse run,
// not a failed one, and the caller has something better to do than abort.
func ShareWithSandboxGroup(dir string) {
	gid := hostPrimaryGID()
	if dir == "" || gid == "" {
		return
	}
	g, err := strconv.Atoi(gid)
	if err != nil {
		return
	}
	share(dir, g, true)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 {
			// A symlink's own mode is meaningless, and following one would let a
			// link inside a sandbox-owned directory redirect a chmod anywhere.
			continue
		}
		share(filepath.Join(dir, e.Name()), g, e.IsDir())
	}
}

// share moves one path into the group and opens the group bits, skipping the
// work when it is already done — this runs on every launch, so the common case
// is "nothing to do" and it should cost one stat.
func share(path string, gid int, isDir bool) {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&fs.ModeSymlink != 0 {
		return
	}
	want := fi.Mode().Perm() | 0o060 // g+rw
	if isDir {
		want |= 0o010 // g+x: a directory is unusable without it
	}
	mode := fs.FileMode(want)
	if isDir {
		mode |= os.ModeSetgid
	}

	if cur, ok := ownerGID(fi); !ok || cur != gid {
		// -1 leaves the owner alone: the point is to share the directory, not to
		// hand it over. Permitted for the owner as long as they are a member of
		// the target group, which they are — it is their own primary group.
		_ = os.Chown(path, -1, gid)
	}
	if fi.Mode()&(fs.ModePerm|os.ModeSetgid) != mode {
		_ = os.Chmod(path, mode)
	}
}
