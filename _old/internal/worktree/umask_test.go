package worktree

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
)

// TestGroupWritableOpensTheGroupBits pins what a worktree is created with, and
// it is a unit test rather than a worktree-creation test because the property is
// about the mask, not about git.
//
// The container runs as a different uid in the host user's primary group, so a
// directory created at the ordinary umask 022 comes back 0755 and is one the
// agent cannot write — the failure internal/sandbox/writable.go now refuses
// under prod, on a path this package created moments earlier.
func TestGroupWritableOpensTheGroupBits(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("no umask to adjust")
	}
	root := t.TempDir()

	var made string
	err := groupWritable(func() error {
		made = filepath.Join(root, "wt")
		return os.Mkdir(made, 0o777)
	})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(made)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o070 != 0o070 {
		t.Errorf("worktree created %v, want group rwx: the container is in the group and cannot write without it",
			fi.Mode().Perm())
	}
	if fi.Mode().Perm()&0o002 != 0 {
		t.Errorf("worktree created %v, world-writable: 0002 must not open that", fi.Mode().Perm())
	}
}

// TestGroupWritableRestoresTheMask: the umask is process-wide, so failing to put
// it back would silently widen every file the rest of the command creates.
func TestGroupWritableRestoresTheMask(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("no umask to adjust")
	}
	root := t.TempDir()

	before := filepath.Join(root, "before")
	if err := os.Mkdir(before, 0o777); err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(before)
	if err != nil {
		t.Fatal(err)
	}

	if err := groupWritable(func() error { return nil }); err != nil {
		t.Fatal(err)
	}

	after := filepath.Join(root, "after")
	if err := os.Mkdir(after, 0o777); err != nil {
		t.Fatal(err)
	}
	got, err := os.Stat(after)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode().Perm() != want.Mode().Perm() {
		t.Errorf("mode after = %v, before = %v: the process umask was not restored",
			got.Mode().Perm(), want.Mode().Perm())
	}
}
