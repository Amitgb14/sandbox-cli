package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// An agentos image is a desktop image with an agent that works the desktop,
// reached over the Agent Client Protocol (ACP): JSON-RPC, one message a line,
// on a process's stdin and stdout. The image says what it offers in a
// manifest, and Studio shows its Agent tab only for a sandbox that has one —
// never by the image's name, which forks, mirrors and local builds change.
const (
	// AgentOSManifest is where an agentos image keeps its manifest.
	AgentOSManifest = "/etc/agentos/manifest.json"
	// AgentOSContract is the manifest contract this Studio speaks. A higher
	// one changed something Studio relies on, and is not offered.
	AgentOSContract = 1

	// agentosManifestMax bounds the manifest, which the guest decides the
	// size of and Studio holds in memory.
	agentosManifestMax = 64 << 10
	// acpMaxLine bounds one ACP message from the agent. Messages carry tool
	// output and, at most, a screenshot; a line longer than this ends the
	// session rather than grow without limit.
	acpMaxLine = 16 << 20
	// acpExitGrace is how long an agent has to exit after its input ends,
	// before it is sent TERM.
	acpExitGrace = 5 * time.Second
	// agentosCwd is where an ACP agent's process starts: the sandbox user's
	// home, which is where sandbox-cli agent restores its login.
	agentosCwd = "/sandbox/home"
)

// AgentOS is GET /api/agentos: a sandbox's manifest, as Studio checked it.
type AgentOS struct {
	Contract  int            `json:"contract"`
	Agents    []AgentOSAgent `json:"agents"`
	Desktop   AgentOSDesktop `json:"desktop"`
	OutputDir string         `json:"outputDir"`
}

// AgentOSAgent is one agent an agentos image offers.
type AgentOSAgent struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	ACP         []string `json:"acp"`
	Login       string   `json:"login"`
	DefaultMode string   `json:"defaultMode"`
}

// AgentOSDesktop is the image's desktop.
type AgentOSDesktop struct {
	VNCPort int      `json:"vncPort"`
	Start   []string `json:"start"`
}

var (
	agentosID   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	agentosMode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)
)

// errNoAgentOS is a sandbox without a manifest Studio can use.
var errNoAgentOS = errors.New("not an agentos sandbox")

// parseAgentOS checks a manifest, which comes from the guest and is
// untrusted: every field Studio uses has a bounded size and shape, so what
// the page shows is plain short text and what the bridge runs is a short
// argv. Running it is no new power — it runs in the same guest that wrote it,
// as anything the token's holder could start there already.
func parseAgentOS(data []byte) (AgentOS, error) {
	var m AgentOS
	if err := json.Unmarshal(data, &m); err != nil {
		return AgentOS{}, fmt.Errorf("%s: %w", AgentOSManifest, err)
	}
	if m.Contract != AgentOSContract {
		return AgentOS{}, fmt.Errorf("%s: contract %d; this Studio speaks %d", AgentOSManifest, m.Contract, AgentOSContract)
	}
	if len(m.Agents) == 0 || len(m.Agents) > 16 {
		return AgentOS{}, fmt.Errorf("%s: 1 to 16 agents", AgentOSManifest)
	}
	seen := map[string]bool{}
	for _, a := range m.Agents {
		switch {
		case !agentosID.MatchString(a.ID) || seen[a.ID]:
			return AgentOS{}, fmt.Errorf("%s: agent id %q: lowercase letters, digits and dashes, and unique", AgentOSManifest, a.ID)
		case a.Name == "" || len(a.Name) > 64:
			return AgentOS{}, fmt.Errorf("%s: agent %s: a name of 1 to 64 bytes", AgentOSManifest, a.ID)
		case !argvOK(a.ACP):
			return AgentOS{}, fmt.Errorf("%s: agent %s: acp: a command of 1 to 16 words", AgentOSManifest, a.ID)
		case a.Login != "" && !agentosID.MatchString(a.Login):
			return AgentOS{}, fmt.Errorf("%s: agent %s: login %q", AgentOSManifest, a.ID, a.Login)
		case a.DefaultMode != "" && !agentosMode.MatchString(a.DefaultMode):
			return AgentOS{}, fmt.Errorf("%s: agent %s: defaultMode %q", AgentOSManifest, a.ID, a.DefaultMode)
		}
		seen[a.ID] = true
	}
	if m.Desktop.VNCPort != 0 && (m.Desktop.VNCPort < DesktopPort || m.Desktop.VNCPort > MaxDesktopPort) {
		return AgentOS{}, fmt.Errorf("%s: desktop.vncPort %d: a VNC display port", AgentOSManifest, m.Desktop.VNCPort)
	}
	if len(m.Desktop.Start) > 0 && !argvOK(m.Desktop.Start) {
		return AgentOS{}, fmt.Errorf("%s: desktop.start: a command of 1 to 16 words", AgentOSManifest)
	}
	if len(m.OutputDir) > 256 {
		return AgentOS{}, fmt.Errorf("%s: outputDir: at most 256 bytes", AgentOSManifest)
	}
	return m, nil
}

// keepACPLogin asks the gateway to copy the user's kept login for the agent
// into the sandbox before the agent starts, as an agent console does. A
// sandbox started some other way than sandbox-cli agent — by a bot, from an
// image — had no login in it, and the tab's first task answered
// "Authentication required" though the gateway held one. The gateway copies
// only the caller's own login, into the caller's own sandbox, and saves it
// back as it is refreshed. The name comes from the guest's manifest, so only
// an agent this side knows to have login files is asked for. Anything that
// fails — a node with no logins, none kept — leaves the agent to ask, as
// before: the login is a convenience, not a control.
func keepACPLogin(ctx context.Context, c *api.Client, ref, login string) {
	if d, ok := agents.Lookup(login); !ok || len(d.AuthPaths) == 0 {
		return
	}
	_, _ = c.KeepAgentLogin(ctx, ref, login)
}

func argvOK(argv []string) bool {
	if len(argv) == 0 || len(argv) > 16 || argv[0] == "" {
		return false
	}
	for _, w := range argv {
		if len(w) > 256 || bytes.ContainsAny([]byte(w), "\x00\n") {
			return false
		}
	}
	return true
}

// readAgentOS reads and checks a sandbox's manifest. A sandbox without one is
// errNoAgentOS.
func readAgentOS(ctx context.Context, c *api.Client, ref string) (AgentOS, error) {
	data, err := c.ReadFileLimit(ctx, ref, AgentOSManifest, agentosManifestMax)
	if e := (*api.Error)(nil); errors.As(err, &e) && e.Status == http.StatusNotFound {
		return AgentOS{}, errNoAgentOS
	}
	if err != nil {
		return AgentOS{}, err
	}
	return parseAgentOS(data)
}

// agentos is GET /api/agentos?sandbox=: the sandbox's manifest, 404 when it
// has none, 422 when it has one Studio cannot use (the reason is the error).
func (s *Server) agentos(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("sandbox")
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "sandbox: required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	m, err := readAgentOS(ctx, s.clientFor(r), ref)
	switch {
	case errors.Is(err, errNoAgentOS):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, api.ErrFileTooLarge):
		writeErr(w, http.StatusUnprocessableEntity, AgentOSManifest+": too large")
	case err != nil && isAPIError(err):
		writeErr(w, http.StatusBadGateway, err.Error())
	case err != nil:
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeJSON(w, http.StatusOK, m)
	}
}

func isAPIError(err error) bool {
	var e *api.Error
	return errors.As(err, &e)
}

// acp bridges a browser WebSocket to an ACP session: a new process running the
// command the sandbox's manifest names for ?agent=, its stdin and stdout
// carrying the protocol. Browser to server, text frames are
// {"type":"rpc","message":{…}}, one JSON-RPC message each, written to the
// agent as one line. Server to browser: {"type":"rpc","message":{…}} for each
// line of the agent's stdout, {"type":"log","data":"…"} for its stderr, and,
// last, {"type":"exit","code":N}.
//
// The terminal bridge (/api/ws/attach) cannot carry this: it joins stdout and
// stderr, and a log line in the middle of the protocol breaks it.
//
// The guest's messages reach the page as data, parsed as JSON and shown as
// text; nothing in them is served as a page or run. Closing the page ends the
// session: the agent's input ends, and it is sent TERM if it has not exited
// within acpExitGrace.
func (s *Server) acp(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		writeErr(w, http.StatusBadRequest, "want a WebSocket upgrade")
		return
	}
	ref, agent := r.URL.Query().Get("sandbox"), r.URL.Query().Get("agent")
	if ref == "" || agent == "" {
		writeErr(w, http.StatusBadRequest, "sandbox and agent: required")
		return
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	c := s.clientFor(r)

	// Everything that can fail does so before the upgrade, as an HTTP error
	// the page can show.
	m, err := readAgentOS(ctx, c, ref)
	if errors.Is(err, errNoAgentOS) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	} else if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var argv []string
	login := ""
	for _, a := range m.Agents {
		if a.ID == agent {
			argv, login = a.ACP, a.Login
		}
	}
	if argv == nil {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("no agent %q in %s", agent, AgentOSManifest))
		return
	}
	keepACPLogin(ctx, c, ref, login)
	proc, err := c.StartProcess(ctx, ref, api.RunRequest{Argv: argv, Cwd: agentosCwd})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	stop := func() {
		// The agent ends its session when its input ends; one that does not
		// is stopped, so a closed page never leaves an agent at work.
		sctx, scancel := context.WithTimeout(context.WithoutCancel(r.Context()), acpExitGrace+10*time.Second)
		defer scancel()
		deadline := time.Now().Add(acpExitGrace)
		for time.Now().Before(deadline) {
			if p, err := c.Process(sctx, ref, proc.PID); err != nil || p.State != api.ProcessRunning {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		_ = c.Signal(sctx, ref, proc.PID, "TERM")
	}
	stream, err := c.Attach(ctx, ref, proc.PID)
	if err != nil {
		_ = c.Signal(ctx, ref, proc.PID, "TERM")
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer stream.Close()
	ws, err := upgradeWebSocket(w, r)
	if err != nil {
		_ = stream.CloseStdin()
		go stop()
		if !errors.Is(err, errUpgradeAborted) {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	defer ws.close(wsCloseNormal, "")
	go ws.keepalive(ctx)
	go ws.readLoop(func(_ byte, payload []byte) {
		line, ok := acpLine(payload)
		if ok {
			_, _ = stream.Write(line)
		}
	}, func() {
		_ = stream.CloseStdin()
		cancel()
		go stop()
		// Detaching ends Copy below, and with it the handler.
		_ = stream.Close()
	})

	var failOnce sync.Once
	stdout := &acpLines{
		send: func(line []byte) error {
			if line[0] == '{' && json.Valid(line) {
				return ws.writeFrame(wsOpText, acpEnvelope(line))
			}
			// Not protocol: an agent that prints to stdout. Shown as a log
			// line, not handed to the page's client as a message.
			return ws.writeJSON(map[string]string{"type": "log", "data": string(line)})
		},
		// Copy does not stop for a writer's error, so a message too long to
		// hold ends the session here: the agent's input ends, and closing
		// the stream ends Copy.
		fail: func(err error) {
			failOnce.Do(func() {
				_ = ws.writeJSON(map[string]string{"type": "log", "data": err.Error()})
				_ = stream.CloseStdin()
				go stop()
				_ = stream.Close()
			})
		},
	}
	stderr := writerFunc(func(p []byte) (int, error) {
		if err := ws.writeJSON(map[string]string{"type": "log", "data": string(p)}); err != nil {
			return 0, err
		}
		return len(p), nil
	})
	if code, err := stream.Copy(stdout, stderr); err == nil {
		stdout.flush()
		_ = ws.writeJSON(map[string]any{"type": "exit", "code": code})
	}
}

// acpLine turns a browser frame into a line for the agent: the message of an
// {"type":"rpc"} frame, which must be a JSON object, compacted so it is one
// line. Anything else is dropped.
func acpLine(payload []byte) ([]byte, bool) {
	var f struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(payload, &f) != nil || f.Type != "rpc" {
		return nil, false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(f.Message, &obj) != nil {
		return nil, false
	}
	var b bytes.Buffer
	if json.Compact(&b, f.Message) != nil {
		return nil, false
	}
	b.WriteByte('\n')
	return b.Bytes(), true
}

// acpEnvelope wraps one message from the agent, already checked to be JSON.
func acpEnvelope(message []byte) []byte {
	b := make([]byte, 0, len(message)+32)
	b = append(b, `{"type":"rpc","message":`...)
	b = append(b, message...)
	return append(b, '}')
}

var errACPLineTooLong = fmt.Errorf("the agent sent a message over %d bytes; the session is ended", acpMaxLine)

// acpLines splits the agent's stdout into lines, each handed to send without
// its newline; blank lines are skipped. A line that grows past acpMaxLine is
// handed to fail, once, and everything after it is dropped.
type acpLines struct {
	buf    []byte
	failed bool
	send   func(line []byte) error
	fail   func(error)
}

func (l *acpLines) Write(p []byte) (int, error) {
	if l.failed {
		return len(p), nil
	}
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(l.buf[:i])
		l.buf = l.buf[i+1:]
		if len(line) > 0 {
			_ = l.send(line)
		}
	}
	if len(l.buf) > acpMaxLine {
		l.failed, l.buf = true, nil
		l.fail(errACPLineTooLong)
	}
	if len(l.buf) == 0 {
		l.buf = nil // do not hold on to the array of a long line just sent
	}
	return len(p), nil
}

// flush sends what is left after the agent's last newline.
func (l *acpLines) flush() {
	if line := bytes.TrimSpace(l.buf); len(line) > 0 && !l.failed {
		_ = l.send(line)
	}
	l.buf = nil
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
