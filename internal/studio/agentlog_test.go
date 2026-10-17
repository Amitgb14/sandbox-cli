package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
)

// records reads every record in the log at path, in order.
func records(t *testing.T, path string) []AgentRecord {
	t.Helper()
	var out []AgentRecord
	audit.Lines(path, func(line []byte) {
		var r AgentRecord
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		out = append(out, r)
	})
	return out
}

func types(recs []AgentRecord) string {
	var s []string
	for _, r := range recs {
		s = append(s, r.Type)
	}
	return strings.Join(s, " ")
}

// A session's messages become one record for each prompt, each tool call
// (its updates merged, written once it finishes), each answered question and
// the turn's end, in the order they happened.
func TestACPRecorder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-log.jsonl")
	rec := newACPRecorder(audit.NewLog(path), AgentRecord{Sandbox: "sbx_1", Name: "os", Agent: "claude", User: "ada", Org: "lab"})
	clock := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	rec.now = func() time.Time { clock = clock.Add(time.Second); return clock }

	page := func(s string) { rec.Page([]byte(s)) }
	agent := func(s string) { rec.Agent([]byte(s)) }
	update := func(u string) {
		agent(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":` + u + `}}`)
	}
	big := strings.Repeat("é", agentLogField) // two bytes each: cut mid-way

	rec.Start("ghcr.io/x/sandbox-agentos:edge")
	page(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	agent(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1}}`)
	page(`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"s","prompt":[{"type":"text","text":"open example.com"},{"type":"image","data":"AAAA","mimeType":"image/png"}]}}`)
	// Started with no input, which a later update brings, and finished.
	update(`{"sessionUpdate":"tool_call","toolCallId":"t1","title":"screenshot","kind":"other","status":"pending","rawInput":{}}`)
	update(`{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"in_progress","rawInput":{"display": ":0"}}`)
	update(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Opening "}}`)
	update(`{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"saved"}},{"type":"content","content":{"type":"image","data":"AAAA","mimeType":"image/png"}}]}`)
	// Asked first, and answered.
	update(`{"sessionUpdate":"tool_call","toolCallId":"t2","title":"rm -rf out","kind":"execute","status":"pending","rawInput":{"command":"rm -rf out"}}`)
	agent(`{"jsonrpc":"2.0","id":0,"method":"session/request_permission","params":{"sessionId":"s","toolCall":{"toolCallId":"t2","title":"rm -rf out"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"},{"optionId":"reject","name":"Reject","kind":"reject_once"}]}}`)
	page(`{"jsonrpc":"2.0","id":0,"result":{"outcome":{"outcome":"selected","optionId":"reject"}}}`)
	update(`{"sessionUpdate":"tool_call_update","toolCallId":"t2","status":"failed","rawOutput":{"error":"` + big + `"}}`)
	update(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done."}}`)
	agent(`{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn"}}`)
	page(`{"jsonrpc":"2.0","method":"session/set_mode","params":{"sessionId":"s","modeId":"default"}}`)
	page(`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"s","prompt":[{"type":"text","text":"again"}]}}`)
	update(`{"sessionUpdate":"tool_call","toolCallId":"t3","title":"click","status":"in_progress"}`)
	agent(`{"jsonrpc":"2.0","id":5,"method":"session/request_permission","params":{"toolCall":{"title":"type"},"options":[]}}`)
	page(`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s"}}`)
	rec.End(nil)
	// Nothing after the end.
	page(`{"jsonrpc":"2.0","id":6,"method":"session/prompt","params":{"prompt":[{"type":"text","text":"late"}]}}`)

	recs := records(t, path)
	want := "session.start prompt tool permission tool turn.end mode prompt cancel tool permission session.end"
	if got := types(recs); got != want {
		t.Fatalf("records: %s\nwant:    %s", got, want)
	}
	for _, r := range recs {
		if r.Sandbox != "sbx_1" || r.Name != "os" || r.Agent != "claude" || r.User != "ada" || r.Org != "lab" || r.Session == "" || r.Time.IsZero() {
			t.Fatalf("record without its session: %+v", r)
		}
		if r.Session != recs[0].Session {
			t.Fatalf("two sessions in one: %q %q", r.Session, recs[0].Session)
		}
	}
	if recs[0].Image != "ghcr.io/x/sandbox-agentos:edge" {
		t.Errorf("start: %+v", recs[0])
	}
	if p := recs[1]; p.Text != "open example.com" || p.Images != 1 {
		t.Errorf("prompt: %+v", p)
	}
	if s := recs[2]; s.ToolID != "t1" || s.Title != "screenshot" || s.Status != "completed" || s.Input != `{"display":":0"}` || s.Output != "saved" || s.Images != 1 || s.Started == nil || !s.Started.Before(s.Time) {
		t.Errorf("tool t1: %+v", s)
	}
	if q := recs[3]; q.Title != "rm -rf out" || q.Status != "selected" || q.Option != "Reject" || q.OptionKind != "reject_once" {
		t.Errorf("permission: %+v", q)
	}
	if f := recs[4]; f.ToolID != "t2" || f.Status != "failed" || f.Input != `{"command":"rm -rf out"}` || !f.Truncated || len(f.Output) > agentLogField || !utf8.ValidString(f.Output) {
		t.Errorf("tool t2: status %q truncated %v, output %d bytes", f.Status, f.Truncated, len(f.Output))
	}
	if e := recs[5]; e.StopReason != "end_turn" || e.Text != "Opening done." {
		t.Errorf("turn.end: %+v", e)
	}
	if m := recs[6]; m.Mode != "default" {
		t.Errorf("mode: %+v", m)
	}
	if u := recs[9]; u.ToolID != "t3" || u.Status != "unfinished" {
		t.Errorf("open at the end: %+v", u)
	}
	if u := recs[10]; u.Title != "type" || u.Status != "unanswered" {
		t.Errorf("unanswered at the end: %+v", u)
	}
	if end := recs[11]; end.Code != nil || end.Status != "closed" {
		t.Errorf("end: %+v", end)
	}
}

// A session holds at most agentLogOpen tool calls open; one past that is
// written as it stands rather than held, so an agent cannot grow Studio's
// memory without bound.
func TestACPRecorderBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-log.jsonl")
	rec := newACPRecorder(audit.NewLog(path), AgentRecord{Sandbox: "sbx_1", Agent: "claude"})
	for i := 0; i < agentLogOpen+3; i++ {
		rec.Agent([]byte(`{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"t` + strconv.Itoa(i) + `","title":"x","status":"in_progress"}}}`))
	}
	if n := len(records(t, path)); n != 3 {
		t.Fatalf("written before the end: %d, want 3", n)
	}
	rec.mu.Lock()
	open := len(rec.tools)
	rec.mu.Unlock()
	if open != agentLogOpen {
		t.Fatalf("held open: %d", open)
	}
	code := 0
	rec.End(&code)
	recs := records(t, path)
	if len(recs) != agentLogOpen+3+1 || recs[len(recs)-1].Code == nil || *recs[len(recs)-1].Code != 0 {
		t.Fatalf("at the end: %d records, last %+v", len(recs), recs[len(recs)-1])
	}
}

// A session that writes its budget's worth stops being recorded, says so
// once, and still records its end with how much it dropped: one agent cannot
// rotate the shared log's other sandboxes away.
func TestACPRecorderBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-log.jsonl")
	rec := newACPRecorder(audit.NewLog(path), AgentRecord{Sandbox: "sbx_1", Agent: "claude"})
	rec.budget = 2000
	for i := 0; i < 100; i++ {
		rec.Agent([]byte(`{"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"t` + strconv.Itoa(i) + `","title":"` + strings.Repeat("x", 100) + `","status":"completed"}}}`))
	}
	rec.End(nil)
	recs := records(t, path)
	last := recs[len(recs)-1]
	if recs[len(recs)-2].Type != "limit" || last.Type != "session.end" || last.Dropped == 0 || len(recs)-2+last.Dropped != 100 {
		t.Fatalf("%d records: %s, dropped %d", len(recs), types(recs[len(recs)-2:]), last.Dropped)
	}
	var size int
	audit.Lines(path, func(line []byte) { size += len(line) + 1 })
	if size > rec.budget+agentLogField*2 {
		t.Fatalf("wrote %d bytes on a budget of %d", size, rec.budget)
	}
}

// Over the bridge: a session is recorded under the sandbox's id, and
// GET /api/agent-log returns that sandbox's records and no other's.
func TestAgentLogOverTheBridge(t *testing.T) {
	node := &acpNode{exited: make(chan struct{})}
	sd := httptest.NewServer(node.handler(t))
	t.Cleanup(sd.Close)
	c, err := api.NewClient(sd.URL, "sandboxd-token")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agent-log.jsonl")
	log := audit.NewLog(path)
	// Another sandbox's record, which a read of "os" must not return.
	log.Append(AgentRecord{Type: "prompt", Sandbox: "sbx_other", Session: "x", Agent: "claude", Text: "not yours"})
	st := httptest.NewServer((&Server{Client: c, Context: "t", Token: testToken, AgentLog: log}).Handler())
	t.Cleanup(st.Close)
	addr := strings.TrimPrefix(st.URL, "http://")

	conn, resp, br := dialWS(t, addr, "/api/ws/acp?sandbox=os&agent=claude&token="+testToken)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake: %d", resp.StatusCode)
	}
	sendMaskedFrame(t, conn, wsOpText, []byte(`{"type":"rpc","message":{"id":2,"method":"session/prompt","params":{"prompt":[{"type":"text","text":"hello"}]}}}`))
	for {
		f := readJSONFrame(t, br)
		if string(f["type"]) == `"rpc"` {
			break
		}
	}
	conn.Close()
	select {
	case <-node.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent did not end")
	}

	// The handler writes its last record as it returns, after the agent ends.
	var got AgentLogResponse
	deadline := time.Now().Add(5 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, st.URL+"/api/agent-log?sandbox=os", nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got = AgentLogResponse{}
		if err := json.NewDecoder(res.Body).Decode(&got); err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/agent-log: %d %v", res.StatusCode, err)
		}
		res.Body.Close()
		if n := len(got.Records); n > 0 && got.Records[n-1].Type == "session.end" || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !got.Enabled || types(got.Records) != "session.start prompt turn.end session.end" {
		t.Fatalf("records: %+v", got)
	}
	for _, r := range got.Records {
		if r.Sandbox != "sbx_os" || r.Name != "os" {
			t.Fatalf("recorded under %q (%q)", r.Sandbox, r.Name)
		}
	}
	if got.Records[0].Image != "ghcr.io/x/sandbox-agentos:edge" || got.Records[1].Text != "hello" {
		t.Errorf("records: %+v", got.Records)
	}

	// A sandbox the caller cannot look up has no record to read.
	req, _ := http.NewRequest(http.MethodGet, st.URL+"/api/agent-log?sandbox=nope", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown sandbox: %d", res.StatusCode)
	}
}

// Without a log, the read says so and the bridge does not look the sandbox up.
func TestAgentLogOff(t *testing.T) {
	_, addr := agentosUnderTest(t)
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/agent-log?sandbox=os", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got AgentLogResponse
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil || got.Enabled || got.Records == nil || len(got.Records) != 0 {
		t.Fatalf("%d %+v %v", res.StatusCode, got, err)
	}
}

func TestClip(t *testing.T) {
	s, cut := clip(strings.Repeat("a", agentLogField-1) + "é")
	if !cut || s != strings.Repeat("a", agentLogField-1) {
		t.Fatalf("cut %v, %d bytes", cut, len(s))
	}
	if s, cut := clip("short"); cut || s != "short" {
		t.Fatalf("%q %v", s, cut)
	}
}
