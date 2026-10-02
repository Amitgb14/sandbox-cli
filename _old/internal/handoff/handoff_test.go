package handoff

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The three files exist, and the brief says what it is.
//
// The label is not decoration: a target told it is *resuming* answers as though
// it remembers decisions it never made, with file-writing tools. Every path out
// of this package has to say "briefing".
func TestWriteProducesABriefingAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	ws := gitRepo(t)
	writeFile(t, ws, "a.go", "package a\n")

	session := writeTranscript(t, []map[string]any{
		{"type": "user", "message": map[string]any{"content": "add pagination to /orders"}},
		{"type": "assistant", "message": map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "Reading the handler first.\nThen the query."},
		}}},
	})

	ex, err := Write(dir, "claude", session, ws, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"HANDOFF.md", "transcript.jsonl", "files.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}

	brief := read(t, filepath.Join(dir, "HANDOFF.md"))
	if !strings.Contains(brief, "briefing, not a resume") {
		t.Error("the brief does not say it is a briefing; a target that thinks it is resuming will answer from a memory it does not have")
	}
	if !strings.Contains(brief, "add pagination to /orders") {
		t.Error("the user's prompt is not quoted verbatim; paraphrasing loses the only unambiguous thing in a transcript")
	}
	// Assistant turns are reduced to their first line: the body is reasoning the
	// target cannot verify and must not inherit as fact.
	if !strings.Contains(brief, "Reading the handler first.") {
		t.Error("the assistant's heading is missing from the brief")
	}
	if strings.Contains(brief, "Then the query.") {
		t.Error("the assistant's full body was copied into the brief; only the heading or conclusion should cross")
	}

	if ex.Turns != 1 {
		t.Errorf("turns = %d, want 1 — a user turn is a prompt somebody typed", ex.Turns)
	}
	if ex.Files != 1 {
		t.Errorf("files = %d, want the one untracked file", ex.Files)
	}
}

// The prompt handed to the fallback must name the mount, the shape of what is
// there, and the limit — and must still carry the original task.
func TestPromptSeedsWithoutClaimingAResume(t *testing.T) {
	ex := &Export{Dir: "/tmp/x", From: "claude", Turns: 12, Files: 3}
	got := ex.Prompt("fix the flaky test")

	for _, want := range []string{GuestDir, "claude", "briefing, not a resumed conversation", "fix the flaky test"} {
		if !strings.Contains(got, want) {
			t.Errorf("seed prompt does not mention %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "fix the flaky test") {
		t.Error("the original task is not last; the briefing is context and the task is the instruction, and burying the instruction is how it gets ignored")
	}
}

// A failed agent that wrote nothing is the case routing fires on most, so an
// absent transcript must produce an export rather than an error.
func TestWriteToleratesAMissingTranscript(t *testing.T) {
	dir := t.TempDir()
	ws := gitRepo(t)

	ex, err := Write(dir, "claude", filepath.Join(ws, "nope.jsonl"), ws, "")
	if err != nil {
		t.Fatalf("a missing transcript failed the export: %v — the agent dying before it wrote one is exactly when this runs", err)
	}
	if ex.Turns != 0 {
		t.Errorf("turns = %d, want 0", ex.Turns)
	}
	brief := read(t, filepath.Join(dir, "HANDOFF.md"))
	if !strings.Contains(brief, "stopped before writing a transcript") {
		t.Error("the brief does not say the transcript was absent; silence would read as 'nothing was asked'")
	}
	ledger := read(t, filepath.Join(dir, "files.md"))
	if !strings.Contains(ledger, "None") {
		t.Errorf("an unchanged workspace should say so plainly:\n%s", ledger)
	}
}

// The normalized transcript carries the three fields that mean the same thing to
// every agent, and nothing vendor-specific. Tool names and ids are the part that
// does not translate — a target reading them would be reading about tools it
// does not have.
func TestTranscriptIsNormalizedAndVendorNeutral(t *testing.T) {
	dir := t.TempDir()
	ws := gitRepo(t)
	session := writeTranscript(t, []map[string]any{
		{"type": "user", "message": map[string]any{"content": "go"}},
		{"type": "assistant", "message": map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "done"},
			map[string]any{"type": "tool_use", "name": "Edit", "id": "toolu_01ABC"},
		}}},
	})

	if _, err := Write(dir, "claude", session, ws, ""); err != nil {
		t.Fatal(err)
	}
	body := read(t, filepath.Join(dir, "transcript.jsonl"))
	if strings.Contains(body, "toolu_01ABC") || strings.Contains(body, "tool_use") {
		t.Errorf("vendor tool details crossed into the neutral transcript:\n%s", body)
	}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("a line of the normalized transcript is not JSON: %v", err)
		}
		for k := range m {
			switch k {
			case "role", "text", "at":
			default:
				t.Errorf("unexpected field %q in the neutral transcript", k)
			}
		}
	}
}

// --- helpers -------------------------------------------------------------

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v\n%s", err, out)
		}
	}
	return dir
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTranscript(t *testing.T, lines []map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	var b strings.Builder
	for _, l := range lines {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
