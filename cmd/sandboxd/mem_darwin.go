//go:build darwin

package main

import (
	"encoding/binary"
	"syscall"
)

// hostMemMB is the machine's memory, from the hw.memsize sysctl. macOS has no
// /proc/meminfo, and reading it there reported 0: a gateway took the node for
// one with no room and queued everything sent to it.
func hostMemMB() int {
	s, err := syscall.Sysctl("hw.memsize")
	if err != nil || len(s) == 0 || len(s) > 8 {
		return 0
	}
	// Sysctl returns the raw little-endian uint64 as a string with one
	// trailing NUL dropped, taking the top byte of every size under 2^56
	// with it; pad it back before reading the number.
	var b [8]byte
	copy(b[:], s)
	return int(binary.LittleEndian.Uint64(b[:]) / (1 << 20))
}
