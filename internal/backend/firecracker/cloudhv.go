//go:build linux

package firecracker

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// A sandbox with a GPU runs on Cloud Hypervisor instead of Firecracker, which
// cannot pass a host device through (docs/roadmap/task-9-gpu-sandboxes.md).
// Everything else is this backend's, unchanged: the root and scratch disks,
// the tap in the sandbox's namespace and its egress, and the guest agent over
// vsock, which Cloud Hypervisor implements as Firecracker does (a unix socket
// on the host and CONNECT <port>).

// pciAddr is a PCI device's full address, domain included, as sysfs names it.
var pciAddr = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)

// ValidPCIAddr reports whether a is a PCI address in sysfs's form
// (0000:01:00.0), the only form a GPU is named in.
func ValidPCIAddr(a string) bool { return pciAddr.MatchString(a) }

// CloudHypervisorArgs renders a GPU sandbox's Cloud Hypervisor command line,
// as BuildConfig renders a Firecracker VM's: a pure function of its inputs, and
// the only place such a VM's resources, disks and devices are decided. The
// disks are BuildConfig's (root read-only, scratch and writable volumes the
// only writable ones), and devices are the PCI addresses the sandbox was
// given, checked by the caller with ValidPCIAddr.
//
// Cloud Hypervisor parses its options as comma-separated key=value lists, so
// no path given to it may hold a comma; sandboxd's own paths never do, and
// one that did is refused here rather than read as another option.
func CloudHypervisorArgs(s backend.Spec, p vmPaths, bootArgs string, t *tap, devices []string) ([]string, error) {
	for _, path := range append([]string{p.Kernel, p.RootFS, p.Scratch, p.VsockUDS, p.APISock}, p.Volumes...) {
		if strings.ContainsAny(path, ", ") {
			return nil, fmt.Errorf("cloud hypervisor: path %q holds a comma or space", path)
		}
	}
	m := machineFor(s)
	args := []string{
		"--api-socket", "path=" + p.APISock,
		"--kernel", p.Kernel,
		"--cmdline", bootArgs,
		"--cpus", fmt.Sprintf("boot=%d", m.VCPUCount),
		"--memory", fmt.Sprintf("size=%dM", m.MemSizeMiB),
		// Every disk is raw ext4, said outright: Cloud Hypervisor will not
		// guess a format from what the disk holds.
		"--disk", "path=" + p.RootFS + ",readonly=on,image_type=raw", "path=" + p.Scratch + ",image_type=raw",
	}
	for i, path := range p.Volumes {
		d := "path=" + path + ",image_type=raw"
		if i < len(s.Volumes) && s.Volumes[i].ReadOnly {
			d += ",readonly=on"
		}
		args = append(args, d)
	}
	if t != nil {
		name, mac := t.device()
		args = append(args, "--net", "tap="+name+",mac="+mac)
	}
	// As Firecracker's: backed by a unix socket, so the CID is the VM's own.
	args = append(args, "--vsock", fmt.Sprintf("cid=3,socket=%s", p.VsockUDS))
	for _, d := range devices {
		if !ValidPCIAddr(d) {
			return nil, fmt.Errorf("cloud hypervisor: %q is not a PCI address", d)
		}
		args = append(args, "--device", "path=/sys/bus/pci/devices/"+d+"/")
	}
	// The serial console to the VMM's stdout (console.log), as Firecracker's;
	// no virtio console.
	return append(args, "--serial", "tty", "--console", "off"), nil
}

// cloudHypervisorBootArgs is a GPU sandbox's kernel command line:
// Firecracker's without pci=off, since its disks, vsock and GPU are all on PCI
// here; the root device, which Firecracker adds itself for the drive marked
// root and Cloud Hypervisor does not; sbx.halt=poweroff, because Cloud
// Hypervisor resets a guest that reboots and exits only when it powers off,
// so a boot sandbox-guestd fails must power off to end; and, for a sandbox
// with a GPU, sbx.gpu=1, which has sandbox-guestd load the kit's driver.
//
// A kernel panic still reboots (panic=1), and Cloud Hypervisor starts the
// guest again; the create's boot timeout is what ends that.
func cloudHypervisorBootArgs(fcArgs []string, gpu bool) []string {
	out := make([]string, 0, len(fcArgs)+3)
	for _, a := range fcArgs {
		if a != "pci=off" {
			out = append(out, a)
		}
	}
	out = append(out, "root=/dev/vda", "sbx.halt=poweroff")
	if gpu {
		out = append(out, "sbx.gpu=1")
	}
	return out
}
