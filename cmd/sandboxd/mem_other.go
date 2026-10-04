//go:build !darwin

package main

// hostMemMB is the machine's memory: MemTotal on Linux, 0 where there is no
// /proc/meminfo (the operator sets --capacity-memory-mb).
func hostMemMB() int { return memTotalMB("/proc/meminfo") }
