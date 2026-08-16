package studioapi

import (
	"path/filepath"
	"testing"
	"time"
)

// A gap is not an outage.
//
// The commonest reason a span has no samples is a laptop that was closed, and a
// bucket that reported "down" for that would draw an incident every night. So a
// bucket carries two counts and the absence of both is a third state the client
// paints as absence.
func TestASpanWithNoSamplesIsNeitherUpNorDown(t *testing.T) {
	now := time.Now()
	l := loadProbeLog(filepath.Join(t.TempDir(), "probes.json"))
	// Two readings an hour ago, nothing since.
	l.record("claude", probeSample{At: now.Add(-time.Hour), Up: true})
	l.record("claude", probeSample{At: now.Add(-time.Hour).Add(time.Minute), Up: false, Reason: "provider answered 503"})

	buckets := l.buckets("claude", 24*time.Hour, 48, now)
	if len(buckets) != 48 {
		t.Fatalf("got %d buckets, want 48 — every slot in the window is present", len(buckets))
	}

	var withData, empty int
	for _, b := range buckets {
		if b.Up+b.Down == 0 {
			empty++
			continue
		}
		withData++
	}
	if withData == 0 {
		t.Fatal("the recorded samples landed in no bucket")
	}
	if empty == 0 {
		t.Error("no empty buckets — a window nothing was recorded in must stay empty rather than being filled in")
	}
	// And the failure keeps its reason, because "timed out" and "answered 503"
	// are different incidents.
	var sawReason bool
	for _, b := range buckets {
		if b.Down > 0 && b.Reason == "provider answered 503" {
			sawReason = true
		}
	}
	if !sawReason {
		t.Error("the reason for a failed probe was not kept")
	}
}

// The ring is bounded, and it drops the oldest rather than refusing the newest:
// a daemon left running for a month must not grow a file without limit, and the
// recent history is the part anybody looks at.
func TestTheProbeLogIsBounded(t *testing.T) {
	now := time.Now()
	l := loadProbeLog(filepath.Join(t.TempDir(), "probes.json"))
	for i := range maxProbeSamples + 50 {
		l.record("claude", probeSample{At: now.Add(time.Duration(i) * time.Second), Up: true})
	}
	if got := len(l.Samples["claude"]); got > maxProbeSamples {
		t.Errorf("kept %d samples, want at most %d", got, maxProbeSamples)
	}
	// Anything older than the retention window is gone too, whatever the count.
	l.record("claude", probeSample{At: now.Add(probeRetention + time.Hour), Up: true})
	for _, s := range l.Samples["claude"] {
		if s.At.Before(now.Add(probeRetention + time.Hour).Add(-probeRetention)) {
			t.Errorf("sample from %s survived the retention window", s.At)
			break
		}
	}
}

// History survives a restart — the whole point of persisting it is to answer
// "was it down while I was away", and a daemon restart is exactly when somebody
// asks.
func TestProbeHistorySurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probes.json")
	first := loadProbeLog(path)
	first.record("codex", probeSample{At: time.Now(), Up: false, Reason: "timed out"})

	again := loadProbeLog(path)
	if got := len(again.Samples["codex"]); got != 1 {
		t.Fatalf("reloaded %d samples, want 1", got)
	}
	if again.Samples["codex"][0].Up {
		t.Error("a failed probe came back as a successful one")
	}
}

// A Server built as a struct literal has no probe log, which every test in this
// package does — and a chart is never worth a panic in a handler.
func TestTheProbeLogToleratesNotExisting(t *testing.T) {
	var l *probeLog
	l.record("claude", probeSample{At: time.Now(), Up: true})
	if got := l.buckets("claude", time.Hour, 4, time.Now()); got != nil {
		t.Errorf("buckets on a nil log = %v, want nil", got)
	}
}
