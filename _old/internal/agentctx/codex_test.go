package agentctx

import (
	"path/filepath"
	"strings"
	"testing"
)

// A rollout in the shape codex actually writes, trimmed to the lines this reader
// looks at. Every impostor below appears in a real session and was the reason
// the reader needed writing: three `developer` messages carrying codex's own
// instructions, and an injected `<environment_context>` user turn that arrives
// before anything a person typed.
const codexRollout = `{"timestamp":"2026-08-18T22:44:15.557Z","type":"session_meta","payload":{"session_id":"01a0170b-f760-7b20-a50c-654198f0a759","timestamp":"2026-08-18T22:44:15.409Z","cwd":"/workspace","originator":"codex_exec"}}
{"timestamp":"2026-08-18T22:44:16.000Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<skills_instructions>## Skills</skills_instructions>"}]}}
{"timestamp":"2026-08-18T22:44:16.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/workspace</cwd>\n</environment_context>"}]}}
{"timestamp":"2026-08-18T22:44:16.200Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"finish the pagination work"}]}}
{"timestamp":"2026-08-18T22:44:20.000Z","type":"event_msg","payload":{"type":"agent_message","message":"I'll start with the handler."}}
{"timestamp":"2026-08-18T22:44:21.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I'll start with the handler."}]}}
{"timestamp":"2026-08-18T22:44:30.000Z","type":"response_item","payload":{"type":"token_count","info":{"total":1200}}}
{"timestamp":"2026-08-18T22:44:49.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done — the cursor is wired."}]}}
`

func codexFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "2026", "08", "18",
		"rollout-2026-08-18T15-44-15-01a0170b-f760-7b20-a50c-654198f0a759.jsonl")
	writeTranscript(t, path, codexRollout)
	return path
}

// What a codex conversation is *called*, and how many prompts it holds.
//
// Both numbers were wrong before this reader existed in a way that mattered
// more than being unknown: with the injected turn counted, a session someone
// typed one prompt into reported two, and every codex conversation in the list
// was titled "<environment_context>".
func TestReadCodexSessionCountsOnlyTypedPrompts(t *testing.T) {
	path := codexFixture(t)
	s := Session{Agent: "codex", ID: sessionID(path), Path: path, Partial: true}
	readCodexSession(path, &s)

	if s.Partial {
		t.Error("a rollout this reader understood must not stay partial")
	}
	if s.Turns != 1 {
		t.Errorf("turns = %d, want 1: the environment_context block and the developer instructions are not prompts", s.Turns)
	}
	if s.Title != "finish the pagination work" {
		t.Errorf("title = %q, want the first prompt somebody typed", s.Title)
	}
	if s.Project != "/workspace" {
		t.Errorf("project = %q, want the cwd from session_meta", s.Project)
	}
	if s.ID != "01a0170b-f760-7b20-a50c-654198f0a759" {
		t.Errorf("id = %q, want the one session_meta declares", s.ID)
	}
	if s.Started.IsZero() {
		t.Error("started is zero; session_meta carries it")
	}
}

// The same rule applied to the *contents*, which is what a console view renders
// and what a handoff briefing carries across. An injected block reaching either
// would be quoted back to the next agent as though somebody had asked for it.
func TestCodexTranscriptDropsWhatNobodyTyped(t *testing.T) {
	msgs, err := Transcript(codexFixture(t), 0)
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (one prompt, two answers): %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Text != "finish the pagination work" {
		t.Errorf("first message = %+v, want the typed prompt", msgs[0])
	}
	for _, m := range msgs {
		if strings.Contains(m.Text, "environment_context") || strings.Contains(m.Text, "skills_instructions") {
			t.Errorf("an injected block reached the transcript: %+v", m)
		}
	}
	if msgs[len(msgs)-1].At.IsZero() {
		t.Error("timestamps are on every line and should survive")
	}
}

// Transcript takes a path and no format, so the file has to say what it is.
// A caller that had to supply the format would be reconstructing a fact at three
// call sites, and getting it wrong yields zero messages with nothing to explain.
func TestTranscriptPicksTheReaderFromTheFile(t *testing.T) {
	if got := sniffFormat(codexFixture(t)); got != FormatCodexRollout {
		t.Errorf("sniffFormat on a rollout = %q, want %q", got, FormatCodexRollout)
	}
	dir := t.TempDir()
	claude := filepath.Join(dir, "3f2a1b4c-0000-4000-8000-000000000001.jsonl")
	writeTranscript(t, claude, claudeTranscript)
	if got := sniffFormat(claude); got != FormatClaudeJSONL {
		t.Errorf("sniffFormat on a claude transcript = %q, want %q", got, FormatClaudeJSONL)
	}
}

// The id of a codex session is at the *end* of its file name, not the whole of
// it. This is pinned because a second copy of the rule already existed in
// internal/studioapi and was wrong in exactly this way: a run reported
// `rollout-2026-08-18T15-48-43-<uuid>` as its session id and built a resume
// command from it, which the agent refuses. A wrong answer shaped like a right
// one is what a duplicated rule produces.
func TestSessionIDFromPathReadsPrefixedNames(t *testing.T) {
	for name, want := range map[string]string{
		"rollout-2026-08-18T15-44-15-01a0170b-f760-7b20-a50c-654198f0a759.jsonl": "01a0170b-f760-7b20-a50c-654198f0a759",
		"3f2a1b4c-0000-4000-8000-000000000001.jsonl":                             "3f2a1b4c-0000-4000-8000-000000000001",
		"not-a-uuid.jsonl": "not-a-uuid",
	} {
		if got := SessionIDFromPath(filepath.Join("/store", name)); got != want {
			t.Errorf("SessionIDFromPath(%q) = %q, want %q", name, got, want)
		}
	}
}
