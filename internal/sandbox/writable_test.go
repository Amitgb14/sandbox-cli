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

// TestGuestIDs covers the whole resolution table, and the first row is the one
// the check would be useless without: in allowlist mode — the dev default —
// docker's --user is root and the guest's real identity is in SANDBOX_RUN_AS.
// Reading spec.User there answers for the root phase, and root can write
// anything, so every run in the default mode would report no problem.
func TestGuestIDs(t *testing.T) {
	tests := []struct {
		name     string
		spec     runtime.RunSpec
		uid, gid int
		ok       bool
	}{
		{
			name: "allowlist: SANDBOX_RUN_AS outranks a root --user",
			spec: runtime.RunSpec{User: "root", Env: map[string]string{"SANDBOX_RUN_AS": "1001:1000"}},
			uid:  1001, gid: 1000, ok: true,
		},
		{
			name: "default mode: the docker --user is the answer",
			spec: runtime.RunSpec{User: "1001:1000"},
			uid:  1001, gid: 1000, ok: true,
		},
		{
			name: "the image's own user is the one name resolvable from here",
			spec: runtime.RunSpec{User: "sandbox"},
			uid:  1001, gid: 1001, ok: true,
		},
		{
			name: "a bare uid reuses itself as the gid, as docker reads it",
			spec: runtime.RunSpec{User: "1000"},
			uid:  1000, gid: 1000, ok: true,
		},
		{
			name: "root bypasses the mode bits, so there is nothing to check",
			spec: runtime.RunSpec{User: "root"},
			ok:   false,
		},
		{
			name: "uid 0 spelled numerically is still root",
			spec: runtime.RunSpec{User: "0:0"},
			ok:   false,
		},
		{
			name: "an empty user means the image default, which is root",
			spec: runtime.RunSpec{},
			ok:   false,
		},
		{
			name: "a name only the image's passwd database defines is not ours to guess",
			spec: runtime.RunSpec{User: "node"},
			ok:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uid, gid, ok := guestIDs(tc.spec)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && (uid != tc.uid || gid != tc.gid) {
				t.Errorf("ids = %d:%d, want %d:%d", uid, gid, tc.uid, tc.gid)
			}
		})
	}
}

// TestUnwritableBy is the arithmetic the whole check rests on, exercised against
// a real directory rather than a table of modes: the point is that it agrees
// with what the kernel would do, and only a real stat can be wrong about that.
//
// 0755 vs 0775 is not an arbitrary pair. It is umask 022 against umask 002 —
// the difference between a host where the agent has a read-only workspace and
// one where it does not, which is the whole of issue #80.
func TestUnwritableBy(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("no owning uid/gid to read")
	}
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
		t.Fatal("0755 must NOT be writable through the group — this is the umask 022 case, and missing it is issue #80")
	}
	if u.Path != dir || u.UID != me || u.GID != myGroup {
		t.Errorf("report = %+v, want the directory's own path and ids", u)
	}

	if err := os.Chmod(dir, 0o775); err != nil {
		t.Fatal(err)
	}
	if _, bad := unwritableBy(dir, otherUID, myGroup); bad {
		t.Error("0775 must be writable through the group — this is the umask 002 case, which works today")
	}

	// Write without search is not enough to create an entry, and a mode that
	// grants one without the other is exactly the case a `w`-only test would miss.
	if err := os.Chmod(dir, 0o764); err != nil {
		t.Fatal(err)
	}
	if _, bad := unwritableBy(dir, otherUID, myGroup); !bad {
		t.Error("g=rw with no search must be reported: a directory cannot be written without traversing it")
	}

	// A path that cannot be stat'd is a question that could not be asked, not an
	// answer of "no" — the same distinction doctor draws for an unreachable daemon.
	if _, bad := unwritableBy(filepath.Join(dir, "nope"), otherUID, myGroup); bad {
		t.Error("a missing path must not be reported as unwritable")
	}
}

// TestCheckDirLooksInsideAGitDirectory: a writable .git whose objects/ fan-out is
// not is the state that produced the opaque git error, and the reason the check
// does not stop at the mount itself.
func TestCheckDirLooksInsideAGitDirectory(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("no owning uid/gid to read")
	}
	const otherUID = 999999
	myGroup := os.Getgid()

	gitDir := t.TempDir()
	fanout := filepath.Join(gitDir, "objects", "ab")
	if err := os.MkdirAll(fanout, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(gitDir, "refs"), 0o775); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{gitDir, filepath.Join(gitDir, "objects")} {
		if err := os.Chmod(d, 0o775); err != nil {
			t.Fatal(err)
		}
	}
	// Only the fan-out is wrong — what git leaves behind when the host umask was
	// 022 at the moment that one directory was created.
	if err := os.Chmod(fanout, 0o755); err != nil {
		t.Fatal(err)
	}

	got := checkDir(gitDir, otherUID, myGroup)
	if len(got) != 1 || got[0].Path != fanout {
		t.Fatalf("checkDir = %+v, want just the fan-out directory %s", got, fanout)
	}

	// A plain directory is not searched for an object store it does not have.
	plain := t.TempDir()
	if err := os.Chmod(plain, 0o775); err != nil {
		t.Fatal(err)
	}
	if got := checkDir(plain, otherUID, myGroup); len(got) != 0 {
		t.Errorf("checkDir on a non-git directory = %+v, want nothing", got)
	}
}

// TestUnwritableMountsSkipsWhereTheIdsDoNotMeet pins the three cases where the
// question does not arise. Each is a host where the mode on this side is not what
// decides access on the other, so an answer computed here would be a guess.
func TestUnwritableMountsSkipsWhereTheIdsDoNotMeet(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("the check itself is Linux-only; that is the property under test elsewhere")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil { // unreadable by any group
		t.Fatal(err)
	}
	spec := runtime.RunSpec{
		User:   "1001:" + fmt.Sprint(os.Getgid()),
		Mounts: []runtime.Mount{{Source: dir, Target: "/workspace"}},
	}

	if got := unwritableMounts(spec, "podman"); got != nil {
		t.Errorf("podman = %+v, want nothing: keep-id maps the host user onto the container user", got)
	}

	rootSpec := spec
	rootSpec.User = "root"
	if got := unwritableMounts(rootSpec, ""); got != nil {
		t.Errorf("root guest = %+v, want nothing: uid 0 is not subject to the mode bits", got)
	}

	roSpec := spec
	roSpec.Mounts = []runtime.Mount{{Source: dir, Target: "/workspace", RO: true}}
	if got := unwritableMounts(roSpec, ""); got != nil {
		t.Errorf("read-only mount = %+v, want nothing: nobody was promised they could write it", got)
	}

	volSpec := spec
	volSpec.Mounts = []runtime.Mount{{Source: "sandbox-cache-npm", Target: "/cache", Volume: true}}
	if got := unwritableMounts(volSpec, ""); got != nil {
		t.Errorf("named volume = %+v, want nothing: there is no host path to judge", got)
	}
}

// TestEnforceWritableMounts is the profile asymmetry: the same host fact warns
// under dev and refuses under prod, which is the one difference of kind between
// the two profiles.
func TestEnforceWritableMounts(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("the underlying check is Linux-only")
	}
	if os.Getuid() == 0 {
		t.Skip("running as root, which can write anything")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil { // umask 022: the reported case
		t.Fatal(err)
	}
	spec := runtime.RunSpec{
		// A different uid in our own group, which is what sharedGroupUser renders.
		User:   "1001:" + fmt.Sprint(os.Getgid()),
		Mounts: []runtime.Mount{{Source: dir, Target: "/workspace"}},
	}
	if os.Getuid() == 1001 {
		t.Skip("this host's user is the container's uid; the owner class would decide")
	}

	var said []string
	prev := warnedWritable
	warnedWritable = func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { warnedWritable = prev })

	dev := &Session{Cfg: config.Config{Profile: config.ProfileDev}}
	if err := dev.enforceWritableMounts(spec); err != nil {
		t.Fatalf("dev must warn and run, not refuse: %v", err)
	}
	if len(said) != 1 {
		t.Fatalf("dev said %d warnings, want 1: %q", len(said), said)
	}
	if !strings.Contains(said[0], dir) || !strings.Contains(said[0], "chmod g+w") {
		t.Errorf("the warning must name the path and the fix, got %q", said[0])
	}

	prod := &Session{Cfg: config.Config{Profile: config.ProfileProd}}
	err := prod.enforceWritableMounts(spec)
	if err == nil {
		t.Fatal("prod must refuse: nobody is watching, and an agent that cannot write fails quietly")
	}
	if !strings.Contains(err.Error(), "--profile dev") {
		t.Errorf("a prod refusal must say how to proceed anyway, got %q", err)
	}

	// The writable case says nothing at all, on either profile.
	if err := os.Chmod(dir, 0o775); err != nil {
		t.Fatal(err)
	}
	said = nil
	if err := dev.enforceWritableMounts(spec); err != nil || len(said) != 0 {
		t.Errorf("a writable workspace must be silent; err=%v said=%q", err, said)
	}
	if err := prod.enforceWritableMounts(spec); err != nil {
		t.Errorf("a writable workspace must not be refused under prod: %v", err)
	}
}
