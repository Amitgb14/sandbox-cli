package agentctx

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Message is one turn of a conversation, as read from a transcript.
type Message struct {
	Role string    `json:"role"` // "user" | "assistant"
	Text string    `json:"text"`
	At   time.Time `json:"at,omitempty"`
}

// Transcript reads the last n turns of a claude-jsonl transcript.
//
// This is the *contents* of a session, where List reads only enough to describe
// one. It exists for the live console: a run somebody is watching is a
// conversation they may need to answer, and the transcript is the only place
// that conversation is legible. The alternative — parsing the agent's own
// terminal output — is parsing a TUI mid-redraw, which produces text that looks
// like an answer and is not.
//
// It keeps the two rules the rest of this package keeps. Only a regular file is
// ever opened (the transcript lives in a directory the agent can write, so it
// can put a symlink there and have the host render whatever it points at), and
// a *user* turn is a prompt somebody typed — never a tool result coming back as
// a user message, which outnumber real prompts roughly thirty to one.
//
// Unreadable lines are skipped rather than fatal: a transcript is being appended
// to while this reads it, so the last line is routinely half-written.
func Transcript(path string, n int) ([]Message, error) {
	// Lstat on the path, not Stat on the opened file: Open follows the link, so a
	// symlink to a regular file looks regular. The question is what this path is,
	// not what it leads to.
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errNotRegular(path)
	}
	// Which reader, decided by the file rather than by the caller.
	//
	// Every caller has a path and only some have an agent — the console
	// correlates one, a briefing is asked for by id — so a format *parameter*
	// would be a fact reconstructed at three call sites, and a caller that
	// reconstructed it wrongly would get zero messages with nothing to say why.
	// The file answers for itself: a rollout opens with a session_meta line that
	// a claude transcript has no shape for.
	if sniffFormat(path) == FormatCodexRollout {
		return codexTranscript(path, n)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Same limit List uses, and for the same reason: one tool result can be
	// megabytes, and the default 64KB would stop the scan partway through.
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)

	var out []Message
	for sc.Scan() {
		var l claudeLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue
		}
		if l.Message == nil || l.IsMeta || l.IsSidechain {
			continue
		}
		var text string
		switch l.Type {
		case "user":
			t, ok := userPromptText(l.Message.Content)
			if !ok {
				continue // a tool result, not something anyone typed
			}
			text = t
		case "assistant":
			text = assistantText(l.Message.Content)
		default:
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		m := Message{Role: l.Type, Text: cleanBody(text)}
		if l.Timestamp != "" {
			if t, terr := time.Parse(time.RFC3339, l.Timestamp); terr == nil {
				m.At = t
			}
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

// assistantText pulls the prose out of an assistant turn, dropping tool calls.
//
// What the agent *did* is already visible as a diff, a log and a set of
// commits. What is wanted here is what it *said*, because that is the half that
// can contain a question.
func assistantText(content json.RawMessage) string {
	// Same two shapes userPromptText handles, for the same reason: the field is
	// a bare string in older transcripts and a block list in current ones.
	var plain string
	if err := json.Unmarshal(content, &plain); err == nil {
		return plain
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type != "text" || blk.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(blk.Text)
	}
	return b.String()
}

// cleanBody strips control characters while keeping the line structure.
//
// Deliberately not termsafe.Clean, which collapses all whitespace into single
// spaces — right for a value going into a table cell, wrong for a message body,
// where flattening the newlines out of a code block or a numbered list destroys
// the thing being read. Newline and tab survive; everything else below 0x20,
// DEL, and the C1 range (where the escape sequences that repaint a terminal
// live) do not.
func cleanBody(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

// FirstPrompt returns the first thing a person typed in a transcript.
//
// Cheap on purpose: it stops at the first real user turn rather than reading
// the file, because the caller is identifying a session rather than reading
// one, and a long transcript is megabytes of tool results.
//
// This exists to tell two concurrent sessions apart. Sandbox runs pool into one
// `-workspace` directory, so a time window alone cannot say which transcript
// belongs to which container — demonstrated the expensive way, with one run's
// conversation shown under another run's id. A run's first prompt is recorded
// on its container as a label, and comparing the two is an actual answer rather
// than a tie-break.
func FirstPrompt(path string) (string, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var l claudeLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue
		}
		if l.Type != "user" || l.Message == nil || l.IsMeta || l.IsSidechain {
			continue
		}
		if text, ok := userPromptText(l.Message.Content); ok {
			return strings.TrimSpace(text), true
		}
	}
	return "", false
}

// errNotRegular is the refusal shared by every reader here: these files live in
// a directory the agent can write, so a symlink named like a transcript must be
// refused rather than followed and rendered.
func errNotRegular(path string) error {
	return fmt.Errorf("not a regular file: %s", path)
}

// sniffFormat reads far enough to tell a codex rollout from a claude transcript.
//
// Bounded to the first few lines: a rollout's session_meta is line one, and a
// file that has not said what it is by then is read as claude's — the format
// this package was written against, and the one whose reader treats an
// unrecognised line as skippable rather than fatal.
func sniffFormat(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return FormatClaudeJSONL
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for i := 0; i < 4 && sc.Scan(); i++ {
		var probe struct {
			Type    string `json:"type"`
			Payload *struct {
				SessionID string `json:"session_id"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &probe) != nil {
			continue
		}
		if probe.Type == "session_meta" && probe.Payload != nil {
			return FormatCodexRollout
		}
	}
	return FormatClaudeJSONL
}
