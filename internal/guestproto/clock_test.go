//go:build unix

package guestproto

import (
	"context"
	"testing"
	"time"
)

// set_clock gives the guest's clock setter the host's time to the
// nanosecond, and a guest agent that sets no clock (tests, or an agent that
// is not the guest's own) says so rather than answering ok.
func TestSetClock(t *testing.T) {
	var got []time.Time
	c := serveWith(t, &Server{Root: t.TempDir(), UID: -1, GID: -1, SetClock: func(at time.Time) error {
		got = append(got, at)
		return nil
	}})
	ctx := context.Background()
	at := time.Date(2026, 10, 15, 6, 30, 0, 123456789, time.UTC)
	if err := c.SetClock(ctx, at); err != nil || len(got) != 1 || !got[0].Equal(at) {
		t.Fatalf("set_clock: %v, set %v; want %v", err, got, at)
	}
	if err := c.SetClock(ctx, time.Unix(0, 0)); err == nil || len(got) != 1 {
		t.Fatalf("the epoch itself: %v, set %v; want refused", err, got)
	}

	none := serveWith(t, &Server{Root: t.TempDir(), UID: -1, GID: -1})
	if err := none.SetClock(ctx, at); err == nil {
		t.Fatal("a guest agent that sets no clock answered ok")
	}
}
