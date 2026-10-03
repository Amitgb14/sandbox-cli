package agentctx

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Reading codex's rollout transcripts.
//
// The format is JSONL like claude's and shares none of its field names. One
// `session_meta` line opens the file with the id, the working directory and the
// start time; the conversation itself is `response_item` lines whose payload is
// a `message` with a role and a list of content blocks. A parallel `event_msg`
// stream repeats much of it — deliberately ignored here, because reading both
// would count every turn twice.
//
// The rule that matters is the one claude's reader already states, wearing
// different clothes: **a user turn is a prompt somebody typed.** In claude's
// transcripts the impostors are tool results arriving as user messages; here
// they are two other things, and both were confirmed against a real rollout
// before this was written:
//
//   - **`developer` messages**, which are the system instructions codex ships
//     with — skills, multi-agent mode, its own persona. Never typed by anyone.
//   - **an injected `<environment_context>` user message**, which codex writes
//     as the first user turn of every session: cwd, shell, date. Counting it
//     made a one-prompt session report two, and made the *title* of every codex
//     conversation "<environment_context>".
//
// The tag list is short and named rather than a rule about angle brackets,
// which is the same trade internal/creds makes with token prefixes: a rule that
// dropped anything starting with `<` would silently discard a real prompt that
// opens with one. `environment_context` is verified against a rollout this
// machine produced; `user_instructions` is listed defensively — codex is
// documented to inject repository instructions, and a session whose first
// "prompt" is an AGENTS.md would be wrong in exactly the same way.
var codexInjectedTags = []string{"<environment_context>", "<user_instructions>"}

// codexLine is one line of a rollout file. Only the fields this reads are
// declared; the rest of the payload (token counts, reasoning, tool calls) is
// deliberately not modelled, so an upstream addition cannot break the parse.
type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"` // session_meta | response_item | event_msg | …
	Payload   struct {
		Type      string          `json:"type"` // message, on a response_item
		Role      string          `json:"role"` // developer | user | assistant
		Content   json.RawMessage `json:"content"`
		SessionID string          `json:"session_id"` // session_meta
		Cwd       string          `json:"cwd"`        // session_meta
		Timestamp string          `json:"timestamp"`  // session_meta
	} `json:"payload"`
}

// codexBlock is one element of a message's content list. User turns carry
// `input_text`, assistant turns `output_text`; both are read, because what
// distinguishes them is the role rather than the block name.
type codexBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// codexText flattens a message's content blocks into prose.
func codexText(content json.RawMessage) string {
	var blocks []codexBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		// Older or simpler lines may carry a bare string.
		var plain string
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

// codexTypedPrompt reports whether a user message is something a person typed,
// and returns its text.
func codexTypedPrompt(content json.RawMessage) (string, bool) {
	text := codexText(content)
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}
	for _, tag := range codexInjectedTags {
		if strings.HasPrefix(trimmed, tag) {
			return "", false
		}
	}
	return text, true
}

// codexScanner opens a rollout for line-by-line reading, with the two rules the
// rest of this package keeps: only a regular file is ever opened, and the buffer
// is large enough for a line carrying a whole tool result.
func codexScanner(path string) (*os.File, *bufio.Scanner, bool) {
	// Lstat on the path rather than Stat on the descriptor: Open follows a
	// symlink, and these live in a directory the agent can write.
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return nil, nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	return f, sc, true
}

// readCodexSession fills in what only the transcript can say — the same job
// readClaudeSession does, and best-effort in the same way: the session being
// written right now must still list, with whatever is already on disk.
func readCodexSession(path string, s *Session) {
	f, sc, ok := codexScanner(path)
	if !ok {
		return
	}
	defer f.Close()

	var firstPrompt string
	// Whether this file said anything this reader understood. Without it, a
	// JSONL file that happens to sit in codex's store — `{}` on a line, a
	// half-written first write, a format that has moved on — would come back
	// `Partial: false` with an empty title and zero turns, which reads as "an
	// empty conversation" rather than "not understood". That is the same
	// distinction internal/agentusage draws when a cache shape stops parsing: no
	// answer, never a zero.
	var recognised bool
	for sc.Scan() {
		var l codexLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue // one unreadable line is not a reason to lose the session
		}
		if l.Type == "session_meta" {
			recognised = true
			if s.ID == "" && l.Payload.SessionID != "" {
				s.ID = l.Payload.SessionID
			}
			if s.Project == "" && l.Payload.Cwd != "" {
				// Written by the agent, so it is cleaned before it can reach a
				// terminal — the same treatment the title gets.
				s.Project = termsafe.Clean(l.Payload.Cwd)
			}
			if s.Started.IsZero() {
				if t, err := time.Parse(time.RFC3339, l.Payload.Timestamp); err == nil {
					s.Started = t
				}
			}
			continue
		}
		if l.Type != "response_item" || l.Payload.Type != "message" {
			continue
		}
		recognised = true
		if l.Payload.Role != "user" {
			continue
		}
		text, typed := codexTypedPrompt(l.Payload.Content)
		if !typed {
			continue
		}
		s.Turns++
		if firstPrompt == "" {
			firstPrompt = text
		}
	}
	// Only now: everything above may have run on a half-written file, and this
	// says the fields are *this reader's* answer rather than the filename's.
	// Codex writes no title of its own, so the first typed prompt is the name —
	// the same fallback claude's reader uses for a session too short to have been
	// titled.
	if !recognised {
		return // leave it Partial: the id and dates are real, nothing else is known
	}
	s.Partial = false
	s.Title = oneLine(firstPrompt)
}

// codexTranscript reads the last n turns as vendor-neutral messages, for the
// console view and for a handoff briefing.
func codexTranscript(path string, n int) ([]Message, error) {
	f, sc, ok := codexScanner(path)
	if !ok {
		return nil, errNotRegular(path)
	}
	defer f.Close()

	var out []Message
	for sc.Scan() {
		var l codexLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue
		}
		if l.Type != "response_item" || l.Payload.Type != "message" {
			continue
		}
		var text string
		switch l.Payload.Role {
		case "user":
			t, typed := codexTypedPrompt(l.Payload.Content)
			if !typed {
				continue // injected context, not a prompt
			}
			text = t
		case "assistant":
			text = codexText(l.Payload.Content)
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
