package main

import (
	"os"
	"path/filepath"
	"testing"
)

// idle on macOS must give an image without one a home for the sandbox user:
// every process starts there.
func TestMissingDirsMakesHome(t *testing.T) {
	root := t.TempDir()
	missingDirs(root)
	if fi, err := os.Stat(filepath.Join(root, defaultHome)); err != nil || !fi.IsDir() {
		t.Errorf("%s was not made: %v", defaultHome, err)
	}
	// There is no workspace any more, and nothing makes one.
	if _, err := os.Stat(filepath.Join(root, "workspace")); err == nil {
		t.Error("a /workspace was made")
	}
}

// An existing home is the image's; it is left as it is.
func TestMissingDirsLeavesAnExistingHome(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, defaultHome)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	missingDirs(root)
	fi, err := os.Stat(home)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("an existing home was changed: %v %v", fi.Mode(), err)
	}
}
