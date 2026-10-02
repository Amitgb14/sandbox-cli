package handoff

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
)

// The three files exist, and the brief says what it is.
//
// The label is not decoration: a target told it is *resuming* answers as though
// it remembers decisions it never made, with file-writing tools. Every path out
// of this package has to say "briefing".
func TestBuildProducesABriefingAndSaysSo(t *testing.T) {
	msgs := transcript(t, []map[string]any{
		{"type": "user", "message": map[string]any{"content": "add pagination to /orders"}},
		{"type": "assistant", "message": map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "Reading the handler first.\nThen the query."},
		}}},
	})
	ex := Build("claude", msgs, []FileStat{{Path: "a.go", Status: "added", Insertions: 1}})

	for _, name := range []string{"HANDOFF.md", "transcript.jsonl", "files.md"} {
		if _, ok := ex.Files[name]; !ok {
			t.Errorf("%s was not built", name)
		}
	}
	brief := string(ex.Files["HANDOFF.md"])
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
	if ex.Changed != 1 || !strings.Contains(string(ex.Files["files.md"]), "`a.go` — added, +1/-0") {
		t.Errorf("ledger:\n%s", ex.Files["files.md"])
	}
}

// The prompt handed to the fallback must name where the briefing is, the shape
// of what is there, and the limit — and must still carry the original task.
func TestPromptSeedsWithoutClaimingAResume(t *testing.T) {
	ex := &Export{From: "claude", Turns: 12, Changed: 3}
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

// A failed agent that wrote nothing is the case routing fires on most, so no
// transcript must still produce an export.
func TestBuildToleratesNoTranscript(t *testing.T) {
	ex := Build("claude", nil, nil)
	if ex.Turns != 0 {
		t.Errorf("turns = %d, want 0", ex.Turns)
	}
	if !strings.Contains(string(ex.Files["HANDOFF.md"]), "stopped before writing a transcript") {
		t.Error("the brief does not say the transcript was absent; silence would read as 'nothing was asked'")
	}
	if !strings.Contains(string(ex.Files["files.md"]), "None") {
		t.Errorf("an unchanged workspace should say so plainly:\n%s", ex.Files["files.md"])
	}
}

// The normalized transcript carries the three fields that mean the same thing to
// every agent, and nothing vendor-specific. Tool names and ids are the part that
// does not translate — a target reading them would be reading about tools it
// does not have.
func TestTranscriptIsNormalizedAndVendorNeutral(t *testing.T) {
	msgs := transcript(t, []map[string]any{
		{"type": "user", "message": map[string]any{"content": "go"}},
		{"type": "assistant", "message": map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "done"},
			map[string]any{"type": "tool_use", "name": "Edit", "id": "toolu_01ABC"},
		}}},
	})
	body := string(Build("claude", msgs, nil).Files["transcript.jsonl"])
	if strings.Contains(body, "toolu_01ABC") || strings.Contains(body, "tool_use") {
		t.Errorf("vendor tool details crossed into the neutral transcript:\n%s", body)
	}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
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

// A long session is cut to its last turns, not its first: the end is where the
// agent stopped.
func TestBuildKeepsTheLastTurns(t *testing.T) {
	var msgs []agentctx.Message
	for i := range maxBriefTurns + 10 {
		msgs = append(msgs, agentctx.Message{Role: "user", Text: strings.Repeat("x", i+1)})
	}
	ex := Build("claude", msgs, nil)
	if ex.Turns != maxBriefTurns || !strings.Contains(string(ex.Files["HANDOFF.md"]), strings.Repeat("x", maxBriefTurns+10)) {
		t.Errorf("turns %d; the last prompt should survive", ex.Turns)
	}
}

func transcript(t *testing.T, lines []map[string]any) []agentctx.Message {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteString("\n")
	}
	msgs, err := agentctx.ParseTranscript(strings.NewReader(b.String()), 0)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}
