//go:build linux

package firecracker

import (
	"path/filepath"
	"testing"
)

// The VMM's /proc files are read as the kernel writes them: a command name
// with spaces and parentheses does not shift the CPU fields.
func TestParseProcFiles(t *testing.T) {
	stat := "4242 (fire cracker (x)) S 1 4242 4242 0 -1 4194560 3300 0 0 0 731 269 0 0 20 0 3 0 12345 0 0"
	if ticks, err := parseStatCPU(stat); err != nil || ticks != 1000 {
		t.Fatalf("utime+stime: %d, %v; want 1000", ticks, err)
	}
	if _, err := parseStatCPU("garbage"); err == nil {
		t.Error("a stat with no command name parsed")
	}
	if n := parseStatmResident("262144 51200 1024 1 0 70000 0"); n != 51200 {
		t.Errorf("resident %d", n)
	}
	r, w := parseProcIO("rchar: 1\nwchar: 2\nsyscr: 3\nsyscw: 4\nread_bytes: 4096\nwrite_bytes: 8192\ncancelled_write_bytes: 0\n")
	if r != 4096 || w != 8192 {
		t.Errorf("io %d %d", r, w)
	}
	// This process's own directory, as a VMM's would be read.
	u, err := processUsage(filepath.Join("/proc", "self"))
	if err != nil || u.CPUUsec < 0 || u.MemoryBytes <= 0 {
		t.Fatalf("/proc/self: %+v, %v", u, err)
	}
}
