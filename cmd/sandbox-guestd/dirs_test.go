package main

import (
	"os"
	"path/filepath"
	"testing"
)

// idle on macOS must give an image without them a workspace and a home: the CLI
// runs every command in /workspace.
func TestMissingDirsMakesWorkspaceAndHome(t *testing.T) {
	root := t.TempDir()
	missingDirs(root)
	for _, d := range []string{"/workspace", defaultHome} {
		if fi, err := os.Stat(filepath.Join(root, d)); err != nil || !fi.IsDir() {
			t.Errorf("%s was not made: %v", d, err)
		}
	}
}

// An existing /workspace may be a bind of a host directory; it is left as it is.
func TestMissingDirsLeavesAnExistingWorkspace(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "workspace")
	if err := os.Mkdir(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	missingDirs(root)
	fi, err := os.Stat(ws)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("an existing /workspace was changed: %v %v", fi.Mode(), err)
	}
}
