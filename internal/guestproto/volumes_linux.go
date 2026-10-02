package guestproto

import (
	"bufio"
	"os"
	"strings"
	"syscall"
)

// finalizeVolumes remounts every writable volume read-only before the VM is
// stopped. A remount commits the journal and marks the filesystem clean; one
// left dirty cannot be mounted read-only by the next VM, whose drive is
// read-only and so cannot replay a journal. Volumes are the drives after the
// root and scratch disks, /dev/vdc onwards. Best-effort: a file still open for
// writing makes the remount fail, and the sync before it has already written
// the data out.
func finalizeVolumes() {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || !isVolumeDevice(fields[0]) {
			continue
		}
		if !strings.Contains(","+fields[3]+",", ",rw,") {
			continue
		}
		_ = syscall.Mount("", fields[1], "", syscall.MS_REMOUNT|syscall.MS_RDONLY, "")
	}
}

func isVolumeDevice(dev string) bool {
	return len(dev) == len("/dev/vdc") && strings.HasPrefix(dev, "/dev/vd") && dev[7] >= 'c' && dev[7] <= 'z'
}
