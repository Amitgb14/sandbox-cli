package studioapi

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentusage"
)

// The loop spends a request from the user's own subscription every time it
// fires, so what it decides *not* to do is as much the contract as what it
// does. All three of these are stubbed at the same seams the endpoint uses.

func zeroSnapshot() (agentusage.Snapshot, error) { return agentusage.Snapshot{}, nil }

func staleSnapshot() (agentusage.Snapshot, error) {
	return agentusage.Snapshot{FetchedAt: time.Now().Add(-48 * time.Hour)}, nil
}

func stubUsage(t *testing.T, refreshable bool, snapFn func() (agentusage.Snapshot, error), refresh func(context.Context) error) *int64 {
	t.Helper()
	var calls int64

	oldRefreshable, oldSnapshot, oldRefresh := usageRefreshable, usageSnapshot, usageRefresh
	t.Cleanup(func() {
		usageRefreshable, usageSnapshot, usageRefresh = oldRefreshable, oldSnapshot, oldRefresh
	})

	usageRefreshable = func() bool { return refreshable }
	usageSnapshot = snapFn
	usageRefresh = func(ctx context.Context) error {
		atomic.AddInt64(&calls, 1)
		if refresh != nil {
			return refresh(ctx)
		}
		return nil
	}
	return &calls
}

// Without the agent there is nothing to drive, and a timer would only fail on a
// schedule — every ten minutes, forever, on the ordinary containerised
// deployment where the cache is readable and the binary is absent.
func TestUsageRefreshDoesNotStartWithoutTheAgent(t *testing.T) {
	calls := stubUsage(t, false, zeroSnapshot, nil)

	srv := &Server{}
	if srv.StartUsageRefresh(t.Context(), time.Millisecond) {
		t.Fatal("started a refresh loop on a machine with no agent on PATH")
	}
	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Fatalf("refreshed %d times with no agent available", got)
	}
}

// Zero is off, and off must not depend on the agent being absent to stay off.
func TestUsageRefreshOffAtZero(t *testing.T) {
	calls := stubUsage(t, true, zeroSnapshot, nil)

	srv := &Server{}
	if srv.StartUsageRefresh(t.Context(), 0) {
		t.Fatal("started a refresh loop at interval 0")
	}
	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Fatalf("refreshed %d times while disabled", got)
	}
}

// A reading younger than the interval was already advanced by somebody — the
// refresh button, a sandbox run, the operator's own Claude Code — and buying it
// again spends a request to learn what is already known.
func TestUsageRefreshSkipsAFreshReading(t *testing.T) {
	// Stamped when it is *read*, which is what a reading somebody else keeps
	// current looks like — a fixed "now" from before the loop started would age
	// past a millisecond interval and stop being the case under test.
	calls := stubUsage(t, true, func() (agentusage.Snapshot, error) {
		return agentusage.Snapshot{FetchedAt: time.Now()}, nil
	}, nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	srv := &Server{}
	if !srv.StartUsageRefresh(ctx, 10*time.Millisecond) {
		t.Fatal("refresh loop did not start")
	}
	time.Sleep(80 * time.Millisecond)
	if got := atomic.LoadInt64(calls); got != 0 {
		t.Fatalf("refreshed %d times for a reading taken just now", got)
	}
}

// A stale reading is the case the flag exists for. A failing agent must not stop
// the loop either: the next tick is the retry.
func TestUsageRefreshFiresOnAStaleReadingAndSurvivesFailure(t *testing.T) {
	calls := stubUsage(t, true, staleSnapshot, func(context.Context) error {
		return errors.New("claude: not signed in")
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	srv := &Server{}
	if !srv.StartUsageRefresh(ctx, 10*time.Millisecond) {
		t.Fatal("refresh loop did not start")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(calls) >= 2 {
			return // fired, failed, and came back for the next tick
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("refreshed %d times for a two-day-old reading; wanted repeated attempts", atomic.LoadInt64(calls))
}

// Cancelling the server's context ends the loop rather than leaving a goroutine
// spending requests after shutdown.
func TestUsageRefreshStopsWithTheContext(t *testing.T) {
	calls := stubUsage(t, true, staleSnapshot, nil)

	ctx, cancel := context.WithCancel(context.Background())
	srv := &Server{}
	if !srv.StartUsageRefresh(ctx, 10*time.Millisecond) {
		t.Fatal("refresh loop did not start")
	}
	time.Sleep(60 * time.Millisecond)
	cancel()

	settled := atomic.LoadInt64(calls)
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt64(calls); got != settled {
		t.Fatalf("refreshed %d more times after the context was cancelled", got-settled)
	}
}
