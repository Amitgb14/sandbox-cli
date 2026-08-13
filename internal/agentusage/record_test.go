package agentusage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const recordJSON = `{"recorded_at":1786742400,"agent":"claude","rate_limits":
{"five_hour":{"used_percentage":23,"resets_at":1786757400},
 "seven_day":{"used_percentage":49.5,"resets_at":1787016000}}}`

func TestReadStatusLineRecord(t *testing.T) {
	p := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(p, []byte(recordJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Read(p)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if s.Source != SourceStatusLine {
		t.Fatalf("source = %q, want %q", s.Source, SourceStatusLine)
	}
	if got, want := s.FetchedAt.Unix(), int64(1786742400); got != want {
		t.Fatalf("fetchedAt = %d, want %d", got, want)
	}
	if len(s.Windows) != 2 {
		t.Fatalf("windows = %d, want 2", len(s.Windows))
	}
	if s.Windows[0].Kind != KindFiveHour || s.Windows[0].Percent != 23 {
		t.Fatalf("five-hour window = %+v", s.Windows[0])
	}
	// Fractional percentages survive: the hook reports them, and rounding is a
	// rendering decision rather than a parsing one.
	if s.Windows[1].Percent != 49.5 {
		t.Fatalf("seven-day percent = %v, want 49.5", s.Windows[1].Percent)
	}
	if s.Windows[1].ResetsAt.Unix() != 1787016000 {
		t.Fatalf("seven-day reset = %v", s.Windows[1].ResetsAt)
	}
	// The hook carries no is_active flag, so nothing here may assert one.
	if s.Windows[0].Active != nil {
		t.Fatal("a window claimed to know whether it was in force; the hook does not say")
	}
}

// A window the hook did not report is absent, not zero. 0% is a real reading.
func TestRecordOmitsUnreportedWindows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "usage.json")
	os.WriteFile(p, []byte(`{"recorded_at":1786742400,"rate_limits":{"five_hour":{"used_percentage":0}}}`), 0o600)
	s, err := Read(p)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(s.Windows) != 1 {
		t.Fatalf("windows = %d, want only the one that was reported", len(s.Windows))
	}
	if s.Windows[0].Percent != 0 {
		t.Fatal("a reported 0% was dropped; absent and zero are different answers")
	}
}

// The whole point of the phase: a live recording outranks a cache Claude Code
// has stopped maintaining, and Find says which one it used.
func TestFindPrefersTheLiveRecording(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "usage.json")
	cache := filepath.Join(dir, ".claude.json")

	os.WriteFile(rec, []byte(recordJSON), 0o600)
	// A cache stamped three weeks before the recording — the real shape of this
	// machine: written recently, reading long dead.
	old := (time.Unix(1786742400, 0).Add(-21 * 24 * time.Hour).UnixMilli())
	os.WriteFile(cache, []byte(`{"cachedUsageUtilization":{"fetchedAtMs":`+itoa(old)+
		`,"utilization":{"five_hour":{"utilization":90}}}}`), 0o600)

	s, err := Find(rec, cache)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if s.Source != SourceStatusLine {
		t.Fatalf("source = %q, want the recording to win", s.Source)
	}
	if s.Windows[0].Percent != 23 {
		t.Fatalf("took the stale cache's figure (%v)", s.Windows[0].Percent)
	}
	// And it is not reported as abandoned: the file and its reading are the same
	// moment, which is what a live source looks like.
	if s.Abandoned() {
		t.Fatal("a fresh recording was called abandoned")
	}
}

// A cache with no recording beside it still works, unchanged.
func TestFindFallsBackToTheCache(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, ".claude.json")
	os.WriteFile(cache, []byte(`{"cachedUsageUtilization":{"fetchedAtMs":1786742400000,
		"utilization":{"five_hour":{"utilization":90}}}}`), 0o600)

	s, err := Find(filepath.Join(dir, "usage.json"), cache)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if s.Source != SourceCache {
		t.Fatalf("source = %q, want %q", s.Source, SourceCache)
	}
	if s.Windows[0].Percent != 90 {
		t.Fatalf("percent = %v, want the cache's 90", s.Windows[0].Percent)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
