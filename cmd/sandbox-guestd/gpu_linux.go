package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// GPUKitDir is where sandboxd puts a GPU kit's kernel modules in a GPU
// sandbox's root disk (internal/image, InjectDir), with modules.list naming
// them in load order. Nothing in an image is ever loaded: the kit is the
// operator's, written by sandboxd, and loaded only when the kernel command line
// says this sandbox has a GPU (sbx.gpu=1).
const GPUKitDir = "/usr/lib/sandbox-gpu"

// loadGPU loads the kit's modules and makes their device nodes. Every process
// after this runs as the sandbox user, so the nodes are made readable and
// writable by all: the GPU is this sandbox's alone.
func loadGPU() error {
	list, err := os.ReadFile(filepath.Join(GPUKitDir, "modules.list"))
	if err != nil {
		return fmt.Errorf("no GPU kit in the root disk: %w", err)
	}
	for _, name := range strings.Fields(string(list)) {
		if strings.Contains(name, "/") {
			return fmt.Errorf("modules.list: %q is not a file name", name)
		}
		if err := finitModule(filepath.Join(GPUKitDir, name)); err != nil {
			return fmt.Errorf("loading %s: %w", name, err)
		}
	}
	f, err := os.Open("/proc/devices")
	if err != nil {
		return err
	}
	defer f.Close()
	gpus, _ := os.ReadDir("/proc/driver/nvidia/gpus")
	_, modeset := os.Stat("/sys/module/nvidia_modeset")
	for _, n := range nvidiaNodes(charMajors(f), len(gpus), modeset == nil) {
		_ = os.Remove(n.path)
		if err := syscall.Mknod(n.path, syscall.S_IFCHR|0o666, int(mkdev(n.major, n.minor))); err != nil {
			return fmt.Errorf("%s: %w", n.path, err)
		}
		// Mknod is subject to the umask; the mode is meant.
		_ = os.Chmod(n.path, 0o666)
	}
	if len(gpus) == 0 {
		return fmt.Errorf("the driver loaded but found no GPU")
	}
	return nil
}

// finitModule loads the kernel module at path, with no parameters.
func finitModule(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	nr := map[string]uintptr{"amd64": 313, "arm64": 273}[runtime.GOARCH]
	if nr == 0 {
		return fmt.Errorf("finit_module: unknown on %s", runtime.GOARCH)
	}
	params := []byte{0}
	if _, _, errno := syscall.Syscall(nr, f.Fd(), uintptr(unsafe.Pointer(&params[0])), 0); errno != 0 {
		return errno
	}
	return nil
}

// charMajors reads /proc/devices: each character device's name and major.
func charMajors(r io.Reader) map[string]uint32 {
	out := map[string]uint32{}
	sc := bufio.NewScanner(r)
	chars := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "Character devices:":
			chars = true
		case line == "Block devices:":
			chars = false
		case chars:
			if f := strings.Fields(line); len(f) == 2 {
				if n, err := strconv.ParseUint(f[0], 10, 32); err == nil {
					out[f[1]] = uint32(n)
				}
			}
		}
	}
	return out
}

type devNode struct {
	path         string
	major, minor uint32
}

// nvidiaNodes is what nvidia-modprobe would make, for the majors the driver
// registered: one node per GPU and the control node on the driver's major
// (named "nvidia-frontend" by current drivers, "nvidia" by older ones), the
// modeset node beside them when nvidia-modeset is loaded (it has no major of
// its own), and CUDA's memory manager on its own major.
func nvidiaNodes(majors map[string]uint32, gpus int, modeset bool) []devNode {
	var out []devNode
	major, ok := majors["nvidia-frontend"]
	if !ok {
		major, ok = majors["nvidia"]
	}
	if ok {
		for i := 0; i < gpus; i++ {
			out = append(out, devNode{fmt.Sprintf("/dev/nvidia%d", i), major, uint32(i)})
		}
		out = append(out, devNode{"/dev/nvidiactl", major, 255})
		if modeset {
			out = append(out, devNode{"/dev/nvidia-modeset", major, 254})
		}
	}
	if uvm, ok := majors["nvidia-uvm"]; ok {
		out = append(out, devNode{"/dev/nvidia-uvm", uvm, 0}, devNode{"/dev/nvidia-uvm-tools", uvm, 1})
	}
	return out
}

// mkdev is the kernel's new_encode_dev, as glibc's makedev.
func mkdev(major, minor uint32) uint64 {
	return uint64(minor&0xff) | uint64(major&0xfff)<<8 | uint64(minor&^0xff)<<12 | uint64(major&^0xfff)<<32
}
