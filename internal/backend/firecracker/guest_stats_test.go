//go:build linux

package firecracker

import (
	"math"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
)

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

// The guest is where the agent runs, and may say anything: only what could
// be true of the VM it was given reaches a sample.
func TestBoundGuestStats(t *testing.T) {
	const limit = 8 << 30
	good := guestproto.GPUStats{Index: 0, UtilizationPercent: f64(40), MemoryUsedBytes: i64(1 << 30), MemoryTotalBytes: i64(16 << 30), TemperatureC: f64(50), PowerWatts: f64(120)}

	g := boundGuestStats(guestproto.Stats{MemTotalBytes: 7 << 30, MemAvailableBytes: 5 << 30, GPUs: []guestproto.GPUStats{good}}, limit, 1)
	if g == nil || g.MemoryTotalBytes != 7<<30 || g.MemoryUsedBytes != 2<<30 || len(g.GPUs) != 1 || *g.GPUs[0].UtilizationPercent != 40 {
		t.Fatalf("a true account: %+v", g)
	}

	cases := map[string]guestproto.Stats{
		"more memory than the VM was given": {MemTotalBytes: limit + 1, MemAvailableBytes: 0},
		"more available than total":         {MemTotalBytes: 1 << 30, MemAvailableBytes: 2 << 30},
		"negative available":                {MemTotalBytes: 1 << 30, MemAvailableBytes: -1},
		"a GPU it was not given":            {GPUs: []guestproto.GPUStats{{Index: 1, UtilizationPercent: f64(1)}}},
		"a negative index":                  {GPUs: []guestproto.GPUStats{{Index: -1, UtilizationPercent: f64(1)}}},
	}
	for name, st := range cases {
		if g := boundGuestStats(st, limit, 1); g != nil {
			t.Errorf("%s: kept %+v", name, g)
		}
	}

	// Each figure out of bounds is dropped on its own; the GPU stays.
	bad := guestproto.GPUStats{Index: 0, UtilizationPercent: f64(101), MemoryUsedBytes: i64(17 << 30), MemoryTotalBytes: i64(16 << 30),
		TemperatureC: f64(math.Inf(1)), PowerWatts: f64(-3)}
	g = boundGuestStats(guestproto.Stats{GPUs: []guestproto.GPUStats{bad}}, limit, 1)
	if g == nil || len(g.GPUs) != 1 {
		t.Fatalf("bad figures: %+v", g)
	}
	if b := g.GPUs[0]; b.UtilizationPercent != nil || b.MemoryUsedBytes != nil || b.MemoryTotalBytes != nil || b.TemperatureC != nil || b.PowerWatts != nil {
		t.Fatalf("an out-of-bounds figure survived: %+v", b)
	}
	if g := boundGuestStats(guestproto.Stats{GPUs: []guestproto.GPUStats{{Index: 0, MemoryUsedBytes: i64(1), MemoryTotalBytes: i64(2 << 40)}}}, limit, 1); g.GPUs[0].MemoryTotalBytes != nil {
		t.Fatal("a card over 1 TiB was kept")
	}

	// No more GPUs than given, each once, in order.
	many := []guestproto.GPUStats{{Index: 1, UtilizationPercent: f64(1)}, {Index: 0, UtilizationPercent: f64(2)}, {Index: 0, UtilizationPercent: f64(3)}, {Index: 2}}
	g = boundGuestStats(guestproto.Stats{GPUs: many}, limit, 2)
	if len(g.GPUs) != 2 || g.GPUs[0].Index != 0 || *g.GPUs[0].UtilizationPercent != 2 || g.GPUs[1].Index != 1 {
		t.Fatalf("many: %+v", g.GPUs)
	}
}
