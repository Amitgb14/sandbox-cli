package agentusage

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestParseReadsBothWindows(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Agent != "claude" {
		t.Errorf("Agent = %q, want claude", s.Agent)
	}
	// The two account windows, then the one model-scoped cap. The unscoped
	// limits[] entries restate the first two and must not appear twice.
	if len(s.Windows) != 3 {
		t.Fatalf("got %d windows, want 3: %+v", len(s.Windows), s.Windows)
	}
	if got := s.Windows[0]; got.Kind != KindFiveHour || got.Percent != 23 || got.Scope != "" {
		t.Errorf("window[0] = %+v, want unscoped 5h at 23%%", got)
	}
	if got := s.Windows[1]; got.Kind != KindSevenDay || got.Percent != 49.4 || got.Scope != "" {
		t.Errorf("window[1] = %+v, want unscoped 7d at 49.4%%", got)
	}
	if got := s.Windows[2]; got.Kind != KindSevenDay || got.Percent != 25 || got.Scope != "Opus" {
		t.Errorf("window[2] = %+v, want 7d at 25%% scoped to Opus", got)
	}
	// The offset spelling the cache actually uses ("+00:00") must parse.
	want := time.Date(2026, 7, 25, 10, 9, 59, 429244000, time.UTC)
	if !s.Windows[0].ResetsAt.Equal(want) {
		t.Errorf("ResetsAt = %v, want %v", s.Windows[0].ResetsAt, want)
	}
	if got, want := s.FetchedAt.UnixMilli(), int64(1784957403517); got != want {
		t.Errorf("FetchedAt = %d ms, want %d", got, want)
	}
}

// The whole point of the package is that unknown shapes report nothing rather
// than a plausible zero, so the empty cases are the load-bearing ones.
func TestParseWithoutUsageIsEmptyNotAnError(t *testing.T) {
	for name, in := range map[string]string{
		"empty object":     `{}`,
		"no utilization":   `{"cachedUsageUtilization":{"fetchedAtMs":1}}`,
		"null windows":     `{"cachedUsageUtilization":{"utilization":{"five_hour":null,"seven_day":null}}}`,
		"windows w/o pct":  `{"cachedUsageUtilization":{"utilization":{"five_hour":{"resets_at":"2026-07-25T10:09:59Z"}}}}`,
		"unrelated config": `{"numStartups":3,"projects":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Parse([]byte(in))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !s.Empty() {
				t.Errorf("got %+v, want no windows", s.Windows)
			}
		})
	}
}

func TestParseScopedLimits(t *testing.T) {
	const in = `{"cachedUsageUtilization":{"utilization":{
	  "five_hour":{"utilization":10,"resets_at":"2026-07-25T10:00:00Z"},
	  "limits":[
	    {"kind":"session","percent":10,"scope":null},
	    {"kind":"weekly_all","percent":40,"scope":null},
	    {"kind":"weekly_scoped","percent":25,"resets_at":"2026-07-26T06:00:00Z","scope":{"model":{"display_name":"Opus"}}},
	    {"kind":"session_scoped","percent":60,"scope":{"model":{"display_name":"Haiku"}}},
	    {"kind":"monthly_scoped","percent":5,"scope":{"model":{"display_name":"Ghost"}}},
	    {"kind":"weekly_scoped","scope":{"model":{"display_name":"NoPercent"}}},
	    {"kind":"weekly_scoped","percent":8,"scope":{"model":null}}
	  ]}}}`
	s, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var got []string
	for _, w := range s.Windows {
		got = append(got, w.Kind+"/"+w.Scope)
	}
	// The account window, the scoped weekly, and the scoped session. Dropped:
	// both unscoped limits[] entries (they restate the account windows), a
	// period this package cannot place, an entry with no percentage, and one
	// whose scope names no model.
	want := []string{"5h/", "7d/Opus", "5h/Haiku"}
	if len(got) != len(want) {
		t.Fatalf("windows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("windows = %v, want %v", got, want)
			break
		}
	}
}

func TestParseRejectsNonJSON(t *testing.T) {
	if _, err := Parse([]byte("not json")); err == nil {
		t.Fatal("want an error for bytes that are not JSON")
	}
}

func TestParseAcceptsEpochAndUnparseableResetTimes(t *testing.T) {
	cases := map[string]struct {
		resetsAt string
		want     time.Time
	}{
		"epoch seconds":      {`1784957400`, time.Unix(1784957400, 0).UTC()},
		"epoch milliseconds": {`1784957400000`, time.UnixMilli(1784957400000).UTC()},
		"rfc3339 Z":          {`"2026-07-25T10:09:59Z"`, time.Date(2026, 7, 25, 10, 9, 59, 0, time.UTC)},
		// A stamp we cannot read must not take the percentage down with it.
		"gibberish": {`"soon"`, time.Time{}},
		"null":      {`null`, time.Time{}},
		"zero":      {`0`, time.Time{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := `{"cachedUsageUtilization":{"utilization":{"five_hour":{"utilization":7,"resets_at":` + tc.resetsAt + `}}}}`
			s, err := Parse([]byte(in))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(s.Windows) != 1 || s.Windows[0].Percent != 7 {
				t.Fatalf("got %+v, want one window at 7%%", s.Windows)
			}
			if !s.Windows[0].ResetsAt.Equal(tc.want) {
				t.Errorf("ResetsAt = %v, want %v", s.Windows[0].ResetsAt, tc.want)
			}
		})
	}
}

func TestReadMissingFileIsEmptyNotAnError(t *testing.T) {
	s, err := Read(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !s.Empty() {
		t.Errorf("got %+v, want empty", s)
	}
}

func TestReadRecordsThePath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude.json")
	writeCache(t, p, 1000, 5)
	s, err := Read(p)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if s.Path != p {
		t.Errorf("Path = %q, want %q", s.Path, p)
	}
}

// Both homes describe the same account, so the newer reading wins regardless of
// which path it came from.
func TestFindPrefersTheNewestReading(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "older.json")
	newer := filepath.Join(dir, "newer.json")
	writeCache(t, older, 1_000_000, 11)
	writeCache(t, newer, 2_000_000, 77)

	for _, order := range [][]string{{older, newer}, {newer, older}} {
		s, err := Find(order...)
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if s.Path != newer || s.Windows[0].Percent != 77 {
			t.Errorf("Find(%v) = %s at %v%%, want the newer file", order, s.Path, s.Windows[0].Percent)
		}
	}
}

func TestFindWithNothingToReadIsEmptyNotAnError(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"numStartups":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Find(filepath.Join(dir, "absent.json"), empty, "")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if !s.Empty() {
		t.Errorf("got %+v, want empty", s)
	}
}

func TestAgeIsNeverNegative(t *testing.T) {
	now := time.Now()
	if got := (Snapshot{}).Age(now); got != 0 {
		t.Errorf("unknown FetchedAt: Age = %v, want 0", got)
	}
	future := Snapshot{FetchedAt: now.Add(time.Hour)}
	if got := future.Age(now); got != 0 {
		t.Errorf("clock skew: Age = %v, want 0", got)
	}
	past := Snapshot{FetchedAt: now.Add(-90 * time.Second)}
	if got := past.Age(now); got != 90*time.Second {
		t.Errorf("Age = %v, want 1m30s", got)
	}
}

func writeCache(t *testing.T, path string, fetchedAtMs int64, percent int) {
	t.Helper()
	body := `{"cachedUsageUtilization":{"fetchedAtMs":` + strconv.FormatInt(fetchedAtMs, 10) +
		`,"utilization":{"five_hour":{"utilization":` + strconv.Itoa(percent) +
		`,"resets_at":"2026-07-25T10:09:59Z"}}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
