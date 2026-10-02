package agentctx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Which transcript belongs to a run. Moved here with PickSession itself: two
// callers now depend on these rules — the daemon showing a conversation and the
// CLI exporting one into a briefing — and the tests belong with the function
// rather than with whichever caller happened to be written first.

func TestPickSessionIgnoresAnOldSessionThatIsStillBeingWritten(t *testing.T) {
	now := time.Now()
	runStart := now.Add(-3 * time.Minute)

	sessions := []Session{
		// Newest by mtime — the host's own live session, started two days ago and
		// appended to seconds ago. List returns it first.
		{Path: "/host/live.jsonl", Started: now.Add(-48 * time.Hour), Modified: now},
		// The run's own transcript: started with the container.
		{Path: "/sandbox/run.jsonl", Started: runStart.Add(2 * time.Second), Modified: now.Add(-time.Minute)},
	}

	got, ok := PickSession(sessions, runStart.Add(-2*time.Minute), now.Add(2*time.Minute), "")
	if !ok {
		t.Fatal("expected the run's own transcript to be found")
	}
	if got != "/sandbox/run.jsonl" {
		t.Errorf("picked %q, want the session that started with the run", got)
	}
}

// Nothing in the window means nothing reported. The reply box hangs off this
// answer, so a near-miss must not be promoted to a match.

func TestPickSessionReportsNothingRatherThanTheClosest(t *testing.T) {
	now := time.Now()
	sessions := []Session{
		{Path: "/a.jsonl", Started: now.Add(-72 * time.Hour), Modified: now},
		{Path: "/b.jsonl", Started: now.Add(-71 * time.Hour), Modified: now},
	}
	if got, ok := PickSession(sessions, now.Add(-5*time.Minute), now, ""); ok {
		t.Errorf("picked %q, want no match", got)
	}
}

// A session whose first line could not be read has no start time. It cannot
// pass a filter it has no value for.

func TestPickSessionSkipsASessionWithNoStartTime(t *testing.T) {
	now := time.Now()
	sessions := []Session{{Path: "/unknown.jsonl", Modified: now}}
	if _, ok := PickSession(sessions, now.Add(-5*time.Minute), now, ""); ok {
		t.Error("a session with no recorded start must not match")
	}
}

// Only the sandbox-owned store can hold a container's transcript: that
// directory is the container's whole HOME. The claude wrapper really does have
// two verified stores, and the other one is the user's own history.

func TestPickSessionSeparatesConcurrentRunsByPrompt(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()

	reviewer := writePromptTranscript(t, dir, "reviewer", "review the code but do not write code")
	probe := writePromptTranscript(t, dir, "probe", "Say READY and wait.")

	// Newest first, as List returns them: the probe wrote last.
	sessions := []Session{
		{Path: probe, Started: now.Add(-3 * time.Minute), Modified: now},
		{Path: reviewer, Started: now.Add(-8 * time.Minute), Modified: now.Add(-time.Minute)},
	}
	from, until := now.Add(-9*time.Minute), now

	got, ok := PickSession(sessions, from, until, "review the code but do not write code")
	if !ok {
		t.Fatal("expected the reviewer's own transcript")
	}
	if got != reviewer {
		t.Errorf("picked %q, want the session whose first prompt matches the run", got)
	}
}

// Ambiguity reports nothing. A conversation under the wrong run carries a reply
// box wired to a real agent's stdin.

func TestPickSessionRefusesWhenTheWindowHoldsSeveralAndNothingMatches(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	a := writePromptTranscript(t, dir, "a", "do one thing")
	b := writePromptTranscript(t, dir, "b", "do another thing")
	sessions := []Session{
		{Path: a, Started: now.Add(-2 * time.Minute), Modified: now},
		{Path: b, Started: now.Add(-3 * time.Minute), Modified: now},
	}
	for _, prompt := range []string{"", "something nobody asked"} {
		if got, ok := PickSession(sessions, now.Add(-5*time.Minute), now, prompt); ok {
			t.Errorf("prompt %q: picked %q, want no match", prompt, got)
		}
	}
}

// writeTranscript writes a minimal claude-jsonl file whose first user turn is
// prompt, which is all the correlation reads.
// writePromptTranscript writes a one-turn session whose first user turn is
// `prompt`, which is what the tie-break reads.
func writePromptTranscript(t *testing.T, dir, name, prompt string) string {
	t.Helper()
	path := filepath.Join(dir, name+".jsonl")
	line := map[string]any{
		"type":      "user",
		"sessionId": name,
		"timestamp": time.Now().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": prompt},
	}
	b, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
