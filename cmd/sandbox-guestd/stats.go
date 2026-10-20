package main

import (
	"bufio"
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
)

// nvidiaSMI is the driver's tool, where the GPU kit puts it (scripts/gpu-kit.sh)
// in a root disk the sandbox cannot write. It is named by its full path, never
// looked up on a PATH.
const nvidiaSMI = "/usr/bin/nvidia-smi"

// nvidiaSMIQuery is what OpStats asks the driver, one line per GPU, in this
// order: memory in MiB, power in watts, temperature in °C.
var nvidiaSMIQuery = []string{
	"--query-gpu=index,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw",
	"--format=csv,noheader,nounits",
}

// maxSMIOutput bounds what is read of the tool's output: a line is under 100
// bytes, and a guest has at most a handful of GPUs.
const maxSMIOutput = 64 << 10

// guestStats is OpStats: memory from meminfo, and GPUs from runSMI when the
// guest has a GPU. A driver that does not answer leaves the GPUs out rather
// than failing the reading: memory is still worth having.
func guestStats(ctx context.Context, meminfo io.Reader, hasGPU bool, runSMI func(context.Context) ([]byte, error)) guestproto.Stats {
	var st guestproto.Stats
	st.MemTotalBytes, st.MemAvailableBytes = parseMeminfo(meminfo)
	if hasGPU && runSMI != nil {
		if out, err := runSMI(ctx); err == nil {
			st.GPUs = parseNvidiaSMI(out)
		}
	}
	return st
}

// parseMeminfo is MemTotal and MemAvailable from /proc/meminfo, in bytes; 0
// for one that is missing.
func parseMeminfo(r io.Reader) (total, available int64) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil || n < 0 {
			continue
		}
		// The kernel says kB and means KiB.
		switch f[0] {
		case "MemTotal:":
			total = n << 10
		case "MemAvailable:":
			available = n << 10
		}
	}
	return total, available
}

// parseNvidiaSMI reads nvidiaSMIQuery's output. A line that is not six
// fields, or whose index is not a number, is skipped; a value the driver
// does not have for this card ("[N/A]", "N/A") is left absent.
func parseNvidiaSMI(out []byte) []guestproto.GPUStats {
	var gpus []guestproto.GPUStats
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, ",")
		if len(f) != 6 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		idx, err := strconv.Atoi(f[0])
		if err != nil || idx < 0 {
			continue
		}
		g := guestproto.GPUStats{
			Index:              idx,
			UtilizationPercent: smiFloat(f[1]),
			TemperatureC:       smiFloat(f[4]),
			PowerWatts:         smiFloat(f[5]),
		}
		if v := smiFloat(f[2]); v != nil {
			b := int64(*v) << 20
			g.MemoryUsedBytes = &b
		}
		if v := smiFloat(f[3]); v != nil {
			b := int64(*v) << 20
			g.MemoryTotalBytes = &b
		}
		gpus = append(gpus, g)
	}
	return gpus
}

// smiFloat is one of the tool's numbers, or nil for anything else.
func smiFloat(s string) *float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}
