// Package agentusage reads how much of an agent's subscription window has been
// spent and when it resets.
//
// There is no supported way to ask for that non-interactively — no agent
// subcommand, no endpoint, and the transcripts record per-message token counts
// but neither a limit nor a reset time. What does exist is the cache Claude Code
// writes for its own `/usage` display: `cachedUsageUtilization` in
// ~/.claude.json. This package reads that, and nothing else.
//
// Two rules follow from reading a file another program owns, and they are the
// whole design:
//
//   - Read only. Nothing here opens that file for writing, ever. It is Claude
//     Code's file; we are a spectator.
//   - Cached, so always aged. Every Snapshot carries the FetchedAt stamp that
//     came with it, because the one genuinely misleading way to show these
//     numbers is without saying how old they are. A shape this package no longer
//     recognizes yields *no windows* rather than a zero or a guess — the same
//     bargain internal/agentctx makes with transcripts.
//
// Inside the sandbox the status line does not use any of this: Claude pipes it a
// documented rate_limits object on stdin. This package is for the host side,
// where there is no such pipe.
package agentusage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// Window kinds — the two periods usage is metered over. A plan may report each
// of them twice: once for the account as a whole, and once scoped to a single
// model (see Window.Scope).
const (
	KindFiveHour = "5h"
	KindSevenDay = "7d"
)

// Window is one usage window: how much of it is spent, and when it starts over.
type Window struct {
	Kind    string  `json:"kind"`
	Percent float64 `json:"percent"`
	// ResetsAt is zero when the source did not report one. A percentage without
	// a reset time is still true, so it is reported rather than dropped.
	ResetsAt time.Time `json:"resets_at,omitempty"`
	// Scope names the model this window applies to, for plans that cap a single
	// model separately (e.g. a weekly Opus allowance alongside the weekly
	// account one). Empty means the window covers the whole account.
	Scope string `json:"scope,omitempty"`
}

// Snapshot is one reading of one agent's usage cache.
type Snapshot struct {
	Agent   string   `json:"agent"`
	Windows []Window `json:"windows"`
	// FetchedAt is when the agent last refreshed these numbers from the server,
	// not when we read the file. Zero if it was not recorded.
	FetchedAt time.Time `json:"fetched_at,omitempty"`
	Path      string    `json:"path,omitempty"`
}

// Empty reports whether the snapshot carries no usable window — either because
// nothing was found or because the file no longer says what we know how to read.
func (s Snapshot) Empty() bool { return len(s.Windows) == 0 }

// Age is how long ago the agent refreshed these numbers, or 0 when unknown.
func (s Snapshot) Age(now time.Time) time.Duration {
	if s.FetchedAt.IsZero() {
		return 0
	}
	if d := now.Sub(s.FetchedAt); d > 0 {
		return d
	}
	return 0
}

// claudeConfig is the sliver of ~/.claude.json this package understands. Every
// other key in that file — and there are many — is dropped by encoding/json.
type claudeConfig struct {
	Cached struct {
		FetchedAtMs int64 `json:"fetchedAtMs"`
		Utilization struct {
			FiveHour *window `json:"five_hour"`
			SevenDay *window `json:"seven_day"`
			Limits   []limit `json:"limits"`
		} `json:"utilization"`
	} `json:"cachedUsageUtilization"`
}

type window struct {
	Utilization *float64  `json:"utilization"`
	ResetsAt    timestamp `json:"resets_at"`
}

// limit is one entry of the normalized limits[] array. Only the model-scoped
// entries are read from it: the unscoped ones restate five_hour and seven_day,
// which are the fields this package already treats as authoritative.
type limit struct {
	Kind     string    `json:"kind"`
	Percent  *float64  `json:"percent"`
	ResetsAt timestamp `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

// model returns the display name this limit is scoped to, or "" when it applies
// to the whole account.
func (l limit) model() string {
	if l.Scope == nil || l.Scope.Model == nil {
		return ""
	}
	return l.Scope.Model.DisplayName
}

// timestamp accepts the two spellings a reset time arrives in: an RFC3339 string
// (what the on-disk cache holds) and a bare epoch number (what Claude's own
// status-line JSON uses). Accepting both costs a dozen lines and means one
// source changing to the other's spelling is not a silent loss of the reset time.
type timestamp struct{ time.Time }

func (t *timestamp) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if s == "" {
			return nil
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if v, err := time.Parse(layout, s); err == nil {
				t.Time = v
				return nil
			}
		}
		// An unparseable stamp is a missing stamp, not a broken file: the
		// percentage next to it is still worth showing.
		return nil
	}
	var n float64
	if err := json.Unmarshal(b, &n); err != nil || n <= 0 {
		return nil
	}
	// Seconds or milliseconds — anything past ~2001 in milliseconds is far
	// beyond any plausible seconds-since-epoch reset time.
	if n >= 1e12 {
		t.Time = time.UnixMilli(int64(n)).UTC()
	} else {
		t.Time = time.Unix(int64(n), 0).UTC()
	}
	return nil
}

// Parse reads a snapshot out of the bytes of a ~/.claude.json. It is pure: no
// filesystem, no clock. An error means the bytes were not JSON at all; a file
// that parses but carries no usage cache yields an empty Snapshot and no error,
// because "this agent has not recorded usage" is an ordinary answer and not a
// failure the caller should report as one.
func Parse(data []byte) (Snapshot, error) {
	var c claudeConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return Snapshot{}, fmt.Errorf("parse usage cache: %w", err)
	}
	s := Snapshot{Agent: "claude"}
	if ms := c.Cached.FetchedAtMs; ms > 0 {
		s.FetchedAt = time.UnixMilli(ms).UTC()
	}
	// The two whole-account windows first: these are the fields that exist on
	// every plan, and they are what the status line reports too.
	for _, w := range []struct {
		kind string
		src  *window
	}{
		{KindFiveHour, c.Cached.Utilization.FiveHour},
		{KindSevenDay, c.Cached.Utilization.SevenDay},
	} {
		if w.src == nil || w.src.Utilization == nil {
			continue
		}
		s.Windows = append(s.Windows, Window{
			Kind:     w.kind,
			Percent:  *w.src.Utilization,
			ResetsAt: w.src.ResetsAt.Time,
		})
	}
	// Then the model-scoped caps, for plans that meter one model separately.
	// Only scoped entries are taken: the unscoped ones restate the two windows
	// above, and a listing that shows the same allowance twice under two names
	// reads as two allowances.
	for _, l := range c.Cached.Utilization.Limits {
		name := l.model()
		if name == "" || l.Percent == nil {
			continue
		}
		kind := limitKind(l.Kind)
		if kind == "" {
			// A grouping this package has not seen. Reporting it under a guessed
			// window would misstate how long it lasts.
			continue
		}
		s.Windows = append(s.Windows, Window{
			Kind:     kind,
			Percent:  *l.Percent,
			ResetsAt: l.ResetsAt.Time,
			Scope:    name,
		})
	}
	return s, nil
}

// limitKind maps a limits[] entry's kind onto the window it belongs to, or ""
// when it names a period this package cannot place. The kinds seen so far are
// "session", "weekly_all" and "weekly_scoped"; the prefixes are matched rather
// than the whole string so a new scoped variant of a known period still lands in
// the right window.
func limitKind(kind string) string {
	switch {
	case strings.HasPrefix(kind, "session"):
		return KindFiveHour
	case strings.HasPrefix(kind, "weekly"):
		return KindSevenDay
	default:
		return ""
	}
}

// Read parses the usage cache at path. A file that is not there yields an empty
// Snapshot and no error.
func Read(path string) (Snapshot, error) {
	if path == "" {
		return Snapshot{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Snapshot{}, nil
		}
		return Snapshot{}, err
	}
	s, err := Parse(data)
	if err != nil {
		return Snapshot{}, err
	}
	s.Path = path
	return s, nil
}

// ClaudePaths lists the ~/.claude.json files a host may have, newest-wins order
// resolved by Find rather than by this order: the sandbox-owned HOME the claude
// wrapper persists, and the user's real home for sessions run outside the
// sandbox. Both describe the same account and therefore the same server-side
// quota, so whichever was refreshed last is the better answer. Paths that cannot
// be resolved are omitted.
func ClaudePaths() []string {
	var out []string
	if dir := config.AgentStateDir("claude"); dir != "" {
		out = append(out, filepath.Join(dir, ".claude.json"))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".claude.json"))
	}
	return out
}

// Find reads every candidate path and returns the snapshot whose numbers were
// refreshed most recently. Missing files are skipped; if none of them holds a
// usage cache the result is an empty Snapshot, and an error is returned only
// when a file existed and could not be read at all.
func Find(paths ...string) (Snapshot, error) {
	var best Snapshot
	var firstErr error
	for _, p := range paths {
		s, err := Read(p)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if s.Empty() {
			continue
		}
		if best.Empty() || s.FetchedAt.After(best.FetchedAt) {
			best = s
		}
	}
	if best.Empty() && firstErr != nil {
		return Snapshot{}, firstErr
	}
	return best, nil
}
