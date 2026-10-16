package studio

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
)

const testManifest = `{
  "contract": 1,
  "agents": [{"id": "claude", "name": "Claude Code", "acp": ["agentos-acp"],
              "login": "claude", "defaultMode": "bypassPermissions"}],
  "desktop": {"vncPort": 5900, "start": ["sandbox-desktop"]},
  "outputDir": "~/agentos-output"
}`

// acpNode stands in for sandboxd: sandbox "os" has testManifest, "plain" has
// no manifest, "bad" has one of a contract Studio does not speak. A process
// started in it is a fake ACP agent: for each line on its stdin it writes a
// JSON-RPC result with the line's id, and before that a log line to stderr
// and a line that is not JSON to stdout; when its input ends it exits 0.
type acpNode struct {
	mu      sync.Mutex
	started [][]string
	cwd     string
	lines   []string // what the agent read, one per line
	exited  chan struct{}
	signals []string
}

func (n *acpNode) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sandboxd-token" {
			http.Error(w, "token", http.StatusUnauthorized)
			return
		}
		notFound := func() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"no such file"}}`)
		}
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/files"):
			switch {
			case r.URL.Query().Get("path") != AgentOSManifest:
				notFound()
			case strings.Contains(p, "/os/"):
				_, _ = io.WriteString(w, testManifest)
			case strings.Contains(p, "/bad/"):
				_, _ = io.WriteString(w, strings.Replace(testManifest, `"contract": 1`, `"contract": 2`, 1))
			default:
				notFound()
			}
		case strings.HasSuffix(p, "/processes") && r.Method == http.MethodPost:
			var req api.RunRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			n.mu.Lock()
			n.started = append(n.started, req.Argv)
			n.cwd = req.Cwd
			n.mu.Unlock()
			_ = json.NewEncoder(w).Encode(api.Process{PID: 7, Argv: req.Argv, State: api.ProcessRunning})
		case strings.HasSuffix(p, "/processes/7"):
			state := api.ProcessRunning
			select {
			case <-n.exited:
				state = api.ProcessExited
			default:
			}
			_ = json.NewEncoder(w).Encode(api.Process{PID: 7, State: state})
		case strings.HasSuffix(p, "/processes/7/signal"):
			var req api.SignalRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			n.mu.Lock()
			n.signals = append(n.signals, req.Signal)
			n.mu.Unlock()
		case strings.HasSuffix(p, "/processes/7/attach"):
			conn, brw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + api.StreamProtocol + "\r\n\r\n")
			_ = brw.Flush()
			n.agent(brw.Reader, conn)
		default:
			http.Error(w, "unexpected "+r.Method+" "+p, http.StatusTeapot)
		}
	})
}

func (n *acpNode) agent(r io.Reader, w io.Writer) {
	var in bytes.Buffer
	for {
		typ, payload, err := guestproto.ReadFrame(r)
		if err != nil {
			return
		}
		switch typ {
		case guestproto.FrameStdin:
			in.Write(payload)
			for {
				line, err := in.ReadString('\n')
				if err != nil {
					in.WriteString(line)
					break
				}
				n.mu.Lock()
				n.lines = append(n.lines, line)
				n.mu.Unlock()
				var msg struct {
					ID json.RawMessage `json:"id"`
				}
				_ = json.Unmarshal([]byte(line), &msg)
				_ = guestproto.WriteFrame(w, guestproto.FrameStderr, []byte("agent: working\n"))
				_ = guestproto.WriteFrame(w, guestproto.FrameStdout, []byte("not json\n"))
				// One message split across two frames, as a long one is.
				reply := `{"jsonrpc":"2.0","id":` + string(msg.ID) + `,"result":{"ok":true}}` + "\n"
				_ = guestproto.WriteFrame(w, guestproto.FrameStdout, []byte(reply[:10]))
				_ = guestproto.WriteFrame(w, guestproto.FrameStdout, []byte(reply[10:]))
			}
		case guestproto.FrameStdinEOF:
			var code [4]byte
			binary.BigEndian.PutUint32(code[:], 0)
			_ = guestproto.WriteFrame(w, guestproto.FrameExit, code[:])
			close(n.exited)
			return
		}
	}
}

func agentosUnderTest(t *testing.T) (*acpNode, string) {
	t.Helper()
	node := &acpNode{exited: make(chan struct{})}
	sd := httptest.NewServer(node.handler(t))
	t.Cleanup(sd.Close)
	c, err := api.NewClient(sd.URL, "sandboxd-token")
	if err != nil {
		t.Fatal(err)
	}
	st := httptest.NewServer((&Server{Client: c, Context: "t", Token: testToken}).Handler())
	t.Cleanup(st.Close)
	return node, strings.TrimPrefix(st.URL, "http://")
}

// readJSONFrame reads server frames until a text one, and decodes it.
func readJSONFrame(t *testing.T, br *bufio.Reader) map[string]json.RawMessage {
	t.Helper()
	for {
		op, payload := readServerFrame(t, br)
		if op != wsOpText {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(payload, &m); err != nil {
			t.Fatalf("frame %q: %v", payload, err)
		}
		return m
	}
}

// GET /api/agentos gives the manifest of a sandbox that has one, 404 for one
// that does not, and 422 for one of a contract Studio does not speak.
func TestAgentOSManifest(t *testing.T) {
	_, addr := agentosUnderTest(t)
	get := func(sandbox string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/agentos?sandbox="+sandbox, nil)
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, body
	}

	resp, body := get("os")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("os: %d %s", resp.StatusCode, body)
	}
	var m AgentOS
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Agents) != 1 || m.Agents[0].ID != "claude" || m.Agents[0].ACP[0] != "agentos-acp" || m.Desktop.VNCPort != 5900 {
		t.Fatalf("manifest %+v", m)
	}
	if resp, body := get("plain"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("plain: %d %s; want 404", resp.StatusCode, body)
	}
	if resp, body := get("bad"); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "contract 2") {
		t.Fatalf("bad: %d %s; want 422 naming the contract", resp.StatusCode, body)
	}
	if resp, _ := get(""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no sandbox: %d", resp.StatusCode)
	}
}

// The manifest comes from the guest, so every field Studio uses is checked.
func TestParseAgentOSRefuses(t *testing.T) {
	for name, edit := range map[string][2]string{
		"no agents":       {`"agents": [{`, `"agents": [], "x": [{`},
		"an id with caps": {`"id": "claude"`, `"id": "Claude"`},
		"an empty acp":    {`"acp": ["agentos-acp"]`, `"acp": []`},
		"a newline":       {`"acp": ["agentos-acp"]`, `"acp": ["agentos-acp\n"]`},
		"a mode":          {`"defaultMode": "bypassPermissions"`, `"defaultMode": "<b>"`},
		"a port":          {`"vncPort": 5900`, `"vncPort": 22`},
		"not JSON":        {`{`, `[`},
	} {
		if _, err := parseAgentOS([]byte(strings.Replace(testManifest, edit[0], edit[1], 1))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := parseAgentOS([]byte(testManifest)); err != nil {
		t.Fatalf("the image's own manifest: %v", err)
	}
}

// The ACP bridge starts the manifest's command for the agent in HOME, writes
// each rpc frame to it as one line, hands back each line of its stdout as an
// rpc frame (joined across stream frames) and its stderr and non-protocol
// output as log frames, and ends the session when the browser goes.
func TestACPBridge(t *testing.T) {
	node, addr := agentosUnderTest(t)

	if _, resp, _ := dialWS(t, addr, "/api/ws/acp?sandbox=os&agent=claude"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("without the token: %d", resp.StatusCode)
	}

	conn, resp, br := dialWS(t, addr, "/api/ws/acp?sandbox=os&agent=claude&token="+testToken)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake: %d", resp.StatusCode)
	}
	// Pretty-printed, as a browser may send it: the agent must see one line.
	sendMaskedFrame(t, conn, wsOpText, []byte("{\"type\":\"rpc\",\"message\":{\n \"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}}"))
	// Not an rpc frame, and an rpc frame that is not an object: dropped.
	sendMaskedFrame(t, conn, wsOpText, []byte(`{"type":"input","data":"x"}`))
	sendMaskedFrame(t, conn, wsOpText, []byte(`{"type":"rpc","message":[1]}`))

	var got []string
	for len(got) < 3 {
		f := readJSONFrame(t, br)
		var typ string
		_ = json.Unmarshal(f["type"], &typ)
		switch typ {
		case "log":
			var d string
			_ = json.Unmarshal(f["data"], &d)
			got = append(got, "log "+strings.TrimSpace(d))
		case "rpc":
			got = append(got, "rpc "+string(f["message"]))
		default:
			t.Fatalf("frame %v", f)
		}
	}
	want := []string{"log agent: working", "log not json", `rpc {"jsonrpc":"2.0","id":1,"result":{"ok":true}}`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("frames:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	node.mu.Lock()
	started, cwd, lines := node.started, node.cwd, append([]string(nil), node.lines...)
	node.mu.Unlock()
	if len(started) != 1 || strings.Join(started[0], " ") != "agentos-acp" || cwd != "/sandbox/home" {
		t.Fatalf("started %v in %q", started, cwd)
	}
	if len(lines) != 1 || lines[0] != `{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n" {
		t.Fatalf("the agent read %q", lines)
	}

	conn.Close()
	select {
	case <-node.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent's input did not end when the browser left")
	}
}

// An agent the manifest does not name, or a sandbox without a manifest, is an
// HTTP error before any upgrade, and nothing is started.
func TestACPBridgeRefusals(t *testing.T) {
	node, addr := agentosUnderTest(t)
	for path, want := range map[string]int{
		"/api/ws/acp?sandbox=os&agent=bash":      http.StatusNotFound,
		"/api/ws/acp?sandbox=plain&agent=claude": http.StatusNotFound,
		"/api/ws/acp?sandbox=bad&agent=claude":   http.StatusBadGateway,
		"/api/ws/acp?sandbox=os":                 http.StatusBadRequest,
	} {
		if _, resp, _ := dialWS(t, addr, path+"&token="+testToken); resp.StatusCode != want {
			t.Errorf("%s: %d; want %d", path, resp.StatusCode, want)
		}
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if len(node.started) != 0 {
		t.Fatalf("started %v", node.started)
	}
}

// A line split at any point is joined; one past the limit fails once and
// what follows is dropped.
func TestACPLines(t *testing.T) {
	var sent []string
	var failed []error
	l := &acpLines{
		send: func(b []byte) error { sent = append(sent, string(b)); return nil },
		fail: func(err error) { failed = append(failed, err) },
	}
	for _, p := range []string{`{"a"`, ":1}\n\n", "{\"b\":2}\n{\"c\"", ":3}"} {
		_, _ = l.Write([]byte(p))
	}
	l.flush()
	if strings.Join(sent, "|") != `{"a":1}|{"b":2}|{"c":3}` {
		t.Fatalf("sent %q", sent)
	}
	sent = nil
	_, _ = l.Write(bytes.Repeat([]byte("x"), acpMaxLine+1))
	_, _ = l.Write([]byte("\n{\"d\":4}\n"))
	if len(failed) != 1 || len(sent) != 0 {
		t.Fatalf("failed %v, sent %q", failed, sent)
	}
}
