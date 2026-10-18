//go:build linux

package firecracker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// Under the jailer, a GPU sandbox's VMM is the jail's uid with CAP_IPC_LOCK
// alone, in its network namespace, working in the jail's directory; without
// it, sandboxd's own user in the sandbox's directory.
func TestCloudHVCommand(t *testing.T) {
	v := &vm{id: "sbx_0123456789abcdef", dir: "/state/sandboxes/sbx_0123456789abcdef", net: &tap{ns: "sbx4"}}
	b := &Backend{cfg: Config{CloudHypervisor: "/usr/local/bin/cloud-hypervisor"}}
	cmd := b.cloudHVCommand(v, []string{"--kernel", "/kit/vmlinux"})
	if got := strings.Join(cmd.Args, " "); got != "ip netns exec sbx4 /usr/local/bin/cloud-hypervisor --kernel /kit/vmlinux" {
		t.Errorf("without the jailer: %s", got)
	}
	if cmd.Dir != v.dir {
		t.Errorf("without the jailer: in %s", cmd.Dir)
	}

	b.cfg.Jailer = &Jailer{ChrootBase: "/srv/jail", UIDBase: 900000}
	v.root = b.cfg.Jailer.root(v.id)
	cmd = b.cloudHVCommand(v, []string{"--kernel", "/kit/vmlinux"})
	want := "ip netns exec sbx4 setpriv --reuid=900000 --regid=900000 --clear-groups --inh-caps=-all,+ipc_lock --ambient-caps=+ipc_lock --bounding-set=-all,+ipc_lock -- /usr/local/bin/cloud-hypervisor --kernel /kit/vmlinux"
	if got := strings.Join(cmd.Args, " "); got != want {
		t.Errorf("under the jailer:\n got %s\nwant %s", got, want)
	}
	if cmd.Dir != "/srv/jail/firecracker/sbx-0123456789abcdef/root" {
		t.Errorf("under the jailer: in %s", cmd.Dir)
	}
}

// A kept GPU sandbox's root disk is read from the command line it was started
// with; a Firecracker one's from its vm.json.
func TestRootDiskOf(t *testing.T) {
	dir := t.TempDir()
	v := &vm{dir: dir}
	if _, err := rootDiskOf(v); err == nil {
		t.Error("a VM with no record: found a disk")
	}
	args, _ := json.Marshal([]string{"--kernel", "/kit/vmlinux", "--disk", "path=/images/rootfs/k/rootfs.ext4,readonly=on,image_type=raw", "path=scratch.ext4,image_type=raw"})
	os.WriteFile(filepath.Join(dir, cloudHVArgsFile), args, 0o600)
	if got, err := rootDiskOf(v); err != nil || got != "/images/rootfs/k/rootfs.ext4" {
		t.Errorf("from vm.args: %q, %v", got, err)
	}
	os.Remove(filepath.Join(dir, cloudHVArgsFile))
	cfg, _ := json.Marshal(BuildConfig(backend.Spec{MemoryMB: 512}, vmPaths{RootFS: "/images/rootfs/f/rootfs.ext4", Scratch: "scratch.ext4"}, "", nil))
	os.WriteFile(filepath.Join(dir, "vm.json"), cfg, 0o600)
	if got, err := rootDiskOf(v); err != nil || got != "/images/rootfs/f/rootfs.ext4" {
		t.Errorf("from vm.json: %q, %v", got, err)
	}
}

// A node without a kit refuses a GPU sandbox before anything is built, as
// does one asked for a GPU by an address that is not one.
func TestCheckGPUs(t *testing.T) {
	b := &Backend{}
	if err := b.checkGPUs(backend.Spec{GPUs: []string{"0000:01:00.0"}}); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("no kit: %v", err)
	}
	b.kit = &gpuKit{}
	if err := b.checkGPUs(backend.Spec{GPUs: []string{"0000:01:00.0"}, FromSnapshot: "snap_1"}); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("from a snapshot: %v", err)
	}
	for _, d := range []string{"01:00.0", "0000:01:00.0/../../..", "0000:01:00.0,x"} {
		if err := b.checkGPUs(backend.Spec{GPUs: []string{d}}); err == nil || !strings.Contains(err.Error(), "not a PCI address") {
			t.Errorf("%q: %v", d, err)
		}
	}
}

// The refusals a passed-through device forces say so, as unsupported.
func TestUnsupportedGPU(t *testing.T) {
	b := &Backend{vms: map[string]*vm{"sbx_1": {id: "sbx_1", cloudHV: true, exited: make(chan struct{})}}}
	if err := b.Suspend(t.Context(), "sbx_1"); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("suspend: %v", err)
	}
	if _, err := b.Snapshot(t.Context(), "sbx_1", "snap_1"); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("snapshot: %v", err)
	}
	if _, _, err := b.ResizeSandbox(t.Context(), "sbx_1", sizeOf(backend.Spec{CPUs: 2}), true); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("resize: %v", err)
	}
}
