package agentctx

import (
	"strings"
	"testing"
)

// A rollout in the shape codex writes, trimmed to the lines the reader looks
// at, with every impostor a real session has: a developer message carrying
// codex's own instructions, and an injected <environment_context> user turn
// before anything a person typed. From beta.15's line, where it was checked
// against a real rollout.
const codexRollout = `{"timestamp":"2026-08-18T22:44:15.557Z","type":"session_meta","payload":{"session_id":"01a0170b-f760-7b20-a50c-654198f0a759","timestamp":"2026-08-18T22:44:15.409Z","cwd":"/workspace","originator":"codex_exec"}}
{"timestamp":"2026-08-18T22:44:16.000Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<skills_instructions>## Skills</skills_instructions>"}]}}
{"timestamp":"2026-08-18T22:44:16.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/workspace</cwd>\n</environment_context>"}]}}
{"timestamp":"2026-08-18T22:44:16.200Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"finish the pagination work"}]}}
{"timestamp":"2026-08-18T22:44:20.000Z","type":"event_msg","payload":{"type":"agent_message","message":"I'll start with the handler."}}
{"timestamp":"2026-08-18T22:44:21.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I'll start with the handler."}]}}
{"timestamp":"2026-08-18T22:44:30.000Z","type":"response_item","payload":{"type":"token_count","info":{"total":1200}}}
{"timestamp":"2026-08-18T22:44:49.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done — the cursor is wired."}]}}
`

// What crosses into a handoff briefing is what was typed and what was answered:
// not codex's instructions, not the injected context, and not the event_msg
// stream that repeats the answers.
func TestCodexTranscriptDropsWhatNobodyTyped(t *testing.T) {
	msgs, err := ParseCodexTranscript(strings.NewReader(codexRollout), 0)
	if err != nil {
		t.Fatal(err)
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
	if msgs[2].At.IsZero() {
		t.Error("timestamps are on every line and should survive")
	}
	if last, _ := ParseCodexTranscript(strings.NewReader(codexRollout), 1); len(last) != 1 || last[0].Text != "Done — the cursor is wired." {
		t.Errorf("the last turn: %+v", last)
	}
}

// Each reader understands its own format and nothing else: a claude
// transcript read as a rollout, or a rollout as a claude transcript, is no
// conversation rather than a wrong one.
func TestReadersDoNotCrossFormats(t *testing.T) {
	claude := `{"type":"user","timestamp":"2026-10-02T10:00:00Z","message":{"role":"user","content":"hi"}}` + "\n"
	if m, _ := ParseCodexTranscript(strings.NewReader(claude), 0); len(m) != 0 {
		t.Errorf("a claude transcript read as codex: %+v", m)
	}
	if m, _ := ParseTranscript(strings.NewReader(codexRollout), 0); len(m) != 0 {
		t.Errorf("a codex rollout read as claude: %+v", m)
	}
}
