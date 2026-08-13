package studioapi

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentusage"
)

// The loop spends a request from the user's own subscription every time it
// fires, so what it decides *not* to do is as much the contract as what it
// does.
//
// Every test here joins the goroutine before it returns. Restoring these seams
// while a tick is in flight is a data race — `go test -race` reports it — and
// the consequence is worse than a failed test: the restored `usageRefresh` is
// the real one, so a loop still running would spawn an actual `claude -p` and
// spend a request from whoever ran the suite.

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
	if srv.startUsageRefresh(t.Context(), time.Millisecond) != nil {
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
	if srv.startUsageRefresh(t.Context(), 0) != nil {
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
	srv := &Server{}
	done := srv.startUsageRefresh(ctx, 10*time.Millisecond)
	if done == nil {
		t.Fatal("refresh loop did not start")
	}
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done // joined, so the seams can be restored without racing a tick

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
	srv := &Server{}
	done := srv.startUsageRefresh(ctx, 10*time.Millisecond)
	if done == nil {
		t.Fatal("refresh loop did not start")
	}

	got := int64(0)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got = atomic.LoadInt64(calls); got >= 2 {
			break // fired, failed, and came back for the next tick
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if got < 2 {
		t.Fatalf("refreshed %d times for a two-day-old reading; wanted repeated attempts", got)
	}
}

// Cancelling the server's context ends the loop rather than leaving a goroutine
// spending requests after shutdown.
func TestUsageRefreshStopsWithTheContext(t *testing.T) {
	calls := stubUsage(t, true, staleSnapshot, nil)

	ctx, cancel := context.WithCancel(context.Background())
	srv := &Server{}
	done := srv.startUsageRefresh(ctx, 10*time.Millisecond)
	if done == nil {
		t.Fatal("refresh loop did not start")
	}
	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop was still running two seconds after its context was cancelled")
	}

	// Joined, so this is a settled count rather than a sampled one.
	settled := atomic.LoadInt64(calls)
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt64(calls); got != settled {
		t.Fatalf("refreshed %d more times after the loop reported it had stopped", got-settled)
	}
}

// The loop's own refresh advances the stamp it later reads, so comparing the
// next tick against the whole interval measured that refresh as fresh and
// skipped — firing every *other* tick, and reaching twenty minutes of staleness
// at a setting that says ten. The threshold sits below the interval for exactly
// this, and this test is the difference between the two.
func TestUsageRefreshDoesNotSkipOnItsOwnRefresh(t *testing.T) {
	var mu sync.Mutex
	stamp := time.Now().Add(-48 * time.Hour)

	calls := stubUsage(t, true,
		func() (agentusage.Snapshot, error) {
			mu.Lock()
			defer mu.Unlock()
			return agentusage.Snapshot{FetchedAt: stamp}, nil
		},
		func(context.Context) error {
			// What a real refresh does: Claude Code stamps the cache ~now, so the
			// next tick sees a reading almost exactly one interval old.
			mu.Lock()
			defer mu.Unlock()
			stamp = time.Now()
			return nil
		})

	ctx, cancel := context.WithCancel(t.Context())
	srv := &Server{}
	done := srv.startUsageRefresh(ctx, 20*time.Millisecond)
	if done == nil {
		t.Fatal("refresh loop did not start")
	}

	got := int64(0)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got = atomic.LoadInt64(calls); got >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if got < 3 {
		t.Fatalf("refreshed %d times in ~5s at a 20ms interval: the loop is skipping ticks "+
			"because its own refresh moved the stamp", got)
	}
}

// The reading this exists to fix is already old when the daemon starts, so
// waiting a full interval before the first look serves a stale number for
// exactly as long as the setting says.
func TestUsageRefreshChecksBeforeTheFirstTick(t *testing.T) {
	calls := stubUsage(t, true, staleSnapshot, nil)

	ctx, cancel := context.WithCancel(t.Context())
	srv := &Server{}
	// An interval far longer than this test will wait: anything it does must
	// therefore have happened before the first tick.
	done := srv.startUsageRefresh(ctx, time.Hour)
	if done == nil {
		t.Fatal("refresh loop did not start")
	}

	got := int64(0)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got = atomic.LoadInt64(calls); got >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if got < 1 {
		t.Fatal("a two-day-old reading went unrefreshed at startup; the first check waited for a tick")
	}
}
