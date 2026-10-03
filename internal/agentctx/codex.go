package agentctx

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// Reading codex's rollout transcripts, ported from beta.15's line after 0.0.1
// (_old/internal/agentctx/codex.go), where the format was verified against a
// rollout a real session wrote.
//
// The format is JSONL like claude's and shares none of its field names. A
// `session_meta` line opens the file; the conversation is `response_item`
// lines whose payload is a `message` with a role and a list of content blocks.
// A parallel `event_msg` stream repeats much of it and is ignored, because
// reading both would count every turn twice.
//
// The rule is the one ParseTranscript keeps, in different clothes: **a user
// turn is a prompt somebody typed.** Here the impostors are `developer`
// messages (codex's own instructions) and an injected `<environment_context>`
// user turn that opens every session. Either one reaching a handoff briefing
// would be quoted to the next agent as though somebody had asked for it.
//
// The tag list is named rather than a rule about angle brackets, so a real
// prompt that opens with `<` is not silently dropped. `environment_context` is
// verified; `user_instructions` is listed defensively, since codex is
// documented to inject repository instructions the same way.
var codexInjectedTags = []string{"<environment_context>", "<user_instructions>"}

// codexLine declares only the fields this reads, so an upstream addition to
// the payload cannot break the parse.
type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"` // session_meta | response_item | event_msg | …
	Payload   struct {
		Type    string          `json:"type"` // message, on a response_item
		Role    string          `json:"role"` // developer | user | assistant
		Content json.RawMessage `json:"content"`
	} `json:"payload"`
}

// codexText flattens a message's content blocks into prose. User turns carry
// input_text and assistant turns output_text; the role, not the block name, is
// what tells them apart.
func codexText(content json.RawMessage) string {
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		var plain string // older or simpler lines may carry a bare string
		if err := json.Unmarshal(content, &plain); err == nil {
			return plain
		}
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}

// ParseCodexTranscript is ParseTranscript for a codex rollout, read out of a
// sandbox: the conversation's typed prompts and the agent's answers, the last
// n of them (0: all).
func ParseCodexTranscript(r io.Reader, n int) ([]Message, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	var out []Message
	for sc.Scan() {
		var l codexLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue
		}
		if l.Type != "response_item" || l.Payload.Type != "message" {
			continue
		}
		text := codexText(l.Payload.Content)
		switch l.Payload.Role {
		case "user":
			trimmed := strings.TrimSpace(text)
			injected := false
			for _, tag := range codexInjectedTags {
				injected = injected || strings.HasPrefix(trimmed, tag)
			}
			if injected {
				continue
			}
		case "assistant":
		default:
			continue // developer: codex's own instructions, which nobody typed
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		m := Message{Role: l.Payload.Role, Text: cleanBody(text)}
		if t, err := time.Parse(time.RFC3339, l.Timestamp); err == nil {
			m.At = t
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}
