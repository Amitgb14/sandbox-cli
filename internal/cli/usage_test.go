package cli

import (
	"testing"
	"time"
)

func TestResetCells(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.Local)
	cases := []struct {
		name     string
		at       time.Time
		wantLeft string
		wantWhen string
	}{
		{"later today", now.Add(2*time.Hour + 14*time.Minute), "resets in 2h14m", "(14:14)"},
		{"within the hour", now.Add(9 * time.Minute), "resets in 9m", "(12:09)"},
		{"days out names the day", now.Add(4 * 24 * time.Hour), "resets in 4d", "(Wed 12:00)"},
		{"on the hour drops the minutes", now.Add(3 * time.Hour), "resets in 3h", "(15:00)"},
		// Past its reset: the reading is stale, not negative.
		{"already due", now.Add(-time.Minute), "due", "(11:59)"},
		// A window with no reset time still reports its percentage, so the cell
		// has to say what is missing rather than render a zero time as midnight.
		{"not reported", time.Time{}, "reset time not reported", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left, when := resetCells(tc.at, now)
			if left != tc.wantLeft || when != tc.wantWhen {
				t.Errorf("resetCells = %q %q, want %q %q", left, when, tc.wantLeft, tc.wantWhen)
			}
		})
	}
}

func TestHumanLeft(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "0m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
		{23*time.Hour + 59*time.Minute, "23h59m"},
		{25 * time.Hour, "1d"},
		// Floored, like the status line's countdown: a remaining time never
		// promises more than is left.
		{95 * time.Hour, "3d"},
	} {
		if got := humanLeft(tc.in); got != tc.want {
			t.Errorf("humanLeft(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWindowLabel(t *testing.T) {
	for _, tc := range []struct{ kind, scope, want string }{
		{"5h", "", "5h"},
		{"7d", "", "week"},
		{"something-new", "", "something-new"},
		// A model-scoped window must say which model, or it reads as a second
		// allowance of the same kind.
		{"7d", "Opus", "week (Opus)"},
		{"5h", "Fable", "5h (Fable)"},
	} {
		if got := windowLabel(tc.kind, tc.scope); got != tc.want {
			t.Errorf("windowLabel(%q, %q) = %q, want %q", tc.kind, tc.scope, got, tc.want)
		}
	}
}
