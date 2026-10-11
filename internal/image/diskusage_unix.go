//go:build unix

package image

import (
	"io/fs"
	"syscall"
)

// diskUsage is the space a file takes, which for a sparse disk is less than
// its size.
func diskUsage(fi fs.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return fi.Size()
}
