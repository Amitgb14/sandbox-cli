//go:build unix

package guestproto

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stats carries the guest's account to the host, says when the guest has
// none to give, and does not let a wedged stats function hold the
// connection past StatsTimeout.
func TestStats(t *testing.T) {
	util, used, total := 37.0, int64(2<<30), int64(16<<30)
	var fails bool
	c := serveWith(t, &Server{Root: t.TempDir(), UID: -1, GID: -1, Stats: func(context.Context) (Stats, error) {
		if fails {
			return Stats{}, errors.New("no meminfo")
		}
		return Stats{MemTotalBytes: 8 << 30, MemAvailableBytes: 6 << 30, GPUs: []GPUStats{
			{Index: 0, UtilizationPercent: &util, MemoryUsedBytes: &used, MemoryTotalBytes: &total},
		}}, nil
	}})
	ctx := context.Background()
	st, err := c.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.MemTotalBytes != 8<<30 || st.MemAvailableBytes != 6<<30 || len(st.GPUs) != 1 ||
		*st.GPUs[0].UtilizationPercent != util || *st.GPUs[0].MemoryUsedBytes != used || st.GPUs[0].TemperatureC != nil {
		t.Fatalf("stats: %+v", st)
	}
	fails = true
	if _, err := c.Stats(ctx); !IsCode(err, CodeInternal) {
		t.Fatalf("a failing stats: %v", err)
	}

	// An agent with no stats to give — the host's view of an agent older
	// than StatsSince is the same refusal.
	none := serveWith(t, &Server{Root: t.TempDir(), UID: -1, GID: -1})
	if _, err := none.Stats(ctx); !IsCode(err, CodeBadRequest) {
		t.Fatalf("a guest without stats: %v", err)
	}
}

func TestStatsBoundedInTheGuest(t *testing.T) {
	if testing.Short() {
		t.Skip("waits StatsTimeout")
	}
	c := serveWith(t, &Server{Root: t.TempDir(), UID: -1, GID: -1, Stats: func(ctx context.Context) (Stats, error) {
		<-ctx.Done() // a driver tool that never answers
		return Stats{}, ctx.Err()
	}})
	start := time.Now()
	if _, err := c.Stats(context.Background()); err == nil {
		t.Fatal("a stats that never answered came back ok")
	}
	if d := time.Since(start); d > StatsTimeout+time.Second {
		t.Fatalf("the guest held the connection %v", d)
	}
}
