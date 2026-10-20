//go:build linux

package firecracker

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
)

// guestStatsTimeout is how long a reading waits for a GPU sandbox's guest:
// the guest gives its driver StatsTimeout, and a little more covers the trip.
const guestStatsTimeout = guestproto.StatsTimeout + time.Second

// Bounds on what a guest may say of a GPU. They are wide — no card is near
// them — and exist so that a guest cannot put a number on a chart, or into a
// collector, that would break its scale.
const (
	maxGPUMemoryBytes = 1 << 40 // 1 TiB
	minGPUTempC       = -40
	maxGPUTempC       = 150
	maxGPUPowerW      = 5000
)

// guestUsage asks each GPU sandbox's guest for its account of itself, all at
// once, and bounds what comes back. Every other sandbox is measured on the
// host alone: its VMM's resident memory is the truth there, and asking would
// be a connection into every guest every few seconds for nothing. A guest
// that does not answer in time, or at all (an agent older than StatsSince),
// is left out of this reading.
func guestUsage(ctx context.Context, vms []*vm) map[string]*api.GuestSample {
	out := map[string]*api.GuestSample{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, v := range vms {
		if len(v.spec.GPUs) == 0 || v.client == nil {
			continue
		}
		wg.Add(1)
		go func(v *vm) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, guestStatsTimeout)
			defer cancel()
			st, err := v.client.Stats(ctx)
			if err != nil {
				return
			}
			if g := boundGuestStats(st, int64(v.spec.MemoryMB)<<20, len(v.spec.GPUs)); g != nil {
				mu.Lock()
				out[v.id] = g
				mu.Unlock()
			}
		}(v)
	}
	wg.Wait()
	return out
}

// boundGuestStats is what a guest said, kept only where it could be true:
// memory within what the VM was given, at most the GPUs it was given with
// each index once, and every GPU figure within its bounds. What cannot be
// true is dropped, not clamped, so a chart never shows a made-up number as a
// measured one. nil when nothing is left.
func boundGuestStats(st guestproto.Stats, memLimit int64, gpus int) *api.GuestSample {
	var g api.GuestSample
	total, avail := st.MemTotalBytes, st.MemAvailableBytes
	if total > 0 && total <= memLimit && avail >= 0 && avail <= total {
		g.MemoryTotalBytes, g.MemoryUsedBytes = total, total-avail
	}
	seen := map[int]bool{}
	for _, s := range st.GPUs {
		if len(g.GPUs) == gpus {
			break
		}
		if s.Index < 0 || s.Index >= gpus || seen[s.Index] {
			continue
		}
		seen[s.Index] = true
		out := api.GPUSample{Index: s.Index}
		out.UtilizationPercent = within(s.UtilizationPercent, 0, 100)
		if s.MemoryUsedBytes != nil && s.MemoryTotalBytes != nil {
			used, tot := *s.MemoryUsedBytes, *s.MemoryTotalBytes
			if tot > 0 && tot <= maxGPUMemoryBytes && used >= 0 && used <= tot {
				out.MemoryUsedBytes, out.MemoryTotalBytes = &used, &tot
			}
		}
		out.TemperatureC = within(s.TemperatureC, minGPUTempC, maxGPUTempC)
		out.PowerWatts = within(s.PowerWatts, 0, maxGPUPowerW)
		g.GPUs = append(g.GPUs, out)
	}
	sort.Slice(g.GPUs, func(i, j int) bool { return g.GPUs[i].Index < g.GPUs[j].Index })
	if g.MemoryTotalBytes == 0 && len(g.GPUs) == 0 {
		return nil
	}
	return &g
}

// within is v if it is a finite number in [lo, hi], else nil.
func within(v *float64, lo, hi float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v < lo || *v > hi {
		return nil
	}
	x := *v
	return &x
}
