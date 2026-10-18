//go:build linux

package firecracker

import (
	"net"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// A GPU sandbox, as Cloud Hypervisor is told: BuildConfig's disks (root
// read-only, scratch and a writable volume the only writable ones), the tap,
// vsock as the way in, and the GPU by its sysfs path.
func TestCloudHypervisorArgsGolden(t *testing.T) {
	s := backend.Spec{ID: "sbx_0123456789abcdef", Image: "img", CPUs: 1.5, MemoryMB: 8192, DiskMB: 10240,
		Network: api.NetworkPolicy{Mode: api.NetworkOpen},
		Volumes: []api.VolumeMount{{Name: "data"}, {Name: "ref", ReadOnly: true}}}
	p := vmPaths{Kernel: "/k/vmlinux", RootFS: "/i/rootfs.ext4", Scratch: "scratch.ext4", VsockUDS: "v.sock", APISock: "api.sock",
		Volumes: []string{"/v/data.ext4", "/v/ref.ext4"}}
	inNS := &tap{name: "sbx7", ns: "sbx7", host: net.IPv4(172, 16, 0, 29).To4(), guest: net.IPv4(172, 16, 0, 30).To4(), mac: "06:00:ac:10:00:1e"}
	args, err := CloudHypervisorArgs(s, p, "console=ttyS0 ro sbx.gpu=1", inNS, []string{"0000:01:00.0"})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "cloudhv-args.txt", []byte(strings.Join(args, "\n")+"\n"))
}

func TestCloudHypervisorArgsRefuses(t *testing.T) {
	s := backend.Spec{ID: "sbx_x", CPUs: 1, MemoryMB: 512}
	ok := vmPaths{Kernel: "/k", RootFS: "/r", Scratch: "s", VsockUDS: "v", APISock: "a"}
	for name, c := range map[string]struct {
		p       vmPaths
		devices []string
	}{
		"a comma in a path":       {vmPaths{Kernel: "/k", RootFS: "/r,readonly=off", Scratch: "s", VsockUDS: "v", APISock: "a"}, nil},
		"a space in a path":       {vmPaths{Kernel: "/k", RootFS: "/r", Scratch: "s x", VsockUDS: "v", APISock: "a"}, nil},
		"a short PCI address":     {ok, []string{"01:00.0"}},
		"a path for an address":   {ok, []string{"0000:01:00.0/../../x"}},
		"an option in an address": {ok, []string{"0000:01:00.0,iommu=on"}},
	} {
		if _, err := CloudHypervisorArgs(s, c.p, "", nil, c.devices); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCloudHypervisorBootArgs(t *testing.T) {
	fc := []string{"console=ttyS0", "pci=off", "ro", "init=/sbin/sandbox-guestd"}
	if got := strings.Join(cloudHypervisorBootArgs(fc, true), " "); got != "console=ttyS0 ro init=/sbin/sandbox-guestd root=/dev/vda sbx.halt=poweroff sbx.gpu=1" {
		t.Errorf("with a GPU: %q", got)
	}
	// Without one (the tests' cloudHVAll), the driver is not loaded.
	if got := strings.Join(cloudHypervisorBootArgs(fc, false), " "); got != "console=ttyS0 ro init=/sbin/sandbox-guestd root=/dev/vda sbx.halt=poweroff" {
		t.Errorf("without: %q", got)
	}
}
