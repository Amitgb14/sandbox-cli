package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// TestGuestIDs covers the whole resolution table. Two rows are the ones this got
// wrong before: SANDBOX_RUN_AS must outrank a root --user, and a bare numeric uid
// the image does not define must not be assumed to give the same gid.
func TestGuestIDs(t *testing.T) {
	tests := []struct {
		name     string
		spec     runtime.RunSpec
		uid, gid int
		state    idState
	}{
		{
			// In allowlist mode — the dev default — docker's --user is root because
			// the entrypoint programs iptables before it drops. Answering for the
			// root phase finds every path writable and the check does nothing.
			name:  "allowlist: SANDBOX_RUN_AS outranks a root --user",
			spec:  runtime.RunSpec{User: "root", Env: map[string]string{"SANDBOX_RUN_AS": "1001:1000"}},
			uid:   1001,
			gid:   1000,
			state: idsResolved,
		},
		{
			name:  "default mode: the docker --user is the answer",
			spec:  runtime.RunSpec{User: "1001:1000"},
			uid:   1001,
			gid:   1000,
			state: idsResolved,
		},
		{
			name:  "the image's own user is the one name resolvable from here",
			spec:  runtime.RunSpec{User: "sandbox"},
			uid:   1001,
			gid:   1001,
			state: idsResolved,
		},
		{
			// docker resolves a bare uid against the image's passwd database and
			// falls back to gid 0 when it finds nothing, so 5000:5000 is a guess
			// that can refuse a directory the container could write.
			name:  "a bare uid the image does not define has no gid we can name",
			spec:  runtime.RunSpec{User: "5000"},
			state: idsUnresolved,
		},
		{
			name:  "a bare uid the image does define is the sandbox user",
			spec:  runtime.RunSpec{User: "1001"},
			uid:   1001,
			gid:   1001,
			state: idsResolved,
		},
		{
			name:  "root is not subject to the mode bits",
			spec:  runtime.RunSpec{User: "root"},
			state: idsRoot,
		},
		{
			name:  "uid 0 spelled numerically is still root",
			spec:  runtime.RunSpec{User: "0:0"},
			state: idsRoot,
		},
		{
			name:  "an empty user means the image default, which is root",
			spec:  runtime.RunSpec{},
			state: idsRoot,
		},
		{
			name:  "a name only the image's passwd database defines is unresolved, not root",
			spec:  runtime.RunSpec{User: "builder"},
			state: idsUnresolved,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uid, gid, state := guestIDs(tc.spec)
			if state != tc.state {
				t.Fatalf("state = %v, want %v", state, tc.state)
			}
			if state == idsResolved && (uid != tc.uid || gid != tc.gid) {
				t.Errorf("ids = %d:%d, want %d:%d", uid, gid, tc.uid, tc.gid)
			}
		})
	}
}

// TestUnwritableBy is the arithmetic the whole check rests on, exercised against
// real directories: the point is that it agrees with what the kernel would do,
// and only a real stat can be wrong about that.
//
// 0755 vs 0775 is not an arbitrary pair. It is umask 022 against umask 002 — the
// difference between a host where the agent has a read-only workspace and one
// where it does not, which is the whole of issue #80.
func TestUnwritableBy(t *testing.T) {
	requireOwnership(t)
	me, myGroup := os.Getuid(), os.Getgid()
	const otherUID = 999999 // certainly not us, and not a group we are in

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, bad := unwritableBy(dir, me, myGroup); bad {
		t.Error("0755 must be writable by its own owner; the owner class decides first")
	}
	u, bad := unwritableBy(dir, otherUID, myGroup)
	if !bad {
		t.Fatal("0755 must NOT be writable through the group — the umask 022 case, and the whole of issue #80")
	}
	if !u.Group {
		t.Error("the group class is what failed here, and the remedy depends on knowing that")
	}

	if err := os.Chmod(dir, 0o775); err != nil {
		t.Fatal(err)
	}
	if _, bad := unwritableBy(dir, otherUID, myGroup); bad {
		t.Error("0775 must be writable through the group — the umask 002 case, which works today")
	}

	// Write without search cannot create an entry, and a mode granting one
	// without the other is what a w-only check would miss.
	if err := os.Chmod(dir, 0o764); err != nil {
		t.Fatal(err)
	}
	if _, bad := unwritableBy(dir, otherUID, myGroup); !bad {
		t.Error("g=rw with no search must be reported: a directory cannot be written without traversing it")
	}

	// A group we are not in falls to the other class, and there the remedy is not
	// a chmod — which is why Group has to be false.
	if err := os.Chmod(dir, 0o775); err != nil {
		t.Fatal(err)
	}
	u, bad = unwritableBy(dir, otherUID, 999998)
	if !bad {
		t.Fatal("0775 must not be writable by a gid that owns nothing here")
	}
	if u.Group {
		t.Error("the group did not match, so `chmod g+w` is not the fix and Group must say so")
	}

	if _, bad := unwritableBy(filepath.Join(dir, "nope"), otherUID, myGroup); bad {
		t.Error("a missing path must not be reported as unwritable")
	}
}

// TestCheckTreeFindsAnOrdinaryRepositorysObjectStore is the regression for the
// bug that made the git half of this check dead code: for a normal checkout the
// mount is the *working tree* and .git sits inside it, so looking for an object
// store at the mount source found one only for a linked worktree — the case that
// is not the common one.
func TestCheckTreeFindsAnOrdinaryRepositorysObjectStore(t *testing.T) {
	requireOwnership(t)
	const otherUID = 999999
	myGroup := os.Getgid()

	// A working tree whose own directories are fine, with one bad fan-out inside
	// .git — exactly what git leaves when the umask was 022 for one write.
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	fanout := filepath.Join(gitDir, "objects", "ab")
	for _, d := range []string{fanout, filepath.Join(gitDir, "refs"), filepath.Join(root, "src")} {
		if err := os.MkdirAll(d, 0o775); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{root, gitDir, filepath.Join(gitDir, "objects"), filepath.Join(root, "src")} {
		if err := os.Chmod(d, 0o775); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(fanout, 0o755); err != nil {
		t.Fatal(err)
	}

	got := checkTree(root, otherUID, myGroup)
	if len(got) != 1 {
		t.Fatalf("checkTree = %+v, want exactly the fan-out directory", got)
	}
	if got[0].Path != fanout {
		t.Errorf("found %s, want %s", got[0].Path, fanout)
	}
	if !got[0].Git {
		t.Error("the finding must be marked as coming from an object store; the git remedy is gated on it")
	}
	if got[0].Root != gitDir {
		t.Errorf("remedy root = %s, want the git directory %s", got[0].Root, gitDir)
	}

	// A linked worktree mounts the parent .git at its own path, so the mount
	// source *is* the git directory. Both spellings have to work.
	if got := checkTree(gitDir, otherUID, myGroup); len(got) == 0 {
		t.Error("a mount that is itself a git directory must still have its object store checked")
	}

	// And a plain directory is not searched for a repository it does not have.
	plain := t.TempDir()
	if err := os.Chmod(plain, 0o775); err != nil {
		t.Fatal(err)
	}
	if got := checkTree(plain, otherUID, myGroup); len(got) != 0 {
		t.Errorf("checkTree on a non-git directory = %+v, want nothing", got)
	}
}

// TestCheckTreeLooksBelowTheTopDirectory is the regression for the fix that was
// worse than the bug: checking only the mount root, then printing a
// non-recursive `chmod g+w <root>`, gave a remedy that silenced the check while
// every directory under it stayed unwritable.
func TestCheckTreeLooksBelowTheTopDirectory(t *testing.T) {
	requireOwnership(t)
	const otherUID = 999999
	myGroup := os.Getgid()

	root := t.TempDir()
	deep := filepath.Join(root, "internal", "cli")
	if err := os.MkdirAll(deep, 0o775); err != nil {
		t.Fatal(err)
	}
	// The root is fine — the state a user is left in after running the old advice.
	for _, d := range []string{root, filepath.Join(root, "internal")} {
		if err := os.Chmod(d, 0o775); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got := checkTree(root, otherUID, myGroup)
	if len(got) != 1 || got[0].Path != deep {
		t.Fatalf("checkTree = %+v, want the deep directory %s: a fixed root is not a fixed tree", got, deep)
	}
	if got[0].Root != root {
		t.Errorf("remedy root = %s, want the mount %s", got[0].Root, root)
	}

	// And the remedy that names it has to be recursive, or following it lands the
	// user right back here.
	report := unwritableReport(got, 1001, myGroup)
	if !strings.Contains(report, "chmod -R g+w "+root) {
		t.Errorf("remedy must be recursive and name the mount root, got:\n%s", report)
	}
}

// TestUnwritableReportMatchesTheFailingClass: `chmod g+w` fixes nothing when the
// directory belongs to a group the container is not in, and printing it there is
// advice a user can follow twice and get the same refusal.
func TestUnwritableReportMatchesTheFailingClass(t *testing.T) {
	group := []unwritablePath{{Root: "/srv/repo", Path: "/srv/repo", Mode: 0o755, UID: 1000, GID: 1000, Group: true}}
	if got := unwritableReport(group, 1001, 1000); !strings.Contains(got, "chmod -R g+w /srv/repo") {
		t.Errorf("a group-class failure is fixed by opening the group bits, got:\n%s", got)
	} else if strings.Contains(got, "chgrp") {
		t.Errorf("no chgrp is needed when the group already matches, got:\n%s", got)
	}

	other := []unwritablePath{{Root: "/srv/repo", Path: "/srv/repo", Mode: 0o775, UID: 1000, GID: 4000}}
	got := unwritableReport(other, 1001, 1000)
	if !strings.Contains(got, "chgrp -R 1000 /srv/repo") {
		t.Errorf("a directory owned by another group needs the group changed first, got:\n%s", got)
	}

	// The git advice is gated on an actual object-store finding, not on the path
	// happening to contain the word.
	if strings.Contains(got, "core.sharedRepository") {
		t.Errorf("git advice must not appear for a non-git finding, got:\n%s", got)
	}
	objects := []unwritablePath{{Root: "/data/objects/svc", Path: "/data/objects/svc", Mode: 0o755, UID: 1000, GID: 1000, Group: true}}
	if got := unwritableReport(objects, 1001, 1000); strings.Contains(got, "core.sharedRepository") {
		t.Errorf("a path merely containing /objects is not a repository, got:\n%s", got)
	}
}

// TestCheckWritableMountsSkipsWhereTheIdsDoNotMeet pins the cases where the
// question does not arise. Each is a host where the mode on this side is not
// what decides access on the other, so an answer computed here would be a guess.
func TestCheckWritableMountsSkipsWhereTheIdsDoNotMeet(t *testing.T) {
	requireLinux(t)
	// TestMain pins this off for the package so BuildSpec stays deterministic;
	// the check keys on it, so a test about the check has to put a real one back.
	pinHostGID(t, fmt.Sprint(os.Getgid()))
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := runtime.RunSpec{
		User:   "1001:" + fmt.Sprint(os.Getgid()),
		Mounts: []runtime.Mount{{Source: dir, Target: "/workspace"}},
	}

	if got := checkWritableMounts(spec, "podman"); len(got.Bad) != 0 || got.Unknown != "" {
		t.Errorf("podman = %+v, want nothing: keep-id maps the host user onto the container user", got)
	}

	rootSpec := spec
	rootSpec.User = "root"
	if got := checkWritableMounts(rootSpec, ""); len(got.Bad) != 0 || got.Unknown != "" {
		t.Errorf("root guest = %+v, want nothing: uid 0 is not subject to the mode bits", got)
	}

	roSpec := spec
	roSpec.Mounts = []runtime.Mount{{Source: dir, Target: "/workspace", RO: true}}
	if got := checkWritableMounts(roSpec, ""); len(got.Bad) != 0 {
		t.Errorf("read-only mount = %+v, want nothing: nobody was promised they could write it", got)
	}

	volSpec := spec
	volSpec.Mounts = []runtime.Mount{{Source: "sandbox-cache-npm", Target: "/cache", Volume: true}}
	if got := checkWritableMounts(volSpec, ""); len(got.Bad) != 0 {
		t.Errorf("named volume = %+v, want nothing: there is no host path to judge", got)
	}

	// An unresolvable user is *not* a clean bill of health, and the difference is
	// what lets prod refuse rather than read silence as an all-clear.
	nameSpec := spec
	nameSpec.User = "builder"
	got := checkWritableMounts(nameSpec, "")
	if got.Unknown == "" {
		t.Error("a user this process cannot resolve must be reported unknown, not writable")
	}
	if len(got.Bad) != 0 {
		t.Errorf("an unknown must carry no findings: %+v", got.Bad)
	}
}

// TestEnforceWritableMounts is the profile asymmetry: the same host fact warns
// under dev and refuses under prod, which is the one difference of kind between
// the two profiles — plus the unknown case, where prod's own documented rule is
// that a question it could not ask counts as a failure.
func TestEnforceWritableMounts(t *testing.T) {
	requireLinux(t)
	if os.Getuid() == 0 {
		t.Skip("running as root, which can write anything")
	}
	if os.Getuid() == 1001 {
		t.Skip("this host's user is the container's uid; the owner class would decide")
	}
	pinHostGID(t, fmt.Sprint(os.Getgid())) // see the note in the test above
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil { // umask 022: the reported case
		t.Fatal(err)
	}
	spec := runtime.RunSpec{
		// A different uid in our own group, which is what sharedGroupUser renders.
		User:   "1001:" + fmt.Sprint(os.Getgid()),
		Mounts: []runtime.Mount{{Source: dir, Target: "/workspace"}},
	}
	said := captureWritableWarning(t)

	dev := &Session{Cfg: config.Config{Profile: config.ProfileDev}}
	if err := dev.enforceWritableMounts(spec); err != nil {
		t.Fatalf("dev must warn and run, not refuse: %v", err)
	}
	if len(*said) != 1 {
		t.Fatalf("dev said %d warnings, want 1: %q", len(*said), *said)
	}
	if !strings.Contains((*said)[0], dir) || !strings.Contains((*said)[0], "chmod -R g+w") {
		t.Errorf("the warning must name the path and a recursive fix, got %q", (*said)[0])
	}

	// Said once per process. A twenty-task fleet resolving the same mounts must
	// not print the same block twenty times, which is what warnedNames and
	// warnedImages already exist in this package to prevent.
	if err := dev.enforceWritableMounts(spec); err != nil {
		t.Fatal(err)
	}
	if len(*said) != 1 {
		t.Errorf("said it %d times, want 1: a repeated warning is one nobody reads", len(*said))
	}

	prod := &Session{Cfg: config.Config{Profile: config.ProfileProd}}
	err := prod.enforceWritableMounts(spec)
	if err == nil {
		t.Fatal("prod must refuse: nobody is watching, and an agent that cannot write fails quietly")
	}
	if !strings.Contains(err.Error(), "--profile dev") {
		t.Errorf("a prod refusal must say how to proceed anyway, got %q", err)
	}

	// A question that could not be asked: prod refuses, dev stays quiet rather
	// than warning about something nobody can act on.
	unknown := spec
	unknown.User = "builder"
	if err := prod.enforceWritableMounts(unknown); err == nil {
		t.Error("prod must refuse an unanswerable question; it does not get to assume the answer it prefers")
	}
	before := len(*said)
	if err := dev.enforceWritableMounts(unknown); err != nil {
		t.Errorf("dev must not refuse an unanswerable question: %v", err)
	}
	if len(*said) != before {
		t.Errorf("dev must stay quiet on an unknown, said %q", (*said)[before:])
	}

	// The writable case says nothing at all, on either profile.
	if err := os.Chmod(dir, 0o775); err != nil {
		t.Fatal(err)
	}
	clean := captureWritableWarning(t)
	if err := dev.enforceWritableMounts(spec); err != nil || len(*clean) != 0 {
		t.Errorf("a writable workspace must be silent; err=%v said=%q", err, *clean)
	}
	if err := prod.enforceWritableMounts(spec); err != nil {
		t.Errorf("a writable workspace must not be refused under prod: %v", err)
	}
}

// captureWritableWarning redirects the warning and clears the once-per-report
// state, so tests do not depend on which of them ran first.
func captureWritableWarning(t *testing.T) *[]string {
	t.Helper()
	var said []string
	prev := warnedWritable
	warnedWritable = func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) }
	warnedTreeMu.Lock()
	prevSeen := warnedTrees
	warnedTrees = map[string]bool{}
	warnedTreeMu.Unlock()
	t.Cleanup(func() {
		warnedWritable = prev
		warnedTreeMu.Lock()
		warnedTrees = prevSeen
		warnedTreeMu.Unlock()
	})
	return &said
}

// requireOwnership skips where a file has no owning uid/gid to read.
func requireOwnership(t *testing.T) {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("no owning uid/gid to read")
	}
}

// requireLinux skips the checks that are Linux-only by design: everywhere else,
// bind-mount ownership is virtualized and the host mode decides nothing.
func requireLinux(t *testing.T) {
	t.Helper()
	if goruntime.GOOS != "linux" {
		t.Skip("the check is Linux-only; that it does nothing elsewhere is the property under test")
	}
}

// TestCheckWritableMountsIsInertWithoutAHostGroup is the regression for a defect
// that broke `make test` for every developer whose uid is not 1001.
//
// The check compares ids from the spec against ownership on the real filesystem.
// Under test BuildSpec is deliberately deterministic — TestMain pins
// hostPrimaryGID to "" so a spec names uid 1001 gid 1001 on any machine — so
// running the check there judged a real t.TempDir() against ids the machine does
// not have, and refused. Every kernel-boundary test in this package failed with
// a permissions error instead of the message it was asserting on, while CI
// stayed green because its runner is uid 1001 and the owner class decided.
//
// No pinHostGID here on purpose: this test asserts what the package-wide pin
// buys, so it must run under it.
func TestCheckWritableMountsIsInertWithoutAHostGroup(t *testing.T) {
	requireOwnership(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil { // as hostile as a mode gets
		t.Fatal(err)
	}
	spec := runtime.RunSpec{
		User:   defaultRunAsUser,
		Mounts: []runtime.Mount{{Source: dir, Target: "/workspace"}},
	}
	got := checkWritableMounts(spec, "")
	if len(got.Bad) != 0 || got.Unknown != "" {
		t.Errorf("checkWritableMounts = %+v with no host gid pinned; a spec built from "+
			"synthetic ids must not be judged against real ownership", got)
	}
}

// TestRemedyRootsCollapsesNestedPaths is the regression for output observed on a
// real run:
//
//	fix: chmod -R g+w /home/u/project
//	fix: chmod -R g+w /home/u/project/.git
//
// The first is recursive and already covers the second. Two commands where one
// will do reads as two separate problems, and a reader who runs only the one
// naming the path they recognise is left half-fixed.
func TestRemedyRootsCollapsesNestedPaths(t *testing.T) {
	tree := "/home/u/project"
	git := "/home/u/project/.git"

	got := remedyRoots([]unwritablePath{
		{Root: tree, Path: tree + "/bin", Group: true},
		{Root: git, Path: git + "/objects/05", Group: true, Git: true},
	})
	if len(got) != 1 || got[0].Root != tree {
		t.Fatalf("remedyRoots = %+v, want just %s: the recursive fix already covers what is inside it", got, tree)
	}

	// A contained root's requirement is folded in, not dropped: if the object
	// store needs its group changed, the single command has to do that too.
	got = remedyRoots([]unwritablePath{
		{Root: tree, Path: tree, Group: true},
		{Root: git, Path: git, Group: false},
	})
	if len(got) != 1 || got[0].Group {
		t.Fatalf("remedyRoots = %+v, want one root needing a chgrp: collapsing must not weaken the advice", got)
	}

	// Order of discovery must not change the answer.
	got = remedyRoots([]unwritablePath{
		{Root: git, Path: git, Group: false},
		{Root: tree, Path: tree, Group: true},
	})
	if len(got) != 1 || got[0].Root != tree || got[0].Group {
		t.Fatalf("remedyRoots = %+v, want the same answer whichever finding came first", got)
	}

	// Siblings are two real problems and stay two lines.
	a, b := "/home/u/one", "/home/u/two"
	if got := remedyRoots([]unwritablePath{{Root: a, Group: true}, {Root: b, Group: true}}); len(got) != 2 {
		t.Errorf("remedyRoots = %+v, want both: neither contains the other", got)
	}

	// Containment is by path component, not by string prefix: /srv/app is not
	// inside /srv/ap, and dropping it would lose a real finding.
	if got := remedyRoots([]unwritablePath{
		{Root: "/srv/ap", Group: true},
		{Root: "/srv/app", Group: true},
	}); len(got) != 2 {
		t.Errorf("remedyRoots = %+v, want both: /srv/app is not inside /srv/ap", got)
	}
}

// TestUnwritableReportPrintsOneFixPerProblem ties the collapse to what a user
// actually reads.
func TestUnwritableReportPrintsOneFixPerProblem(t *testing.T) {
	report := unwritableReport([]unwritablePath{
		{Root: "/home/u/project", Path: "/home/u/project/bin", Mode: 0o755, UID: 1000, GID: 1000, Group: true},
		{Root: "/home/u/project/.git", Path: "/home/u/project/.git/objects/05", Mode: 0o755, UID: 1000, GID: 1000, Group: true, Git: true},
	}, 1001, 1000)

	if n := strings.Count(report, "fix: "); n != 1 {
		t.Errorf("report has %d fix lines, want 1:\n%s", n, report)
	}
	// Both offending directories are still named — collapsing the remedy must not
	// collapse the evidence.
	for _, want := range []string{"/home/u/project/bin", "/home/u/project/.git/objects/05"} {
		if !strings.Contains(report, want) {
			t.Errorf("report no longer names %s:\n%s", want, report)
		}
	}
	// The git advice is separate from the chmod and survives the collapse.
	if !strings.Contains(report, "core.sharedRepository group") {
		t.Errorf("the durable git fix must still be offered:\n%s", report)
	}
}
