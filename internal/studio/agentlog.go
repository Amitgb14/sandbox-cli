package studio

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
)

// The agent log is Studio's record of what each agentos session did: the
// prompts it was given, every tool call with its arguments and result, each
// permission asked and how it was answered, and how the session ended. The
// ACP bridge writes it from the messages it relays, on the machine Studio
// runs on, so it outlives the sandbox and nothing in the guest can change it
// after the fact. What the guest can do is decide what it says about itself:
// a tool call's title and output are the agent's own account, recorded as
// sent.
//
// It is a JSONL file kept as the server's audit log is (internal/audit): 0600,
// rotated at 8 MiB with five old generations, and best-effort, so a session is
// never refused because its record could not be written. Text is cut at
// agentLogField bytes and images are never written, only counted.
//
// A tool call's arguments are written as the agent sent them, and they are
// where a secret the agent handled would show (a curl with a token, a file it
// read). The log is as sensitive as the sandbox's own files.

// AgentRecord is one line of the agent log. Every record names the sandbox by
// id (and its name then), the session and the agent; the rest depends on Type:
//
//	session.start  Image
//	prompt         Text: what was asked
//	tool           ToolID, Title, Kind, Status, Started, Input, Output, Images
//	permission     Title, Option and OptionKind: the answer, or Status "unanswered"
//	mode           Mode: a mode the page asked for
//	cancel         the page stopped the turn
//	turn.end       StopReason, Text: the agent's reply
//	limit          the session reached agentLogSession; nothing more is kept but its end
//	session.end    Code: the agent's exit code, or Status "closed" when the page left first;
//	               Dropped: records not kept past the limit
type AgentRecord struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Sandbox string    `json:"sandbox"`
	Name    string    `json:"name,omitempty"`
	Session string    `json:"session"`
	Agent   string    `json:"agent"`
	// User and Org are a hosted Studio's signed-in user and the organisation
	// the page acted in.
	User string `json:"user,omitempty"`
	Org  string `json:"org,omitempty"`

	Image      string     `json:"image,omitempty"`
	Text       string     `json:"text,omitempty"`
	ToolID     string     `json:"tool_id,omitempty"`
	Title      string     `json:"title,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Status     string     `json:"status,omitempty"`
	Started    *time.Time `json:"started,omitempty"`
	Input      string     `json:"input,omitempty"`
	Output     string     `json:"output,omitempty"`
	Images     int        `json:"images,omitempty"`
	Option     string     `json:"option,omitempty"`
	OptionKind string     `json:"option_kind,omitempty"`
	Mode       string     `json:"mode,omitempty"`
	StopReason string     `json:"stop_reason,omitempty"`
	Code       *int       `json:"code,omitempty"`
	Dropped    int        `json:"dropped,omitempty"`
	// Truncated is set when a text field was cut at agentLogField bytes.
	Truncated bool `json:"truncated,omitempty"`
}

const (
	// agentLogField bounds each text field of a record: enough to read a
	// prompt, a command or an error, and a record stays a few KiB however
	// much the agent sent.
	agentLogField = 4 << 10
	// agentLogOpen bounds what one session holds in memory: tool calls
	// started and not finished, questions not answered, prompts not
	// answered. Past it, a tool call is written as it stands rather than
	// waited for.
	agentLogOpen = 256
	// agentLogReadMax is the most records a read returns: the newest.
	agentLogReadMax = 2000
	// agentLogSession is how much one session may write. The log is shared
	// and rotates by size, so without it an agent that floods tool calls
	// would rotate every other sandbox's history away; with it, a flood
	// costs a tenth of the log. A session exists only while a page holds
	// it open, so the guest cannot start more of them.
	agentLogSession = 4 << 20
)

// AgentLogResponse is GET /api/agent-log.
type AgentLogResponse struct {
	// Enabled is false when this Studio keeps no agent log.
	Enabled   bool          `json:"enabled"`
	Records   []AgentRecord `json:"records"`
	Truncated bool          `json:"truncated,omitempty"`
}

// agentLog is GET /api/agent-log?sandbox=: the sandbox's records, oldest
// first. The sandbox is looked up with the caller's own credentials first,
// and its id is what the log is searched for: a caller reads the record of a
// sandbox they can see, and a name used again later reads only its own.
func (s *Server) agentLog(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("sandbox")
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "sandbox: required")
		return
	}
	if s.AgentLog == nil {
		writeJSON(w, http.StatusOK, AgentLogResponse{Records: []AgentRecord{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	sb, err := s.clientFor(r).Sandbox(ctx, ref)
	if e := (*api.Error)(nil); errors.As(err, &e) {
		writeErr(w, e.Status, e.Error())
		return
	} else if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	recs, truncated := readAgentLog(s.AgentLog.Path, sb.ID, agentLogReadMax)
	writeJSON(w, http.StatusOK, AgentLogResponse{Enabled: true, Records: recs, Truncated: truncated})
}

// readAgentLog returns the newest max records of one sandbox, oldest first.
func readAgentLog(path, sandbox string, max int) ([]AgentRecord, bool) {
	// Records are written by json.Marshal, so a sandbox's all hold this, and
	// the lines that do not are passed over without being parsed.
	needle, _ := json.Marshal(sandbox)
	needle = append([]byte(`"sandbox":`), needle...)
	recs := []AgentRecord{}
	audit.Lines(path, func(line []byte) {
		if !bytes.Contains(line, needle) {
			return
		}
		var rec AgentRecord
		if json.Unmarshal(line, &rec) == nil && rec.Sandbox == sandbox {
			recs = append(recs, rec)
		}
	})
	if max > 0 && len(recs) > max {
		return recs[len(recs)-max:], true
	}
	return recs, false
}

// acpRecorder writes one ACP session's records from the messages the bridge
// relays: Page is each message from the page, Agent each from the agent.
// Both are called from their own goroutines.
type acpRecorder struct {
	log  *audit.Log
	base AgentRecord
	now  func() time.Time

	mu      sync.Mutex
	tools   map[string]*AgentRecord // started, not yet finished
	order   []string                // tools, oldest first, for the end
	asked   map[string]asked        // permission requests, by RPC id
	prompts map[string]bool         // the page's session/prompt requests, by RPC id
	reply   []byte                  // the agent's message this turn
	budget  int                     // agentLogSession, but in tests
	written int                     // bytes this session has written
	dropped int                     // records not written past agentLogSession
	ended   bool
}

// newACPRecorder returns nil, which records nothing, when log is nil.
func newACPRecorder(log *audit.Log, base AgentRecord) *acpRecorder {
	if log == nil {
		return nil
	}
	if base.Session == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		base.Session = hex.EncodeToString(b)
	}
	return &acpRecorder{
		log: log, base: base, budget: agentLogSession, now: func() time.Time { return time.Now().UTC() },
		tools: map[string]*AgentRecord{}, asked: map[string]asked{}, prompts: map[string]bool{},
	}
}

// acpMessage is the part of a JSON-RPC message the recorder reads.
type acpMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

func (m acpMessage) key() string { return string(m.ID) }

func (r *acpRecorder) record(rec AgentRecord) {
	b := r.base
	rec.Time, rec.Sandbox, rec.Name, rec.Session, rec.Agent, rec.User, rec.Org = r.now(), b.Sandbox, b.Name, b.Session, b.Agent, b.User, b.Org
	over := r.written >= r.budget
	if over && rec.Type != "session.end" && rec.Type != "limit" {
		r.dropped++
		return
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	r.written += len(line) + 1
	r.log.Append(json.RawMessage(line))
	if !over && r.written >= r.budget && rec.Type != "session.end" {
		r.record(AgentRecord{Type: "limit", Text: "this session reached its " + strconv.Itoa(r.budget>>10) + " KiB of records; only its end is kept from here"})
	}
}

// Start writes the session's first record.
func (r *acpRecorder) Start(image string) {
	if r == nil {
		return
	}
	r.record(AgentRecord{Type: "session.start", Image: image})
}

// Page reads one message the page sent the agent.
func (r *acpRecorder) Page(line []byte) {
	if r == nil {
		return
	}
	var m acpMessage
	if json.Unmarshal(line, &m) != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return
	}
	switch {
	case m.Method == "session/prompt":
		var p struct {
			Prompt []acpBlock `json:"prompt"`
		}
		_ = json.Unmarshal(m.Params, &p)
		text, images := blocksText(p.Prompt)
		rec := AgentRecord{Type: "prompt", Images: images}
		rec.Text, rec.Truncated = clip(text)
		if len(m.ID) > 0 && len(r.prompts) < agentLogOpen {
			r.prompts[m.key()] = true
		}
		r.reply = r.reply[:0]
		r.record(rec)
	case m.Method == "session/set_mode":
		var p struct {
			ModeID string `json:"modeId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		r.record(AgentRecord{Type: "mode", Mode: p.ModeID})
	case m.Method == "session/set_config_option":
		var p struct {
			ConfigID string          `json:"configId"`
			Value    json.RawMessage `json:"value"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if p.ConfigID == "mode" {
			var v string
			_ = json.Unmarshal(p.Value, &v)
			r.record(AgentRecord{Type: "mode", Mode: v})
		}
	case m.Method == "session/cancel":
		r.record(AgentRecord{Type: "cancel"})
	case m.Method == "" && len(m.Result) > 0:
		// The page's answer to one of the agent's requests.
		q, ok := r.asked[m.key()]
		if !ok {
			return
		}
		delete(r.asked, m.key())
		var res struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(m.Result, &res)
		rec := q.rec
		rec.Status = res.Outcome.Outcome
		for _, o := range q.options {
			if o.ID == res.Outcome.OptionID {
				rec.Option, _ = clip(o.Name)
				rec.OptionKind, _ = clip(o.Kind)
			}
		}
		r.record(rec)
	}
}

// Agent reads one message the agent sent the page.
func (r *acpRecorder) Agent(line []byte) {
	if r == nil {
		return
	}
	var m acpMessage
	if json.Unmarshal(line, &m) != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return
	}
	switch {
	case m.Method == "session/update":
		var p struct {
			Update acpUpdate `json:"update"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			r.update(p.Update)
		}
	case m.Method == "session/request_permission" && len(m.ID) > 0:
		var p struct {
			ToolCall struct {
				Title string `json:"title"`
			} `json:"toolCall"`
			Options []acpOption `json:"options"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if len(r.asked) >= agentLogOpen {
			return
		}
		q := asked{rec: AgentRecord{Type: "permission"}, options: p.Options}
		if len(q.options) > 16 {
			q.options = q.options[:16]
		}
		q.rec.Title, q.rec.Truncated = clip(p.ToolCall.Title)
		r.asked[m.key()] = q
	case m.Method == "" && r.prompts[m.key()]:
		// The agent's answer to a prompt: the turn is over.
		delete(r.prompts, m.key())
		var res struct {
			StopReason string `json:"stopReason"`
		}
		_ = json.Unmarshal(m.Result, &res)
		rec := AgentRecord{Type: "turn.end", StopReason: res.StopReason}
		rec.Text, rec.Truncated = clip(string(r.reply))
		r.reply = r.reply[:0]
		r.record(rec)
	}
}

// acpUpdate is the part of a session/update the recorder reads.
type acpUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content"`
	ToolCallID    string          `json:"toolCallId"`
	Title         string          `json:"title"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status"`
	RawInput      json.RawMessage `json:"rawInput"`
	RawOutput     json.RawMessage `json:"rawOutput"`
	CurrentModeID string          `json:"currentModeId"`
}

// acpBlock is a content block, or a tool call's content item that holds one.
type acpBlock struct {
	Type    string    `json:"type"`
	Text    string    `json:"text"`
	Path    string    `json:"path"`
	Content *acpBlock `json:"content"`
}

func (r *acpRecorder) update(u acpUpdate) {
	switch u.SessionUpdate {
	case "agent_message_chunk":
		var b acpBlock
		if json.Unmarshal(u.Content, &b) == nil && b.Type == "text" && len(r.reply) <= agentLogField {
			r.reply = append(r.reply, b.Text...)
		}
	case "tool_call", "tool_call_update":
		t := r.tools[u.ToolCallID]
		if t == nil {
			started := r.now()
			t = &AgentRecord{Type: "tool", ToolID: u.ToolCallID, Status: "pending", Started: &started}
			if len(r.tools) < agentLogOpen {
				r.tools[u.ToolCallID] = t
				r.order = append(r.order, u.ToolCallID)
			} else {
				// Too many open to wait for: written as it stands.
				defer r.finish(t, u.Status)
			}
		}
		var cut bool
		if u.Title != "" {
			t.Title, cut = clip(u.Title)
			t.Truncated = t.Truncated || cut
		}
		if u.Kind != "" {
			t.Kind = u.Kind
		}
		if u.Status != "" {
			t.Status = u.Status
		}
		if len(u.RawInput) > 0 && string(u.RawInput) != "null" && string(u.RawInput) != "{}" {
			t.Input, cut = clip(compactJSON(u.RawInput))
			t.Truncated = t.Truncated || cut
		}
		if len(u.Content) > 0 && string(u.Content) != "null" {
			var items []acpBlock
			_ = json.Unmarshal(u.Content, &items)
			var text string
			text, t.Images = blocksText(items)
			t.Output, cut = clip(text)
			t.Truncated = t.Truncated || cut
		}
		if t.Output == "" && len(u.RawOutput) > 0 && string(u.RawOutput) != "null" {
			t.Output, cut = clip(compactJSON(u.RawOutput))
			t.Truncated = t.Truncated || cut
		}
		if _, open := r.tools[u.ToolCallID]; open && (t.Status == "completed" || t.Status == "failed") {
			r.finish(t, "")
		}
	case "current_mode_update":
		r.record(AgentRecord{Type: "mode", Mode: u.CurrentModeID})
	}
}

// finish writes a tool call and forgets it.
func (r *acpRecorder) finish(t *AgentRecord, status string) {
	if status != "" {
		t.Status = status
	}
	if _, open := r.tools[t.ToolID]; open {
		delete(r.tools, t.ToolID)
		for i, id := range r.order {
			if id == t.ToolID {
				r.order = append(r.order[:i], r.order[i+1:]...)
				break
			}
		}
	}
	r.record(*t)
}

// End writes what the session left open and its last record: the agent's exit
// code, or nil when the page left before the agent ended.
func (r *acpRecorder) End(code *int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ended {
		return
	}
	for _, id := range append([]string(nil), r.order...) {
		if t := r.tools[id]; t != nil {
			r.finish(t, "unfinished")
		}
	}
	for _, q := range r.asked {
		rec := q.rec
		rec.Status = "unanswered"
		r.record(rec)
	}
	end := AgentRecord{Type: "session.end", Code: code, Dropped: r.dropped}
	if code == nil {
		end.Status = "closed"
	}
	r.record(end)
	r.ended = true
}

type acpOption struct {
	ID   string `json:"optionId"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// asked is a permission request waiting for the page's answer, which names
// one of its options by id.
type asked struct {
	rec     AgentRecord
	options []acpOption
}

// blocksText is the text of content blocks, one per line, and how many images
// they held.
func blocksText(blocks []acpBlock) (string, int) {
	var b []byte
	images := 0
	add := func(s string) {
		if len(b) > agentLogField {
			return
		}
		if len(b) > 0 {
			b = append(b, '\n')
		}
		b = append(b, s...)
	}
	for _, blk := range blocks {
		if blk.Type == "content" && blk.Content != nil {
			blk = *blk.Content
		}
		switch blk.Type {
		case "text":
			add(blk.Text)
		case "image":
			images++
		case "diff":
			add("edited " + blk.Path)
		case "terminal":
			add("(terminal output)")
		}
	}
	return string(b), images
}

// clip cuts s to agentLogField bytes, on a character boundary.
func clip(s string) (string, bool) {
	if len(s) <= agentLogField {
		return s, false
	}
	n := agentLogField
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return strconv.Quote(string(raw))
	}
	return b.String()
}
