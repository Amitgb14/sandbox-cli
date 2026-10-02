package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// runInit is PID 1 of a Firecracker guest. The kernel command line carries the
// little it needs (sbx.*): the scratch device for the writable layer, the vsock
// port, the hostname, and a DNS server if the host provides one.
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

// sandboxDirs makes the workspace and the sandbox user's home exist and belong
// to that user. An image built for this (the base image) already has them; any
// other image — alpine, say — does not, and a disk built without root cannot
// set ownership, so the guest does it at boot, on its own writable layer.
func sandboxDirs() {
	for _, d := range []string{"/workspace", defaultHome} {
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

func fail(format string, a ...any) {
	fmt.Printf("sandbox-guestd: FATAL: "+format+"\n", a...)
	syscall.Sync()
	_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	for {
		time.Sleep(time.Hour)
	}
}
