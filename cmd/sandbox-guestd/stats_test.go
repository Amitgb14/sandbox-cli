package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseMeminfo(t *testing.T) {
	in := "MemTotal:        8123456 kB\nMemFree:  100 kB\nMemAvailable:    6000000 kB\nBuffers: x kB\n"
	total, avail := parseMeminfo(strings.NewReader(in))
	if total != 8123456<<10 || avail != 6000000<<10 {
		t.Fatalf("got %d, %d", total, avail)
	}
	if total, avail := parseMeminfo(strings.NewReader("nothing\n")); total != 0 || avail != 0 {
		t.Fatalf("empty: %d, %d", total, avail)
	}
}

// The tool's output as a 570-series driver prints it for nvidiaSMIQuery,
// including a card that reports no power draw, and lines that are not GPUs.
func TestParseNvidiaSMI(t *testing.T) {
	out := "0, 37, 2048, 16303, 45, 118.52\n" +
		"1, 0, 0, 16303, 31, [N/A]\n" +
		"No devices were found\n" +
		"x, 1, 2, 3, 4, 5\n" +
		"2, 1, 2\n"
	gpus := parseNvidiaSMI([]byte(out))
	if len(gpus) != 2 {
		t.Fatalf("got %d GPUs: %+v", len(gpus), gpus)
	}
	g := gpus[0]
	if g.Index != 0 || *g.UtilizationPercent != 37 || *g.MemoryUsedBytes != 2048<<20 || *g.MemoryTotalBytes != 16303<<20 ||
		*g.TemperatureC != 45 || *g.PowerWatts != 118.52 {
		t.Fatalf("GPU 0: %+v", g)
	}
	if g := gpus[1]; g.Index != 1 || g.PowerWatts != nil || *g.UtilizationPercent != 0 {
		t.Fatalf("GPU 1: %+v", g)
	}
}

func TestGuestStatsAsksTheDriverOnlyWithAGPU(t *testing.T) {
	mem := "MemTotal: 1024 kB\nMemAvailable: 512 kB\n"
	asked := 0
	smi := func(context.Context) ([]byte, error) { asked++; return []byte("0, 5, 1, 2, 30, 10\n"), nil }

	st := guestStats(context.Background(), strings.NewReader(mem), false, smi)
	if asked != 0 || st.GPUs != nil || st.MemTotalBytes != 1<<20 {
		t.Fatalf("no GPU: asked %d, %+v", asked, st)
	}
	st = guestStats(context.Background(), strings.NewReader(mem), true, smi)
	if asked != 1 || len(st.GPUs) != 1 {
		t.Fatalf("a GPU: asked %d, %+v", asked, st)
	}
	// A driver that fails still leaves the memory.
	st = guestStats(context.Background(), strings.NewReader(mem), true, func(context.Context) ([]byte, error) {
		return nil, errors.New("exit status 9")
	})
	if st.GPUs != nil || st.MemAvailableBytes != 512<<10 {
		t.Fatalf("failing driver: %+v", st)
	}
}
