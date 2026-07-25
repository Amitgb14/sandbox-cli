package agentctx

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session is one conversation in an agent's store, described in the terms a
// person picks one out by: when it was, what it was about, and the id to resume.
type Session struct {
	ID       string    `json:"id"`
	Agent    string    `json:"agent"`
	Path     string    `json:"path"`
	Project  string    `json:"project,omitempty"` // working directory recorded in the transcript
	Title    string    `json:"title,omitempty"`
	Turns    int       `json:"turns"` // prompts sent by the user, not messages exchanged
	Started  time.Time `json:"started,omitempty"`
	Modified time.Time `json:"modified"`
	Size     int64     `json:"size"`

	// Partial marks a session listed from its file alone, because sandbox-cli has
	// no verified reader for this agent's format. The id and the dates are real;
	// the title and turn count are simply unknown, and are shown as unknown.
	Partial bool `json:"partial,omitempty"`
}

// ListOpts scopes a listing to one project. Only stores that keep a derivable
// per-project directory can honour it; for the others every session is returned
// and Scoped comes back false, so a caller can say so rather than implying a
// filter that did not happen.
type ListOpts struct {
	Project string // absolute path of the project, as this process sees it
	All     bool   // every project in the store
}

// scopeOf narrows a store to one project's directory when the store has one that
// can be derived. empty reports a project the store has simply never seen, which
// is not an error — no agent has worked there yet.
func scopeOf(f Finding, o ListOpts) (dir string, depth int, scoped, empty bool) {
	store, _ := Lookup(f.Agent)
	dir, depth = f.Dir, storeDepth(store)
	if o.All || o.Project == "" || store.BucketStyle != BucketDashedPath {
		return dir, depth, false, false
	}
	// The per-project directory is one level of the store, so scoping to it
	// removes exactly one level of the search.
	cand := filepath.Join(dir, ProjectBucket(o.Project))
	if !isDir(cand) {
		return dir, depth, true, true
	}
	return cand, depth - 1, true, false
}

// List returns the sessions in a verified store, newest first.
func List(f Finding, o ListOpts) (sessions []Session, scoped bool, err error) {
	if f.Dir == "" {
		return nil, false, nil
	}
	store, _ := Lookup(f.Agent)
	dir, depth, scoped, empty := scopeOf(f, o)
	if empty {
		return nil, scoped, nil
	}

	err = walkSessionFiles(dir, store.Glob, store.SubDir, depth, func(path string, info fs.FileInfo) {
		s := Session{
			Agent:    f.Agent,
			ID:       sessionID(path),
			Path:     path,
			Modified: info.ModTime(),
			Size:     info.Size(),
			Partial:  true,
		}
		if f.Format == FormatClaudeJSONL {
			readClaudeSession(path, &s)
		}
		sessions = append(sessions, s)
	})
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Modified.After(sessions[j].Modified) })
	return sessions, scoped, err
}

// storeDepth is the descriptor's depth, defaulting to something sane for a
// Finding whose descriptor has since been removed from the table.
func storeDepth(s Store) int {
	if s.MaxDepth == 0 {
		return 1
	}
	return s.MaxDepth
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// sessionID is the id in the file's name. Every store seen so far names a
// session file after its id, with an optional prefix and the extension around it
// (`<uuid>.jsonl`, `rollout-<timestamp>-<uuid>.jsonl`), so the name is the one
// piece of a session that can be read without understanding the format.
func sessionID(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if u, ok := trailingUUID(name); ok {
		return u
	}
	return name
}

// trailingUUID pulls a UUID off the end of a file name, which is how the stores
// that prefix their session files still expose the id the agent resumes by.
func trailingUUID(name string) (string, bool) {
	const uuidLen = 36
	if len(name) < uuidLen {
		return "", false
	}
	tail := name[len(name)-uuidLen:]
	for i, r := range tail {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return "", false
			}
		default:
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				return "", false
			}
		}
	}
	return tail, true
}

// claudeLine is the part of one transcript line this package reads. Everything
// else in the line is ignored by encoding/json, which is what keeps the reader
// from breaking every time Claude Code adds a field.
//
// Timestamp is a string rather than a time.Time on purpose: a single line with an
// unparseable date would otherwise fail to decode entirely, losing the fields
// next to it.
type claudeLine struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	AITitle     string `json:"aiTitle"`
	LastPrompt  string `json:"lastPrompt"`
	Message     *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// readClaudeSession fills in what only the transcript can say. It is best-effort
// by design: a truncated or half-written transcript — the normal state of the
// session that is running right now — must still list, with whatever was already
// on disk.
func readClaudeSession(path string, s *Session) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Transcript lines carry whole tool results and can be megabytes; the default
	// 64KB limit would stop the scan partway through most real sessions.
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)

	var firstPrompt, lastPrompt, aiTitle string
	for sc.Scan() {
		var l claudeLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue // one unreadable line is not a reason to lose the session
		}
		if s.ID == "" && l.SessionID != "" {
			s.ID = l.SessionID
		}
		if s.Project == "" && l.Cwd != "" {
			s.Project = l.Cwd
		}
		if s.Started.IsZero() && l.Timestamp != "" {
			if t, terr := time.Parse(time.RFC3339, l.Timestamp); terr == nil {
				s.Started = t
			}
		}
		switch l.Type {
		case "ai-title":
			// Claude Code names the conversation itself; the last one it wrote is
			// the one that describes where the session ended up.
			if l.AITitle != "" {
				aiTitle = l.AITitle
			}
			continue
		case "last-prompt":
			if l.LastPrompt != "" {
				lastPrompt = l.LastPrompt
			}
			continue
		case "user":
			// Not every "user" line is something the user typed: tool results come
			// back as user messages too, and outnumber real prompts by roughly
			// thirty to one. Counting them would make every session look enormous.
			if l.IsMeta || l.IsSidechain || l.Message == nil {
				continue
			}
			text, ok := userPromptText(l.Message.Content)
			if !ok {
				continue
			}
			s.Turns++
			if firstPrompt == "" {
				firstPrompt = text
			}
		}
	}
	s.Partial = false

	switch {
	case aiTitle != "":
		s.Title = aiTitle
	case firstPrompt != "":
		s.Title = firstPrompt
	default:
		s.Title = lastPrompt // short sessions never get a generated title
	}
	s.Title = oneLine(s.Title)
}

// userPromptText reports whether a user message is a prompt someone typed, and
// returns its text. Content is either a string (the plain case) or a block array;
// an array holding a tool_result is the tool-call return path, while an array
// with text blocks is a real prompt that carried an attachment.
func userPromptText(content json.RawMessage) (string, bool) {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return "", false
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(content, &s); err != nil || strings.TrimSpace(s) == "" {
			return "", false
		}
		return s, true
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", false
	}
	var text string
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return "", false
		}
		if b.Type == "text" && text == "" {
			text = b.Text
		}
	}
	if text == "" {
		return "", false
	}
	return text, true
}

// oneLine flattens a prompt for a table cell. Prompts are routinely pasted logs
// or several paragraphs, and the first line is the part that identifies them.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return strings.Join(strings.Fields(s), " ")
}

// SessionRef is a session's id and the file it lives in, without its contents.
type SessionRef struct {
	ID   string
	Path string
}

// Dir is the directory holding the session — for a store organised by project,
// the project's own directory. Which directory a session is in decides whether a
// sandbox can reach it at all, since only the current project's history is
// mounted into the container.
func (r SessionRef) Dir() string { return filepath.Dir(r.Path) }

// SessionRefs returns the ids in a store, read from file names alone. It is the
// cheap counterpart to List: no transcript is opened, so it is safe to call on
// the path of every agent run, where reading dozens of multi-megabyte transcripts
// to expand one argument would be an absurd price.
func SessionRefs(f Finding, o ListOpts) []SessionRef {
	if f.Dir == "" {
		return nil
	}
	store, _ := Lookup(f.Agent)
	dir, depth, _, empty := scopeOf(f, o)
	if empty {
		return nil
	}
	var out []SessionRef
	_ = walkSessionFiles(dir, store.Glob, store.SubDir, depth, func(path string, _ fs.FileInfo) {
		out = append(out, SessionRef{ID: sessionID(path), Path: path})
	})
	return out
}

// ExpandID turns a shortened session id into the full one, searching only the
// sessions the given scope covers. It returns ok=false unless exactly one matches,
// so an ambiguous or unknown value is left exactly as the user typed it rather
// than being guessed at — the agent's own error is better than the wrong session,
// and the value may not be an id at all (Claude Code also resumes by title).
func ExpandID(f Finding, o ListOpts, short string) (SessionRef, bool) {
	if short == "" || len(short) >= 36 {
		return SessionRef{}, false // already a full id, or nothing to work with
	}
	var hit SessionRef
	for _, r := range SessionRefs(f, o) {
		if r.ID == short {
			return SessionRef{}, false // exact already
		}
		if strings.HasPrefix(r.ID, short) {
			if hit.ID != "" {
				return SessionRef{}, false // ambiguous
			}
			hit = r
		}
	}
	return hit, hit.ID != ""
}

// Find resolves a user-typed session id against a store. An unambiguous prefix is
// enough: session ids are UUIDs, and nobody should have to type one in full.
func Find(sessions []Session, id string) (Session, []Session) {
	var hits []Session
	for _, s := range sessions {
		if s.ID == id {
			return s, nil
		}
		if strings.HasPrefix(s.ID, id) {
			hits = append(hits, s)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	return Session{}, hits
}
