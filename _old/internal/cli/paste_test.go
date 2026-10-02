package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPasteDirsSkipsWhatIsNotThere checks the two halves of the contract: the
// well-known directories that exist are returned in the fixed order, and a
// missing one is skipped rather than handed to docker (which would create it on
// the host, as root, to satisfy the bind).
func TestPasteDirsSkipsWhatIsNotThere(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{"Desktop", "Pictures", "Music"} {
		if err := os.Mkdir(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A file, not a directory: must not be mounted either.
	if err := os.WriteFile(filepath.Join(home, "Downloads"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got := pasteDirs(realpath(t, home))
	want := []string{
		filepath.Join(realpath(t, home), "Desktop"),
		filepath.Join(realpath(t, home), "Pictures"),
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pasteDirs = %v, want %v", got, want)
	}
}

// TestPasteMountsAreReadOnlySamePath pins the property the whole feature rests
// on: the container path is identical to the host path (so a pasted host path
// resolves) and the mount is read-only (attaching an image never writes).
func TestPasteMountsAreReadOnlySamePath(t *testing.T) {
	home := realpath(t, t.TempDir())
	if err := os.Mkdir(filepath.Join(home, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	mounts, dirs := pasteMounts(home)
	desktop := filepath.Join(home, "Desktop")
	if len(mounts) != 1 || len(dirs) != 1 {
		t.Fatalf("pasteMounts = %v, %v, want one entry each", mounts, dirs)
	}
	if want := desktop + ":" + desktop + ":ro"; mounts[0] != want {
		t.Errorf("mount = %q, want %q", mounts[0], want)
	}
	if dirs[0] != desktop {
		t.Errorf("dir = %q, want %q", dirs[0], desktop)
	}
}

// TestPasteDirsRefuseToEscapeHome guards the claim that nothing outside the home
// is ever mounted. A symlinked ~/Desktop pointing at the filesystem root would
// otherwise hand the container the entire host — and a paste directory resolving
// onto a path the guest owns (/tmp) would shadow it.
func TestPasteDirsRefuseToEscapeHome(t *testing.T) {
	home := realpath(t, t.TempDir())
	outside := realpath(t, t.TempDir())
	if err := os.Symlink(outside, filepath.Join(home, "Desktop")); err != nil {
		t.Fatal(err)
	}
	if got := pasteDirs(home); len(got) != 0 {
		t.Errorf("pasteDirs = %v, want none: a symlink out of home must not be mounted", got)
	}
}

// TestPasteDirsWithoutHome: no home means no candidates, not a join against "".
func TestPasteDirsWithoutHome(t *testing.T) {
	if got := pasteDirs(""); got != nil {
		t.Errorf("pasteDirs(\"\") = %v, want nil", got)
	}
}

// realpath resolves symlinks the way pasteDirs does, so the comparisons above
// hold on macOS where t.TempDir() hands back a /var -> /private/var path.
func realpath(t *testing.T, p string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
