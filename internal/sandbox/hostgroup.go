package sandbox

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/image"
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
//
// The group is only half of it, and the missing half showed up as a git error
// rather than a permissions one:
//
//	$ git commit -s -m …
//	fatal: could not open '…/.git/worktrees/docs/COMMIT_EDITMSG': Permission denied
//
// A container had committed in that worktree first, and git opens
// COMMIT_EDITMSG for writing on every commit — including `-m`. Membership in the
// group decides who *may* write; the umask decides whether the bits are there to
// write with, and a container inherits 0022 from whatever started it, which
// strips exactly the bit this whole mechanism exists to grant. So every file the
// container created came back g-w and the host could not touch it, while
// share() below was carefully setting g+rw on the directories around it.
//
// sharedGroupUmask is the answer, and it is deliberately not a new trust
// decision: share() already opens g+rw on these paths, so applying the same mask
// to files created during the run makes one existing decision consistent instead
// of adding a second. It fires on exactly the runs where a shared group is in
// play — which is sharedGroupGID, not sharedGroupUser: see the comment there for
// why the two are not the same set of runs.
//
// Two costs are accepted rather than solved, because a umask is a property of a
// process and cannot be scoped to a path:
//
//   - It applies to the whole container, not only to the host paths it was
//     reasoned about. On a host whose primary group is a *shared* one — SUSE and
//     many LDAP/NIS sites put every account in `users` — that widens what the
//     agent writes to every account in it. sharedGroupUser already puts the
//     container in that group and share() already opens g+rw on the persisted
//     HOME, so this widens an existing exposure rather than creating one; the
//     way to decline it is an explicit `--user`, which turns the whole mechanism
//     off.
//   - Tools that refuse a group-writable config refuse one the agent creates
//     during the run: ssh rejects a 0664 ~/.ssh/config outright ("Bad owner or
//     permissions"), and gpg warns about a 0775 homedir. Both are the tool
//     applying its own policy to a file the agent wrote, not a broken container.
//
// Neither is worth reinstating the reported bug for, and the alternative that
// would solve both — default ACLs on the shared directories instead of a mask —
// needs acl support on the host filesystem and a second mechanism to maintain.

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

// sharedGroupGID is the group the container and the host user will both be in,
// or "" when there is no shared group at all.
//
// This is the question both halves of the mechanism actually depend on, so it is
// asked in one place rather than re-derived by each. Two runs answer "none". An
// explicitly requested user is the caller saying what they want, and
// second-guessing it here would be the surprise; podman has HostUserMapping,
// which already maps container 1001 to the host user. Neither half applies to
// either.
func sharedGroupGID(engine, user string) string {
	if engine == string(runtime.EnginePodman) || user != defaultRunAsUser {
		return ""
	}
	return hostPrimaryGID()
}

// sharedGroupUser gives the default container user the host's primary group.
//
// Applied to the docker `--user` and to SANDBOX_RUN_AS alike, since in
// allowlist mode the container starts as root and the entrypoint drops to the
// second one — a fix applied to only one of them would hold for exactly half of
// the runs.
//
// The extra condition here is the one that does **not** generalize: a host gid
// that already equals the image's own group needs no `--user` override, because
// the container would join it anyway. That is a statement about what to render,
// not about whether the group is shared — on such a host it emphatically is, and
// sharedGroupUmask must still fire. Deriving the umask from "did this function
// change the string" got that backwards and left the reported bug alive on every
// machine whose primary gid is 1001.
func sharedGroupUser(engine, user string) string {
	gid := sharedGroupGID(engine, user)
	if gid == "" || gid == sandboxGID {
		return user
	}
	return sandboxUID + ":" + gid
}

// sharedGroupUmask is the umask the container must run with for the shared group
// to still mean anything after the first file is written, or "" when there is no
// shared group.
//
// Keyed on sharedGroupGID rather than on sharedGroupUser's output: the group is
// what the mask is for, and the user string is only how one of the two ways of
// joining it gets rendered. A container in the group but at 0022 writes files
// the host cannot edit, which is the reported bug; a container at 0002 with no
// shared group opens the mode for a group nobody involved is in. So both must
// key on the same fact, and this is that fact.
//
// 0002 rather than 0007: group-write is the point, and the world bits are left
// exactly where the default put them. This never touches the owner bits, so
// nothing the container writes becomes less private to it.
func sharedGroupUmask(engine, user string) string {
	if sharedGroupGID(engine, user) == "" {
		return ""
	}
	return sharedGroupUmaskValue
}

// sharedGroupUmaskValue is octal and stays a string: it is passed to the
// container's `umask`, which reads octal, and rendering a Go int would invite
// somebody to write it as a decimal literal.
const sharedGroupUmaskValue = "0002"

// warnedImage is where the warning below is written. A var so tests can read what
// was printed, matching warnedSecret; deliberately dumb otherwise.
var warnedImage = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}

// warnedImages records the images already warned about, so a fleet resolving the
// same config for twenty tasks says this once rather than twenty times — the
// reason warnedNames exists, and the same lock rationale: studioapi calls Start
// from an HTTP handler.
var (
	warnedImageMu sync.Mutex
	warnedImages  = map[string]bool{}
)

// warnUmaskNeedsSandboxInit says the one thing sandbox-cli knows and the user
// cannot see: the shared group was applied and the umask that has to travel with
// it was not, because SANDBOX_UMASK is read by sandbox-init and a user-supplied
// image has no reason to carry that script.
//
// The pair being incomplete is exactly the half-fixed state the pairing rule
// calls worse than neither, and here it cannot be prevented — the mask has
// nowhere to be applied. Rendering `--entrypoint` to force it was the obvious
// alternative and is wrong: an image with no sandbox-init would then be handed
// an entrypoint it cannot run, turning a mode problem into a container that does
// not start.
//
// It warns rather than refuses because a custom image is an ordinary, supported
// thing to run, and because this is not a regression — that run had the
// inherited 0022 before this mechanism existed too. What it did not have was the
// CHANGELOG saying the problem was fixed.
func warnUmaskNeedsSandboxInit(spec runtime.RunSpec) {
	if spec.Env["SANDBOX_UMASK"] == "" || spec.Image == image.Ref() {
		return
	}
	warnedImageMu.Lock()
	seen := warnedImages[spec.Image]
	warnedImages[spec.Image] = true
	warnedImageMu.Unlock()
	if seen {
		return
	}
	warnedImage(
		"sandbox-cli: image %s does not carry sandbox-init, so the container cannot be "+
			"given umask %s. It still runs in your primary group, but files it creates will "+
			"be group-read-only and you will not be able to edit them from the host. Build "+
			"the image FROM the sandbox base, or expect to fix modes afterwards.\n",
		spec.Image, sharedGroupUmaskValue)
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
