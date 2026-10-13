//go:build linux

package firecracker

import (
	"os"
	"path/filepath"
	"testing"
)

// The jailer will not start a jail again while the pid file of its last
// start is there, so a resize or a resume, which start the same jail again,
// failed with "firecracker.pid: File exists" and left the sandbox stopped.
func TestCleanupProcessLetsTheJailStartAgain(t *testing.T) {
	j := &Jailer{ChrootBase: t.TempDir()}
	root := j.root("sbx_1")
	if err := os.MkdirAll(filepath.Join(root, "dev"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"firecracker.pid", "api.sock", "v.sock", "scratch.ext4", "rootfs.ext4", "vm.json"} {
		if err := os.WriteFile(filepath.Join(root, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	j.cleanupProcess("sbx_1")
	for _, f := range []string{"firecracker.pid", "api.sock", "v.sock", "dev"} {
		if _, err := os.Stat(filepath.Join(root, f)); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", f, err)
		}
	}
	for _, f := range []string{"scratch.ext4", "rootfs.ext4", "vm.json"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("%s, the sandbox's own, was removed: %v", f, err)
		}
	}
}
