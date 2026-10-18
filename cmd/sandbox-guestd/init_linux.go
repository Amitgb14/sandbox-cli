package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// runInit is PID 1 of a Firecracker guest. The kernel command line carries the
// little it needs (sbx.*): the scratch device for the writable layer, the vsock
// port, the hostname, a DNS server if the host provides one, and whether the
// sandbox has a GPU whose driver to load (sbx.gpu, gpu_linux.go).
//
// The root disk is the image, shared read-only by every sandbox made from it;
// the scratch disk is this sandbox's own, empty at create. An overlay of the two
// becomes the root, so a new sandbox costs an empty sparse file rather than a
// copy of the image — on a filesystem without reflinks, a copy measured 271 ms
// per GiB (M3).
func runInit() {
	mountEarly()
	reopenConsole()
	args := cmdline()
	haltPowerOff = args["sbx.halt"] == "poweroff"
	if dev := args["sbx.overlay"]; dev != "" {
		if err := overlayRoot(dev); err != nil {
			fail("overlay root on %s: %v", dev, err)
		}
	}
	mountLate()
	if h := args["sbx.hostname"]; h != "" {
		_ = syscall.Sethostname([]byte(h))
	}
	if dns := args["sbx.dns"]; dns != "" {
		_ = os.WriteFile("/etc/resolv.conf", []byte("nameserver "+dns+"\n"), 0o644)
	}
	sandboxDirs()
	for _, v := range parseVolumes(args["sbx.volumes"]) {
		// A volume that cannot be mounted fails the boot: running without it
		// would put the agent's writes on the scratch disk, to vanish with the
		// VM, while it believed they were kept.
		if err := mountVolume(v); err != nil {
			fail("volume %s at %s: %v", v.dev, v.path, err)
		}
	}
	// A GPU sandbox's driver, from the kit sandboxd put in the root disk. A
	// sandbox that was given a GPU and cannot use it fails to boot, so the
	// create says so rather than handing over a sandbox with no GPU.
	if args["sbx.gpu"] == "1" {
		if err := loadGPU(); err != nil {
			fail("GPU: %v", err)
		}
	}
	if err := loopbackUp(); err != nil {
		fmt.Printf("sandbox-guestd: loopback: %v\n", err)
	}
	port := args["sbx.vsock"]
	if port == "" {
		port = "5000"
	}
	supervise(port)
}

func mountEarly() {
	for _, m := range []struct{ src, dst, typ string }{
		{"proc", "/proc", "proc"},
		{"sysfs", "/sys", "sysfs"},
		{"devtmpfs", "/dev", "devtmpfs"},
		{"tmpfs", "/run", "tmpfs"},
	} {
		_ = os.MkdirAll(m.dst, 0o755)
		_ = syscall.Mount(m.src, m.dst, m.typ, syscall.MS_NOSUID, "")
	}
}

// mountLate mounts what belongs on the final root.
func mountLate() {
	_ = os.MkdirAll("/dev/pts", 0o755)
	_ = syscall.Mount("devpts", "/dev/pts", "devpts", syscall.MS_NOSUID|syscall.MS_NOEXEC, "ptmxmode=0666,mode=0620")
	_ = os.MkdirAll("/dev/shm", 0o1777)
	_ = syscall.Mount("tmpfs", "/dev/shm", "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, "")
	_ = syscall.Mount("tmpfs", "/run", "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, "mode=0755")
}

// reopenConsole attaches stdio to the console: an image built without device
// nodes gives init none.
func reopenConsole() {
	if c, err := os.OpenFile("/dev/console", os.O_RDWR, 0); err == nil {
		for fd := 0; fd <= 2; fd++ {
			_ = syscall.Dup3(int(c.Fd()), fd, 0)
		}
	}
}

func cmdline() map[string]string {
	out := map[string]string{}
	b, _ := os.ReadFile("/proc/cmdline")
	for _, f := range strings.Fields(string(b)) {
		if k, v, ok := strings.Cut(f, "="); ok && strings.HasPrefix(k, "sbx.") {
			out[k] = v
		}
	}
	return out
}

// overlayRoot makes image + scratch the root and pivots into it.
func overlayRoot(dev string) error {
	const base = "/run/sbx"
	rw, newRoot := base+"/rw", base+"/root"
	for _, d := range []string{rw, newRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := syscall.Mount(dev, rw, "ext4", syscall.MS_NOSUID|syscall.MS_NODEV, ""); err != nil {
		return fmt.Errorf("mount %s: %w", dev, err)
	}
	if err := growToDevice(rw, dev); err != nil {
		fmt.Printf("sandbox-guestd: growing the scratch filesystem: %v\n", err)
	}
	// Kept open for the agent, which grows the filesystem while it runs
	// (OpGrowScratch): after the pivot below its mount is reachable by no
	// path, only through a descriptor opened on it now.
	if d, err := os.Open(rw); err == nil {
		scratchDir, scratchDev = d, dev
	}
	for _, d := range []string{rw + "/upper", rw + "/work"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	opts := "lowerdir=/,upperdir=" + rw + "/upper,workdir=" + rw + "/work"
	if err := syscall.Mount("overlay", newRoot, "overlay", 0, opts); err != nil {
		return fmt.Errorf("mount overlay: %w", err)
	}
	for _, m := range []string{"/proc", "/sys", "/dev"} {
		_ = os.MkdirAll(newRoot+m, 0o755)
		if err := syscall.Mount(m, newRoot+m, "", syscall.MS_MOVE, ""); err != nil {
			return fmt.Errorf("move %s: %w", m, err)
		}
	}
	if err := os.Chdir(newRoot); err != nil {
		return err
	}
	if err := os.MkdirAll(".old", 0o700); err != nil {
		return err
	}
	if err := syscall.PivotRoot(".", ".old"); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}
	if err := os.Chdir("/"); err != nil {
		return err
	}
	// The scratch mount stays alive under the overlay; the old tree is detached.
	_ = syscall.Unmount("/.old", syscall.MNT_DETACH)
	_ = os.Remove("/.old")
	return nil
}

// Linux ioctls: a block device's size in bytes, and an online resize of the
// ext4 filesystem mounted where the descriptor is, to a count of blocks.
const (
	blkGetSize64    = 0x80081272 // BLKGETSIZE64
	ext4IocResizeFS = 0x40086610 // EXT4_IOC_RESIZE_FS
)

// growToDevice grows the ext4 filesystem mounted at dir to fill dev. A
// resize grows the scratch file on the host and nothing else: the host never
// reads the filesystem the guest writes, so the guest's own kernel grows it
// here, on every boot, which is nothing when it already fills the device.
func growToDevice(dir, dev string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	size, err := deviceSize(dev)
	if err != nil {
		return err
	}
	return growFS(f, size)
}

// The scratch filesystem's mount, opened before the pivot, and its device;
// nil and "" when the guest booted without an overlay.
var (
	scratchDir *os.File
	scratchDev string
)

// scratchGrower is OpGrowScratch for the agent: f is the scratch mount
// (passed down from PID 1), dev its device. The host has lengthened the file
// and told the VMM; the guest's kernel learns the new size a moment later,
// so it waits, briefly, for the device to be at least min bytes.
func scratchGrower(f *os.File, dev string) func(min int64) error {
	// Inherited without close-on-exec: set it, or every process the agent
	// starts for the sandbox user would hold the raw scratch mount, under
	// the overlay, open.
	syscall.CloseOnExec(int(f.Fd()))
	return func(min int64) error {
		deadline := time.Now().Add(5 * time.Second)
		for {
			size, err := deviceSize(dev)
			if err != nil {
				return err
			}
			if size >= uint64(min) {
				return growFS(f, size)
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("%s is %d bytes, not the %d asked for", dev, size, min)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// deviceSize is a block device's size in bytes.
func deviceSize(dev string) (uint64, error) {
	d, err := os.Open(dev)
	if err != nil {
		return 0, err
	}
	defer d.Close()
	var size uint64
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, d.Fd(), blkGetSize64, uintptr(unsafe.Pointer(&size))); errno != 0 {
		return 0, fmt.Errorf("the size of %s: %w", dev, errno)
	}
	return size, nil
}

// growFS grows the ext4 filesystem mounted where f is open to size bytes.
func growFS(f *os.File, size uint64) error {
	var st syscall.Statfs_t
	if err := syscall.Fstatfs(int(f.Fd()), &st); err != nil {
		return err
	}
	if st.Bsize <= 0 {
		return fmt.Errorf("block size %d", st.Bsize)
	}
	blocks := size / uint64(st.Bsize)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ext4IocResizeFS, uintptr(unsafe.Pointer(&blocks))); errno != 0 {
		return fmt.Errorf("resize to %d blocks: %w", blocks, errno)
	}
	return nil
}

// sandboxDirs makes the sandbox user's home exist and belong to that user. An
// image built for this (the base image) already has it; any
// other image — alpine, say — does not, and a disk built without root cannot
// set ownership, so the guest does it at boot, on its own writable layer.
func sandboxDirs() {
	for _, d := range []string{defaultHome} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Printf("sandbox-guestd: %s: %v\n", d, err)
			continue
		}
		if fi, err := os.Stat(d); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid == defaultUID {
				continue
			}
		}
		_ = os.Chown(d, defaultUID, defaultGID)
	}
}

// mountVolume mounts one volume drive. Its root goes to the sandbox user on a
// writable mount — a new volume's root belongs to whoever formatted it — and so
// does any directory this made to reach the mount point under the sandbox
// user's home, which would otherwise be root's and unwritable beside it.
func mountVolume(v volumeMount) error {
	var made []string
	for d := v.path; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil {
			break
		}
		made = append(made, d)
	}
	if err := os.MkdirAll(v.path, 0o755); err != nil {
		return err
	}
	for _, d := range made {
		if strings.HasPrefix(d, defaultHome+"/") {
			_ = os.Chown(d, defaultUID, defaultGID)
		}
	}
	flags := uintptr(syscall.MS_NOSUID | syscall.MS_NODEV)
	if v.readOnly {
		flags |= syscall.MS_RDONLY
	}
	if err := syscall.Mount(v.dev, v.path, "ext4", flags, ""); err != nil {
		if !v.readOnly || err != syscall.EROFS {
			return err
		}
		// A volume left dirty — its last VM crashed rather than stopping —
		// needs its journal replayed, which a read-only drive cannot do.
		// noload skips the journal and shows the last committed state.
		if err := syscall.Mount(v.dev, v.path, "ext4", flags, "noload"); err != nil {
			return err
		}
		fmt.Printf("sandbox-guestd: volume %s was not cleanly stopped; mounted read-only without replaying its journal\n", v.path)
	}
	if !v.readOnly {
		if fi, err := os.Stat(v.path); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != defaultUID {
				_ = os.Chown(v.path, defaultUID, defaultGID)
			}
		}
	}
	return nil
}

// loopbackUp brings lo up — the kernel's ip= configuration sets up eth0 only,
// and a dev server inside the sandbox expects 127.0.0.1 to answer.
func loopbackUp() error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	var ifr struct {
		name  [16]byte
		flags uint16
		_     [22]byte
	}
	copy(ifr.name[:], "lo")
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCGIFFLAGS, uintptr(unsafe.Pointer(&ifr))); e != 0 {
		return e
	}
	ifr.flags |= syscall.IFF_UP
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), syscall.SIOCSIFFLAGS, uintptr(unsafe.Pointer(&ifr))); e != 0 {
		return e
	}
	return nil
}

// supervise runs the agent's server as a child and reaps every orphan in the
// guest, which is PID 1's other job. The server is restarted if it dies; after
// repeated deaths the VM is powered off, since a sandbox the host cannot talk to
// is one nobody can stop cleanly from inside.
func supervise(port string) {
	self, err := os.Executable()
	if err != nil {
		self = "/sbin/sandbox-guestd"
	}
	var child *exec.Cmd
	start := func() {
		child = exec.Command(self, "serve", "--vsock", port)
		if scratchDir != nil {
			child.ExtraFiles = []*os.File{scratchDir} // fd 3
			child.Args = append(child.Args, "--scratch-fd", "3", "--scratch-dev", scratchDev)
		}
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			fail("starting the agent: %v", err)
		}
		fmt.Printf("sandbox-guestd: ready on vsock port %s\n", port)
	}
	start()
	deaths := 0
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, 0, nil)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if pid == child.Process.Pid {
			deaths++
			fmt.Printf("sandbox-guestd: the agent exited (%v), restart %d\n", ws, deaths)
			if deaths > 5 {
				fail("the agent keeps dying")
			}
			start()
		}
	}
}

// haltPowerOff is sbx.halt=poweroff: how a failed boot ends on a VMM that
// resets a guest that reboots (Cloud Hypervisor) rather than exiting, as
// Firecracker does. Powered off, the VMM exits and the create fails at once.
var haltPowerOff bool

func fail(format string, a ...any) {
	fmt.Printf("sandbox-guestd: FATAL: "+format+"\n", a...)
	syscall.Sync()
	if haltPowerOff {
		_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
	}
	_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	for {
		time.Sleep(time.Hour)
	}
}

// setClock is OpSetClock: the agent runs as root in the guest, which may set
// the clock. The guest has no clock source of its own that would correct it
// after a snapshot restore, so the host's word is the time.
func setClock(t time.Time) error {
	tv := syscall.NsecToTimeval(t.UnixNano())
	return syscall.Settimeofday(&tv)
}
