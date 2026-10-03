package agentctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `{"type":"user","timestamp":"2026-10-02T10:00:00Z","message":{"role":"user","content":"fix the \u001b[2Jbug"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Looking."},{"type":"tool_use","name":"Read"}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"file body"}]}}
{"type":"user","isMeta":true,"message":{"role":"user","content":"meta"}}
{"type":"user","message":{"role":"user","conte
`

// A tool result coming back as a user message is not a prompt; they outnumber
// real ones about thirty to one. A half-written last line is the normal state
// of a live transcript. Control characters are stripped: this text is printed
// and handed to another agent.
func TestParseTranscript(t *testing.T) {
	msgs, err := ParseTranscript(strings.NewReader(sample), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Text != "Looking." {
		t.Fatalf("got %+v", msgs)
	}
	if msgs[0].Text != "fix the [2Jbug" || msgs[0].At.IsZero() {
		t.Errorf("first: %+v", msgs[0])
	}
	if last, _ := ParseTranscript(strings.NewReader(sample), 1); len(last) != 1 || last[0].Role != "assistant" {
		t.Errorf("n=1 should keep the last turn: %+v", last)
	}
}

// The transcript directory is writable by the agent, so a symlink there must
// not have the host read whatever it points at.
func TestTranscriptRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.jsonl")
	os.WriteFile(real, []byte(sample), 0o600)
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Transcript(link, 0); err == nil {
		t.Fatal("read through a symlink")
	}
	if msgs, err := Transcript(real, 0); err != nil || len(msgs) != 2 {
		t.Fatalf("%v %v", msgs, err)
	}
}
