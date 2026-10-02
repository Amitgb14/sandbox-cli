package cli

import (
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentusage"
)

// The bug this guards: a cache written 29 minutes before a window reset, read
// 16 hours later, printed "week (Fable) 25%" when the true figure was 0. The
// percentage was the previous week's final reading. Once a window has rolled
// over the only honest cell is one that shows no number.
func TestPercentCell(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		w    agentusage.Window
		want string
	}{
		{"still running", agentusage.Window{Percent: 49, ResetsAt: now.Add(time.Hour)}, "49%"},
		{"rolled over", agentusage.Window{Percent: 25, ResetsAt: now.Add(-time.Minute)}, "—"},
		{"rolled over at exactly now", agentusage.Window{Percent: 25, ResetsAt: now}, "—"},
		// No reset time to place it either side of: the percentage is all there
		// is, and it is still true of some window.
		{"no reset reported", agentusage.Window{Percent: 8}, "8%"},
		{"zero is a number, not a gap", agentusage.Window{Percent: 0, ResetsAt: now.Add(time.Hour)}, "0%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentCell(tc.w, now); got != tc.want {
				t.Errorf("percentCell = %q, want %q", got, tc.want)
			}
		})
	}
}

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
		// Past its reset: the window has started over, which is what to say —
		// not a negative countdown.
		{"already rolled over", now.Add(-time.Minute), "rolled over", "(11:59)"},
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
