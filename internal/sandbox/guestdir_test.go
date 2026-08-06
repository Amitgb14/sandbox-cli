package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reported bug, as a property: the guest side of a bind mount must exist on
// the host before the container starts, so the runtime never creates it as root.
// Under rootless podman that root is a subordinate uid, and the directory comes
// back unwritable by the agent — which cost a Claude login on every single run.
func TestEnsureGuestDirCreatesTheWholeChain(t *testing.T) {
	root := t.TempDir()

	EnsureGuestDir(root, ".claude/projects/-workspace")

	for _, rel := range []string{".claude", ".claude/projects", ".claude/projects/-workspace"} {
		fi, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s was not created: %v", rel, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("%s is not a directory", rel)
		}
	}
}

// Idempotent: it runs on every launch, and the common case is that everything is
// already there.
func TestEnsureGuestDirIsIdempotent(t *testing.T) {
	root := t.TempDir()
	rel := ".claude/projects/-workspace"

	EnsureGuestDir(root, rel)
	// Something the agent would have written on the first run. A second call must
	// not disturb it.
	marker := filepath.Join(root, ".claude", ".credentials.json")
	if err := os.WriteFile(marker, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	EnsureGuestDir(root, rel)

	b, err := os.ReadFile(marker)
	if err != nil || string(b) != "token" {
		t.Errorf("a second call disturbed existing content: %q, %v", b, err)
	}
}

// A guest path must never walk out of the directory it is allowed to create in.
// The caller builds rel by trimming a prefix off a mount target, so a target that
// did not have that prefix would otherwise turn into a path outside the persisted
// HOME.
func TestEnsureGuestDirRefusesToEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outside, "inner")

	EnsureGuestDir(inner, "../../escaped/deeper")

	if _, err := os.Stat(filepath.Join(root, "escaped")); err == nil {
		t.Error("created a directory above the root it was given")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escaped")); err == nil {
		t.Error("created a directory outside the temp root entirely")
	}
}

// Nothing is created for a run with no persisted HOME, and nothing is created from
// an empty request — a guard the caller relies on rather than repeating.
func TestEnsureGuestDirIgnoresEmptyInput(t *testing.T) {
	root := t.TempDir()

	EnsureGuestDir("", ".claude/projects")
	EnsureGuestDir(root, "")

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("created %d entries for an empty request", len(entries))
	}
}

// The half that cannot be fixed, only reported: a level that already belongs to
// another uid. EnsureGuestDir prevents that state on a fresh setup but cannot undo
// it, because chown/chmod on a subuid-owned path fails for the user who is not its
// owner — so the run has to say so, with the command that clears it.
//
// The foreign-owner branch itself needs a path owned by somebody else, which a test
// cannot create without privileges. What is pinned here is the part that is
// checkable: our own directories are never reported, so the warning cannot become
// noise that trains somebody to ignore it.
func TestEnsureGuestDirDoesNotWarnAboutOurOwnPaths(t *testing.T) {
	root := t.TempDir()
	var warned strings.Builder

	prev := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	EnsureGuestDir(root, ".claude/projects/-workspace")
	w.Close()
	os.Stderr = prev

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	warned.Write(buf[:n])

	if got := warned.String(); got != "" {
		t.Errorf("warned about a directory we created ourselves: %q", got)
	}
}

// The root does not exist yet on a first run: a wrapper assembles its mounts
// before the run path creates the persisted HOME. This is the regression test for
// a fix that silently did nothing — every Mkdir failed with ENOENT, and the unit
// tests could not see it because t.TempDir() had already created the root.
func TestEnsureGuestDirCreatesAMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "agents", "claude") // deliberately absent

	EnsureGuestDir(root, ".claude/projects/-workspace")

	if fi, err := os.Stat(filepath.Join(root, ".claude", "projects", "-workspace")); err != nil {
		t.Errorf("chain not created under a missing root: %v", err)
	} else if !fi.IsDir() {
		t.Error("leaf is not a directory")
	}
}

// A request that sanitises down to nothing must create nothing at all — not even
// the root, which would leave a directory behind for a path that was refused.
func TestEnsureGuestDirRefusedRequestCreatesNothing(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "agents", "claude")

	EnsureGuestDir(root, "../..")

	if _, err := os.Stat(root); err == nil {
		t.Error("created the root for a request that resolved to no path")
	}
}
