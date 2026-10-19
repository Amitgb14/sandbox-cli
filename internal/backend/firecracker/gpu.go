//go:build linux

package firecracker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/image"
)

// GPU sandboxes (docs/roadmap/task-9-gpu-sandboxes.md, phase 2). A sandbox
// given GPUs boots on Cloud Hypervisor with the GPU kit's kernel, the kit in
// its root disk and the devices passed through; everything else is this
// backend's as for any sandbox. What differs is here: the kit, the VMM's
// command, the devices' VFIO groups, and the refusals a passed-through device
// forces (suspend, snapshot, resize).
//
// Under the jailer, Cloud Hypervisor, which has no jailer of its own, runs as
// the uid the jailer would have given the sandbox, in the jail's directory
// (not chrooted into it), with the one capability VFIO needs to pin the
// guest's memory for DMA (CAP_IPC_LOCK), /dev/kvm's group if the node is not
// open to all (kvmGroups), and the VFIO group node of each of its devices
// handed to that uid for its life.

// gpuKit is a kit as scripts/gpu-kit.sh writes it.
type gpuKit struct {
	kernel string     // <kit>/vmlinux
	tree   *image.Kit // <kit>/rootfs, added to the root disk
}

// loadKit checks a kit directory and sums the tree it adds to root disks, so
// a changed kit is a new disk rather than a stale one.
func loadKit(dir string) (*gpuKit, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	k := &gpuKit{kernel: filepath.Join(dir, "vmlinux")}
	for _, p := range []string{k.kernel, filepath.Join(dir, "kit.json"), filepath.Join(dir, "rootfs", strings.TrimPrefix(gpuModulesList, "/"))} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("GPU kit %s (scripts/gpu-kit.sh writes one): %w", dir, err)
		}
	}
	tree := filepath.Join(dir, "rootfs")
	sum, err := image.TreeSum(tree)
	if err != nil {
		return nil, fmt.Errorf("GPU kit %s: %w", dir, err)
	}
	k.tree = &image.Kit{Dir: tree, Sum: sum}
	return k, nil
}

// gpuModulesList is where sandbox-guestd reads the kit's modules from
// (cmd/sandbox-guestd, GPUKitDir).
const gpuModulesList = "/usr/lib/sandbox-gpu/modules.list"

// cloudHV reports whether a sandbox runs on Cloud Hypervisor: one with GPUs,
// or, in this package's tests, every sandbox (Backend.cloudHVAll), so the path
// is tested on a host without a free GPU.
func (b *Backend) cloudHV(s backend.Spec) bool { return len(s.GPUs) > 0 || b.cloudHVAll }

// checkGPUs refuses a GPU sandbox this node cannot give its devices: no kit,
// an address not in sysfs form, or a device not bound to vfio-pci.
func (b *Backend) checkGPUs(s backend.Spec) error {
	if b.kit == nil {
		return fmt.Errorf("%w: this node serves no GPUs (sandboxd has no GPU kit)", backend.ErrUnsupported)
	}
	if s.FromSnapshot != "" {
		return fmt.Errorf("%w: a sandbox with a GPU cannot start from a snapshot", backend.ErrUnsupported)
	}
	for _, d := range s.GPUs {
		if err := CheckGPU(d); err != nil {
			return err
		}
	}
	return nil
}

// CheckGPU reports whether a device is ready to be passed through: a PCI
// address in sysfs's form, bound to vfio-pci, in an IOMMU group whose VFIO
// node exists. A create checks each of its devices with it, and sandboxd each
// of its --gpu devices at start, so a node set up wrong says so when it
// starts rather than at its first GPU sandbox.
func CheckGPU(d string) error {
	if !backend.ValidPCIAddr(d) {
		return fmt.Errorf("GPU %q is not a PCI address like 0000:01:00.0", d)
	}
	drv, err := os.Readlink(filepath.Join("/sys/bus/pci/devices", d, "driver"))
	if err != nil || filepath.Base(drv) != "vfio-pci" {
		return fmt.Errorf("GPU %s is not bound to vfio-pci (scripts/gpu-vfio.sh bind %s)", d, d)
	}
	_, err = vfioGroup(d)
	return err
}

// vfioGroup is a device's VFIO group node, /dev/vfio/<its IOMMU group>.
func vfioGroup(bdf string) (string, error) {
	g, err := os.Readlink(filepath.Join("/sys/bus/pci/devices", bdf, "iommu_group"))
	if err != nil {
		return "", fmt.Errorf("GPU %s has no IOMMU group: %w", bdf, err)
	}
	if _, err := strconv.Atoi(filepath.Base(g)); err != nil {
		return "", fmt.Errorf("GPU %s: IOMMU group %q", bdf, g)
	}
	node := filepath.Join("/dev/vfio", filepath.Base(g))
	if _, err := os.Stat(node); err != nil {
		return "", fmt.Errorf("GPU %s: %w", bdf, err)
	}
	return node, nil
}

// handGPUs gives each of v's devices' VFIO group nodes to the uid its VMM
// runs as, under the jailer; without it the VMM runs as sandboxd's own user,
// which must be able to open them already. A failure is the create's.
func (b *Backend) handGPUs(v *vm) error {
	if b.cfg.Jailer == nil {
		return nil
	}
	uid := b.cfg.Jailer.uid(v.id)
	for _, d := range v.spec.GPUs {
		node, err := vfioGroup(d)
		if err != nil {
			return err
		}
		if err := os.Chown(node, uid, uid); err != nil {
			return fmt.Errorf("GPU %s: %w", d, err)
		}
	}
	return nil
}

// releaseGPUs gives the devices' VFIO group nodes back to root, as vfio-pci
// made them. A VMM still running keeps what it has open; no other process of
// the jail's uid can open the group after this.
func (b *Backend) releaseGPUs(gpus []string) {
	if b.cfg.Jailer == nil {
		return
	}
	for _, d := range gpus {
		if node, err := vfioGroup(d); err == nil {
			_ = os.Chown(node, 0, 0)
		}
	}
}

// gpuReleaseTimeout is how long a terminate waits for a GPU sandbox's killed
// VMM to exit. Tearing a passed-through device down (its reset, unpinning all
// of the guest's memory) can take far longer than a Firecracker VM's exit,
// and until the VMM has exited it holds the VFIO group, so the next sandbox
// given the GPU would fail to open it. A variable for the tests.
var gpuReleaseTimeout = 2 * time.Minute

// awaitGPURelease kills a GPU sandbox's VMM and waits for it to exit, so a
// terminate that returns has freed the device. One that does not exit in time
// is an error: the sandbox is not terminated, and terminating it again waits
// again, rather than the server handing the GPU to another sandbox while this
// VMM still has it.
func (b *Backend) awaitGPURelease(v *vm) error {
	if len(v.spec.GPUs) == 0 || v.exited == nil {
		return nil
	}
	killVMM(v)
	select {
	case <-v.exited:
		return nil
	case <-time.After(gpuReleaseTimeout):
		return fmt.Errorf("sandbox %s: its VMM has not exited %v after it was killed, and still holds GPU %s; terminate it again",
			v.id, gpuReleaseTimeout, strings.Join(v.spec.GPUs, ", "))
	}
}

// gpusInArgs are the PCI addresses a Cloud Hypervisor command line (vm.args)
// passes through: how a leftover sandbox's devices are known when no keep.json
// says, as without --keep-sandboxes.
func gpusInArgs(args []string) []string {
	var out []string
	for i, a := range args {
		if a != "--device" || i+1 >= len(args) {
			continue
		}
		p := strings.TrimSuffix(strings.TrimPrefix(args[i+1], "path=/sys/bus/pci/devices/"), "/")
		if backend.ValidPCIAddr(p) {
			out = append(out, p)
		}
	}
	return out
}

// leftoverGPUs are the devices a leftover sandbox's VMM was given, from its
// vm.args.
func leftoverGPUs(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, cloudHVArgsFile))
	if err != nil {
		return nil
	}
	var args []string
	if json.Unmarshal(data, &args) != nil {
		return nil
	}
	return gpusInArgs(args)
}

// onCloudHV reports whether a kept sandbox's VMM is Cloud Hypervisor: one with
// GPUs, or any whose directory has the command line only a Cloud Hypervisor VM
// writes (a test's cloudHVAll sandbox). Taken back as a Firecracker VM, its
// suspend and snapshot would be Firecracker API calls to Cloud Hypervisor's
// socket rather than refusals.
func onCloudHV(dir string, s backend.Spec) bool {
	if len(s.GPUs) > 0 {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, cloudHVArgsFile))
	return err == nil
}

// kvmGroups are the supplementary groups a jailed Cloud Hypervisor keeps so it
// can open /dev/kvm: none when the node is world read-writable (0666, as many
// distributions make it), else the group that owns it (kvm, 0660 on Debian and
// Ubuntu). Firecracker's jailer makes a /dev/kvm of its own in the chroot;
// Cloud Hypervisor is not chrooted and opens the host's, as the jail's uid.
func kvmGroups(path string) ([]int, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("GPU sandboxes need %s: %w", path, err)
	}
	if fi.Mode().Perm()&0o006 == 0o006 {
		return nil, nil
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || fi.Mode().Perm()&0o060 != 0o060 {
		return nil, fmt.Errorf("GPU sandboxes under the jailer need %s read-writable by its group or by all (it is %v)", path, fi.Mode().Perm())
	}
	return []int{int(st.Gid)}, nil
}

// unsupportedGPU is the refusal of what a passed-through device cannot do.
func unsupportedGPU(what string) error {
	return fmt.Errorf("%w: a sandbox with a GPU cannot be %s: its VMM cannot save a passed-through device's state", backend.ErrUnsupported, what)
}

// prepareCloudHV is Jailer.prepare for a VMM that is not chrooted: the same
// links and ownership in the jail's directory, named relative to it (the
// VMM's working directory), the kernel by its absolute path in the kit, and
// the directory itself the VMM's, for its sockets.
func (j *Jailer) prepareCloudHV(id, kernel, rootfs, scratch string, volumes []string) (vmPaths, error) {
	p, err := j.prepare(id, "", rootfs, scratch, volumes)
	if err != nil {
		return vmPaths{}, err
	}
	u := j.uid(id)
	if err := os.Chown(j.root(id), u, u); err != nil {
		return vmPaths{}, err
	}
	rel := func(s string) string { return strings.TrimPrefix(s, "/") }
	p.Kernel, p.RootFS, p.Scratch, p.VsockUDS, p.APISock = kernel, rel(p.RootFS), rel(p.Scratch), rel(p.VsockUDS), "api.sock"
	for i := range p.Volumes {
		p.Volumes[i] = rel(p.Volumes[i])
	}
	return p, nil
}

// cloudHVCommand is a GPU sandbox's VMM: Cloud Hypervisor with args, in the
// sandbox's network namespace when it has one, and under the jailer as the
// jail's uid with CAP_IPC_LOCK alone (setpriv), from the directory its
// relative paths are relative to.
func (b *Backend) cloudHVCommand(v *vm, args []string) *exec.Cmd {
	argv := append([]string{b.cfg.CloudHypervisor}, args...)
	dir := v.dir
	if b.cfg.Jailer != nil {
		u := strconv.Itoa(b.cfg.Jailer.uid(v.id))
		groups := "--clear-groups"
		if len(b.kvmGroups) > 0 {
			gs := make([]string, len(b.kvmGroups))
			for i, g := range b.kvmGroups {
				gs[i] = strconv.Itoa(g)
			}
			groups = "--groups=" + strings.Join(gs, ",")
		}
		argv = append([]string{"setpriv", "--reuid=" + u, "--regid=" + u, groups,
			"--inh-caps=-all,+ipc_lock", "--ambient-caps=+ipc_lock", "--bounding-set=-all,+ipc_lock", "--"}, argv...)
		dir = v.root
	}
	if v.net != nil && v.net.ns != "" {
		argv = append([]string{"ip", "netns", "exec", v.net.ns}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	return cmd
}

// cloudHVArgsFile records a GPU sandbox's Cloud Hypervisor command line, as
// vm.json records a Firecracker VM's: read back for the root disk it names.
const cloudHVArgsFile = "vm.args"

// rootDiskOf is the root disk a kept VM names, from its vm.json or vm.args.
func rootDiskOf(v *vm) (string, error) {
	if data, err := os.ReadFile(filepath.Join(v.dir, cloudHVArgsFile)); err == nil {
		var args []string
		if err := json.Unmarshal(data, &args); err != nil {
			return "", fmt.Errorf("its %s is unreadable", cloudHVArgsFile)
		}
		for i, a := range args {
			if a == "--disk" && i+1 < len(args) {
				path, _, _ := strings.Cut(strings.TrimPrefix(args[i+1], "path="), ",")
				return path, nil
			}
		}
		return "", fmt.Errorf("its %s names no disk", cloudHVArgsFile)
	}
	data, err := os.ReadFile(v.hostPath("vm.json"))
	if err != nil {
		return "", errors.New("its disk is not known")
	}
	var vc VMConfig
	if json.Unmarshal(data, &vc) != nil {
		return "", errors.New("its vm.json is unreadable")
	}
	for _, d := range vc.Drives {
		if d.IsRootDevice {
			return d.PathOnHost, nil
		}
	}
	return "", errors.New("its vm.json names no root disk")
}
