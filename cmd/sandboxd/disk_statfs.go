//go:build linux || darwin

package main

import "syscall"

// diskTotalMB is the size of the filesystem holding dir — where sandboxes'
// disks are made — or 0 if it cannot be read (dir not created yet, say).
func diskTotalMB(dir string) int {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0
	}
	return int(st.Blocks * uint64(st.Bsize) / (1 << 20))
}
