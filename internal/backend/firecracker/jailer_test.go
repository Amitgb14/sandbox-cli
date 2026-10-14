//go:build linux

package firecracker

import (
	"os"
	"path/filepath"
	"strings"
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

// A VM with a namespace of its own is started in it: the jailer is told to
// join it before it execs the VMM, and its own arguments still follow "--".
func TestJailerJoinsTheNamespace(t *testing.T) {
	j := &Jailer{Path: "/usr/bin/jailer", ChrootBase: "/srv/jail", UIDBase: 900000}
	got := strings.Join(j.command("sbx_1", "/usr/bin/firecracker", "/run/netns/sbx3", "--config-file", "vm.json").Args, " ")
	want := "/usr/bin/jailer --id sbx-1 --exec-file /usr/bin/firecracker --uid 900000 --gid 900000 --chroot-base-dir /srv/jail --netns /run/netns/sbx3 -- --api-sock api.sock --config-file vm.json"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := strings.Join(j.command("sbx_1", "/usr/bin/firecracker", "").Args, " "); strings.Contains(got, "--netns") {
		t.Errorf("without a namespace: %s", got)
	}
}
