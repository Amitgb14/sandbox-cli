//go:build linux

package firecracker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// clockTicks is the kernel's USER_HZ, the unit of /proc/<pid>/stat's CPU
// times: 100 on every Linux this runs on, and not readable without cgo.
const clockTicks = 100

// Usage is backend.UsageReader, read on the host: the VMM's process for CPU
// time, resident memory and disk I/O (a guest's memory and disk are the
// VMM's), and the tap device for network, counted from the guest's side.
// Nothing here comes from inside the guest. A VM whose counters cannot be
// read is left out of the reading rather than reported as idle.
func (b *Backend) Usage(context.Context) (map[string]backend.Usage, error) {
	b.mu.Lock()
	vms := make([]*vm, 0, len(b.vms))
	for _, v := range b.vms {
		vms = append(vms, v)
	}
	b.mu.Unlock()
	out := map[string]backend.Usage{}
	for _, v := range vms {
		if v.pid <= 1 || v.suspended {
			continue
		}
		u, err := processUsage(fmt.Sprintf("/proc/%d", v.pid))
		if err != nil {
			continue
		}
		u.MemoryLimitBytes = int64(v.spec.MemoryMB) << 20
		if v.net != nil {
			// The tap's receive is what the guest sent, and its send what
			// the guest received.
			stats := filepath.Join("/sys/class/net", v.net.name, "statistics")
			if n, err := readInt(filepath.Join(stats, "rx_bytes")); err == nil {
				u.NetTxBytes = int64(n)
			}
			if n, err := readInt(filepath.Join(stats, "tx_bytes")); err == nil {
				u.NetRxBytes = int64(n)
			}
		}
		out[v.id] = u
	}
	return out, nil
}

// processUsage reads a process's CPU time, resident memory and disk I/O from
// its /proc directory.
func processUsage(dir string) (backend.Usage, error) {
	stat, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return backend.Usage{}, err
	}
	ticks, err := parseStatCPU(string(stat))
	if err != nil {
		return backend.Usage{}, err
	}
	u := backend.Usage{CPUUsec: ticks * 1_000_000 / clockTicks}
	if statm, err := os.ReadFile(filepath.Join(dir, "statm")); err == nil {
		u.MemoryBytes = parseStatmResident(string(statm)) * int64(os.Getpagesize())
	}
	if io, err := os.ReadFile(filepath.Join(dir, "io")); err == nil {
		u.DiskReadBytes, u.DiskWriteBytes = parseProcIO(string(io))
	}
	return u, nil
}

// parseStatCPU is utime + stime from /proc/<pid>/stat, in clock ticks. The
// command name, field 2, is in parentheses and may hold spaces, so fields are
// counted from the last ')'.
func parseStatCPU(stat string) (int64, error) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, fmt.Errorf("stat: no command name")
	}
	f := strings.Fields(stat[i+1:])
	// After the name: state is field 3, so utime (14) and stime (15) are at
	// 11 and 12 here.
	if len(f) < 13 {
		return 0, fmt.Errorf("stat: %d fields after the name", len(f))
	}
	ut, err1 := strconv.ParseInt(f[11], 10, 64)
	st, err2 := strconv.ParseInt(f[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, fmt.Errorf("stat: utime %q, stime %q", f[11], f[12])
	}
	return ut + st, nil
}

// parseStatmResident is the resident pages from /proc/<pid>/statm.
func parseStatmResident(statm string) int64 {
	f := strings.Fields(statm)
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.ParseInt(f[1], 10, 64)
	return n
}

// parseProcIO is read_bytes and write_bytes from /proc/<pid>/io: what reached
// the disk, not what passed through the page cache.
func parseProcIO(io string) (read, write int64) {
	for _, line := range strings.Split(io, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		switch strings.TrimSpace(k) {
		case "read_bytes":
			read = n
		case "write_bytes":
			write = n
		}
	}
	return read, write
}
