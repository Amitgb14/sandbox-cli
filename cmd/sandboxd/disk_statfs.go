//go:build linux || darwin

package main

import (
	"path/filepath"
	"syscall"
)

// diskTotalMB is the size of the filesystem holding dir — where sandboxes'
// disks are made — or 0 if it cannot be read. Capacity is measured before the
// backend creates dir, so on a first start it does not exist yet and its
// nearest existing ancestor is measured instead: the filesystem dir will be
// made on. Measuring dir alone reported 0 on every first start.
func diskTotalMB(dir string) int {
	for {
		var st syscall.Statfs_t
		err := syscall.Statfs(dir, &st)
		if err == nil {
			return int(st.Blocks * uint64(st.Bsize) / (1 << 20))
		}
		parent := filepath.Dir(dir)
		if err != syscall.ENOENT || parent == dir {
			return 0
		}
		dir = parent
	}
}
