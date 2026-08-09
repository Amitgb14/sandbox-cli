package sandbox

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/config"
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
// Three things this gets wrong if built naively, all learned by review:
//
//   - **A repository's mount is its worktree, not its .git.** For an ordinary
//     checkout the mount source is the project root and `.git` sits inside it, so
//     looking for an object store at the mount source finds one only for the
//     linked-worktree `.git` mount — that is, only in the case that is *not* the
//     common one. gitDirOf handles both spellings.
//   - **A tree is not its top directory.** The umask that left the root at 0755
//     left every directory under it at 0755, so checking the root alone and then
//     printing a non-recursive `chmod g+w <root>` produces a fix that silences
//     the warning and changes nothing else. The walk goes down, and the remedy
//     is recursive.
//   - **`chmod g+w` is only the fix when the group is the class that failed.**
//     A directory owned by another group entirely needs the group changed first,
//     and printing g+w there is advice that cannot work.
//
// It detects and never repairs. That is the same bargain EnsureGuestDir makes
// with a foreign-owned level, and for the same reason — the state can be
// described exactly, and fixing it would mean writing to a tree the user owns.

// walkBudget bounds how many directories one mount is examined for. The walk
// stops at the first offender, so a broken tree is answered almost immediately
// and only an entirely healthy one pays the full cost — and a repository with a
// node_modules is not worth a hundred thousand stats on every launch.
//
// The consequence is stated rather than hidden: a problem deeper than the budget
// is not found, so this reports what it saw and never claims the tree is clean.
const walkBudget = 4096

// unwritablePath is one host directory the container user cannot create files
// in, with the facts that decided it — including which permission class failed,
// because that is what decides whether the remedy is a chmod or a chgrp.
type unwritablePath struct {
	Root  string // the mounted directory this was found under; what the remedy names
	Path  string // the offending directory itself, which may be Root
	Mode  fs.FileMode
	UID   int
	GID   int
	Group bool // the container's gid owns it, so opening the group bits is enough
	Git   bool // found inside a git object store
}

// String is the one-line form the report is built from.
func (u unwritablePath) String() string {
	return fmt.Sprintf("%s is mode %#o owned by %d:%d", u.Path, u.Mode.Perm(), u.UID, u.GID)
}

// writeCheck is the outcome for a whole run: what was found unwritable, or why
// the question could not be put at all.
//
// Unknown is not an empty Bad. A run whose guest user this process cannot
// resolve has not been checked, and saying so is what lets prod apply its own
// rule — "a question that could not be asked counts as a failure under prod too"
// — instead of reading silence as an all-clear.
type writeCheck struct {
	Bad     []unwritablePath
	Unknown string
}

// checkWritableMounts asks, for every read-write host directory this run will
// mount, whether the container user can create files in it.
//
// Three cases answer "does not arise", and each is a place where the ids do not
// meet the way this check assumes:
//
//   - Where there is no host primary gid to share. That is off Linux, where
//     Docker Desktop virtualizes bind-mount ownership and the host mode decides
//     nothing, and it is also the seam that keeps this honest under test.
//   - Under podman. HostUserMapping maps the host user onto the container user,
//     so a directory that user owns is writable by construction.
//   - When the guest runs as root. uid 0 is not subject to the mode bits.
//
// Keyed on hostPrimaryGID rather than on runtime.GOOS, and the difference is not
// cosmetic. This is the one check that compares ids **from the spec** against
// ownership **on the real filesystem**, so it is sound only when those ids are
// the real ones. BuildSpec is deliberately deterministic under test — TestMain
// pins hostPrimaryGID to "" for the whole package, so a spec built there names
// uid 1001 gid 1001 whatever the machine is — and judging a real t.TempDir()
// against those synthetic ids made every test that builds a spec depend on the
// uid of whoever ran it. It passed on CI, whose runner happens to be uid 1001,
// and failed on an ordinary workstation.
func checkWritableMounts(spec runtime.RunSpec, engine string) writeCheck {
	if hostPrimaryGID() == "" || engine == string(runtime.EnginePodman) {
		return writeCheck{}
	}
	uid, gid, state := guestIDs(spec)
	switch state {
	case idsRoot:
		return writeCheck{}
	case idsUnresolved:
		return writeCheck{Unknown: fmt.Sprintf(
			"the container user %q is a name only the image's passwd database defines, so this "+
				"process cannot tell which uid and gid the guest will have", guestUser(spec))}
	}

	var out writeCheck
	seen := map[string]bool{}
	for _, m := range spec.Mounts {
		// A named volume has no host path to judge, and a read-only mount is not
		// one the agent was promised it could write.
		if m.Volume || m.RO || seen[m.Source] {
			continue
		}
		seen[m.Source] = true
		out.Bad = append(out.Bad, checkTree(m.Source, uid, gid)...)
	}
	return out
}

// checkTree walks one mounted directory, and the repository inside it, for the
// first place the container user cannot write.
//
// Two answers rather than one, because they have different remedies and a user
// who fixes only the one they were shown is left where they started: the working
// tree, where the agent edits files, and the object store, where git writes.
func checkTree(root string, uid, gid int) []unwritablePath {
	gitDir, hasGit := gitDirOf(root)
	// The working-tree walk steps over the repository directory when it is inside
	// it, so a bad object directory is reported once, by the walk that knows the
	// remedy for it, rather than twice with two different fixes.
	skip := ""
	if hasGit && gitDir != root {
		skip = gitDir
	}

	var out []unwritablePath
	if u, ok := firstUnwritable(root, root, uid, gid, skip); ok {
		out = append(out, u)
	}
	if !hasGit {
		return out
	}
	// The object store is walked separately even when the tree above was clean:
	// git creates those 256 fan-out directories on demand, under whatever umask
	// the host had at the time, so a repository can be years old and half of them
	// wrong while everything else is fine.
	if u, ok := firstUnwritable(filepath.Join(gitDir, "objects"), gitDir, uid, gid, ""); ok {
		u.Git = true
		out = append(out, u)
	}
	return out
}

// gitDirOf returns the repository directory to examine for dir, covering both
// ways a mount can name one.
//
// An ordinary checkout mounts its working tree, with `.git` a directory inside
// it. A linked worktree mounts the parent repository's `.git` at its own host
// path, and that path *is* the git directory — while the worktree's own `.git`
// is a pointer file, whose target is already mounted separately and so already
// checked as a mount in its own right.
func gitDirOf(dir string) (string, bool) {
	if isExistingDir(filepath.Join(dir, "objects")) && isExistingDir(filepath.Join(dir, "refs")) {
		return dir, true // the mount is itself a git directory
	}
	inner := filepath.Join(dir, ".git")
	if isExistingDir(filepath.Join(inner, "objects")) && isExistingDir(filepath.Join(inner, "refs")) {
		return inner, true
	}
	return "", false
}

// firstUnwritable walks from dir and returns the first directory the container
// user cannot create a file in, attributing it to root for the remedy.
//
// Depth-first and bounded by walkBudget. It stops at the first offender because
// the fix is the same for all of them and a list of forty is one nobody reads —
// and because the common broken case answers on the very first stat.
func firstUnwritable(dir, root string, uid, gid int, skip string) (unwritablePath, bool) {
	var found unwritablePath
	var ok bool
	budget := walkBudget

	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			// An unreadable entry is a question that could not be asked rather than
			// an answer of "no"; skipping it keeps this from reporting a directory
			// it never saw.
			return nil //nolint:nilerr // deliberate: see above
		}
		if skip != "" && path == skip {
			return filepath.SkipDir
		}
		if budget--; budget <= 0 {
			return filepath.SkipAll
		}
		u, bad := unwritableBy(path, uid, gid)
		if !bad {
			return nil
		}
		u.Root = root
		found, ok = u, true
		return filepath.SkipAll
	})
	return found, ok
}

// unwritableBy reports whether uid/gid can create a file in dir, by the rule the
// kernel uses: the first of owner/group/other that matches decides, and creating
// an entry needs both write and search on the directory.
//
// A directory carrying a POSIX ACL is reported writable — which is to say, not
// reported. The mode bits do not describe access there, so judging by them is
// how an ACL-managed workspace gets refused for a permission it actually has;
// and the honest failure direction for a check that gates a run is to miss a
// problem rather than to invent one.
func unwritableBy(dir string, uid, gid int) (unwritablePath, bool) {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return unwritablePath{}, false
	}
	dUID, okU := ownerUID(fi)
	dGID, okG := ownerGID(fi)
	if !okU || !okG || hasACL(dir) {
		return unwritablePath{}, false
	}
	perm := fi.Mode().Perm()

	const writeAndSearch = 0o3 // w|x, in whichever class applies
	var bits fs.FileMode
	group := false
	switch {
	case dUID == uid:
		bits = perm >> 6
	case dGID == gid:
		bits = perm >> 3
		group = true
	default:
		bits = perm
	}
	if bits&writeAndSearch == writeAndSearch {
		return unwritablePath{}, false
	}
	return unwritablePath{Path: dir, Mode: fi.Mode(), UID: dUID, GID: dGID, Group: group}, true
}

// idState is how much guestIDs could establish, and the three answers are three
// different things for a caller to do.
type idState int

const (
	idsResolved   idState = iota // the guest's uid and gid are known
	idsRoot                      // the guest is root, which the mode bits do not constrain
	idsUnresolved                // a name this process cannot map to numbers
)

// guestUser is the user string the guest command will actually run as.
//
// SANDBOX_RUN_AS before spec.User, and that order is the whole of it: in
// allowlist mode docker's --user is **root**, because the entrypoint has to
// program iptables before it drops. Reading spec.User there would answer for the
// root phase and conclude that everything is writable — on the mode that is now
// the dev default, so the check would be silently useless for most runs.
func guestUser(spec runtime.RunSpec) string {
	if u := spec.Env["SANDBOX_RUN_AS"]; u != "" {
		return u
	}
	return spec.User
}

// guestIDs resolves the uid and gid the guest command will run as.
//
// A **bare numeric uid** the image does not define is deliberately unresolved
// rather than assumed to give the same gid. docker resolves `--user 5000`
// against the image's passwd database and falls back to gid **0** when it finds
// nothing — so guessing 5000:5000 judges the group class against a gid the
// container will never have, and can refuse a directory root could write.
func guestIDs(spec runtime.RunSpec) (uid, gid int, state idState) {
	user := guestUser(spec)
	switch user {
	case "", "root", "0", "0:0":
		// Empty means the image default, which for the base image is root.
		return 0, 0, idsRoot
	case defaultRunAsUser:
		// The one name this process may resolve without reading the image: it is
		// the image's own user, created by assets/Dockerfile.
		u, _ := strconv.Atoi(sandboxUID)
		g, _ := strconv.Atoi(sandboxGID)
		return u, g, idsResolved
	}
	idPart, gidPart, split := strings.Cut(user, ":")
	u, err := strconv.Atoi(idPart)
	if err != nil {
		return 0, 0, idsUnresolved // a name from the image's passwd database
	}
	if u == 0 {
		return 0, 0, idsRoot
	}
	if !split {
		// See the doc comment: only the image's own uid has a gid we can name.
		if idPart == sandboxUID {
			g, _ := strconv.Atoi(sandboxGID)
			return u, g, idsResolved
		}
		return 0, 0, idsUnresolved
	}
	g, err := strconv.Atoi(gidPart)
	if err != nil {
		return 0, 0, idsUnresolved
	}
	return u, g, idsResolved
}

// unwritableReport renders the facts and the fix, shared by the warning and the
// refusal so the two describe the same host in the same words.
//
// The remedy is **recursive**, and that is the correction to the first version
// of this: the umask that left the root at 0755 left everything under it at 0755
// too, so a non-recursive chmod fixes the one directory that was reported and
// silences the check while the agent still cannot write anything deeper.
//
// It also depends on which class failed. `chmod g+w` is the fix only when the
// container's gid already owns the directory; when some other group does,
// opening the group bits changes nothing for the container and the group has to
// change first.
func unwritableReport(bad []unwritablePath, uid, gid int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the container runs as uid %d gid %d, which cannot write:", uid, gid)
	git := false
	for _, u := range bad {
		fmt.Fprintf(&b, "\n  %s", u)
		git = git || u.Git
	}
	b.WriteString("\n  the agent will report that it cannot edit files")
	if git {
		b.WriteString(", and git will refuse to write objects")
	}
	for _, u := range bad {
		if u.Group {
			fmt.Fprintf(&b, "\n  fix: chmod -R g+w %s", u.Root)
		} else {
			fmt.Fprintf(&b, "\n  fix: chgrp -R %d %s && chmod -R g+w %s", gid, u.Root, u.Root)
		}
	}
	if git {
		b.WriteString("\n  and, so git keeps making them that way: git config core.sharedRepository group")
	}
	return b.String()
}

// warnedWritable is where the dev-profile warning goes. A var so tests can read
// what was printed, the same as warnedSecret and warnedImage.
//
// Here rather than beside enforceWritableMounts in sandbox.go, where the first
// version of it landed in the middle of enforceSeccomp's doc comment and left
// that function undocumented — in a file whose comments are the design record.
var warnedWritable = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}

// warnedTrees records the reports already said, so a twenty-task fleet resolving
// the same mounts says this once rather than twenty times. Same argument as
// warnedNames and warnedImages, and the same lock rationale: studioapi calls
// Start from an HTTP handler.
//
// Keyed on the report rather than on the path, so a workspace that is fixed
// mid-session and breaks again differently is reported again.
var (
	warnedTreeMu sync.Mutex
	warnedTrees  = map[string]bool{}
)

// enforceWritableMounts applies the profiles' asymmetry to one host fact: the
// container user cannot write a directory this run is about to mount read-write.
//
// dev warns and runs; prod refuses, because nobody is watching a prod run and an
// agent silently unable to write is exactly the degraded-quietly failure the
// profile exists to prevent.
//
// The unknown case is separate and is prod's own rule, stated in CLAUDE.md: a
// question that could not be asked counts as a failure under prod too, because
// it does not get to assume the answer it would prefer. dev stays quiet there
// instead of warning, matching what doctor does with a question it could not
// put — a warning nobody can act on is one they learn to skip.
//
// What keeps the refusal honest is that the check no longer answers where it
// cannot see: a directory carrying a POSIX ACL is not reported at all, so prod
// is not refusing on bits that stopped deciding. That was the objection to the
// first version, where the same doc comment gave ACL blindness as the reason dev
// must not refuse and then refused under prod on the strength of it.
func (s *Session) enforceWritableMounts(spec runtime.RunSpec) error {
	prod := s.Cfg.Profile == config.ProfileProd
	res := checkWritableMounts(spec, s.Cfg.Engine)

	if res.Unknown != "" {
		if prod {
			return fmt.Errorf("cannot confirm the container can write this run's mounts: %s\n"+
				"  prod does not assume the answer it would prefer; name the user as uid:gid, "+
				"or run with --profile dev", res.Unknown)
		}
		return nil
	}
	if len(res.Bad) == 0 {
		return nil
	}

	uid, gid, _ := guestIDs(spec)
	report := unwritableReport(res.Bad, uid, gid)
	if prod {
		return fmt.Errorf("%s\n  or run with --profile dev, which warns instead of refusing", report)
	}
	warnedTreeMu.Lock()
	said := warnedTrees[report]
	warnedTrees[report] = true
	warnedTreeMu.Unlock()
	if !said {
		warnedWritable("sandbox-cli: %s\n", report)
	}
	return nil
}
