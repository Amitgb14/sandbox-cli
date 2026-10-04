package gateway

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Limits on what a session's requests may carry. Each is far above what a
// real client sends; they exist so a client cannot make the gateway hold or
// forward arbitrary amounts of its text.
const (
	maxTermLen  = 64
	maxEnvVars  = 32
	maxEnvValue = 1024
)

// sessionEnvAllowed is which `env` requests are honoured: the locale and the
// terminal type, as OpenSSH's default AcceptEnv does, and nothing else. A
// variable such as LD_PRELOAD, PATH or BASH_ENV is an instruction to the
// guest's loader or shell, not a setting (AGENTS.md rule 5); the rest is
// ignored rather than refused, because every client sends what its user's
// SendEnv says and a session should not fail over it.
func sessionEnvAllowed(name string) bool {
	if name == "TERM" || name == "LANG" {
		return true
	}
	rest, ok := strings.CutPrefix(name, "LC_")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if (r < 'A' || r > 'Z') && r != '_' {
			return false
		}
	}
	return true
}

// cleanEnvValue accepts a value only if it is short and printable: a locale
// or a terminal name has no need of control characters.
func cleanEnvValue(v string) bool {
	if len(v) > maxEnvValue {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// cleanTerm accepts a terminal type: letters, digits and . _ + -.
func cleanTerm(t string) bool {
	if t == "" || len(t) > maxTermLen {
		return false
	}
	for _, r := range t {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '.' || r == '_' || r == '+' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// clampDim turns an SSH terminal dimension (uint32) into the API's uint16.
func clampDim(v uint32) uint16 { return uint16(min(v, 0xffff)) }

// session is one "session" channel: at most one shell, exec or subsystem,
// which becomes one process in the sandbox.
type session struct {
	s     *SSHServer
	ctx   context.Context
	login *sshLogin
	ch    ssh.Channel

	pty        bool
	term       string
	rows, cols uint16
	env        map[string]string

	mu      sync.Mutex
	stream  *api.Stream
	tty     bool
	started bool
	done    chan struct{} // closed when the process has exited and the channel is finished
}

func (s *SSHServer) session(ctx context.Context, login *sshLogin, nch ssh.NewChannel) {
	ch, reqs, err := nch.Accept()
	if err != nil {
		return
	}
	ss := &session{s: s, ctx: ctx, login: login, ch: ch, env: map[string]string{}, done: make(chan struct{})}
	defer ch.Close()
	for r := range reqs {
		ok := ss.request(r)
		if r.WantReply {
			_ = r.Reply(ok, nil)
		}
	}
	// The channel is gone: the client closed it or the connection ended.
	ss.hangup()
}

// request handles one channel request and says whether it succeeded.
func (ss *session) request(r *ssh.Request) bool {
	switch r.Type {
	case "pty-req":
		var m struct {
			Term          string
			Cols, Rows    uint32
			Width, Height uint32
			Modes         string
		}
		if ss.isStarted() || ssh.Unmarshal(r.Payload, &m) != nil {
			return false
		}
		ss.pty = true
		ss.rows, ss.cols = clampDim(m.Rows), clampDim(m.Cols)
		if cleanTerm(m.Term) {
			ss.term = m.Term
		}
		return true
	case "env":
		var m struct{ Name, Value string }
		if ss.isStarted() || ssh.Unmarshal(r.Payload, &m) != nil {
			return false
		}
		if !sessionEnvAllowed(m.Name) || !cleanEnvValue(m.Value) {
			return false
		}
		if _, have := ss.env[m.Name]; !have && len(ss.env) >= maxEnvVars {
			return false
		}
		ss.env[m.Name] = m.Value
		return true
	case "window-change":
		var m struct {
			Cols, Rows    uint32
			Width, Height uint32
		}
		if ssh.Unmarshal(r.Payload, &m) != nil {
			return false
		}
		rows, cols := clampDim(m.Rows), clampDim(m.Cols)
		ss.mu.Lock()
		st, tty := ss.stream, ss.tty
		ss.rows, ss.cols = rows, cols
		ss.mu.Unlock()
		if st != nil && tty {
			return st.Resize(rows, cols) == nil
		}
		return true
	case "signal":
		var m struct{ Signal string }
		if ssh.Unmarshal(r.Payload, &m) != nil || !slices.Contains(api.Signals, m.Signal) {
			return false
		}
		ss.mu.Lock()
		st := ss.stream
		ss.mu.Unlock()
		return st != nil && st.Signal(m.Signal) == nil
	case "shell":
		return ss.start("shell", ss.s.cfg.ShellArgv(), ss.pty)
	case "exec":
		var m struct{ Command string }
		if ssh.Unmarshal(r.Payload, &m) != nil {
			return false
		}
		return ss.start("exec", ss.s.cfg.ExecArgv(m.Command), ss.pty)
	case "subsystem":
		var m struct{ Name string }
		if ssh.Unmarshal(r.Payload, &m) != nil || m.Name != "sftp" {
			return false
		}
		// sftp is a binary protocol: a terminal would mangle it.
		return ss.start("sftp", ss.s.cfg.SFTPArgv(), false)
	default:
		// x11-req, auth-agent-req@openssh.com and the rest. Agent forwarding
		// in particular would hand the guest a signer for the user's keys.
		return false
	}
}

func (ss *session) isStarted() bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.started
}

// start runs argv in the sandbox and wires the channel to it, as
// `sandbox-cli shell` does: in the sandbox user's home, on a terminal of the
// client's size when the client asked for one, with TERM set for it.
func (ss *session) start(kind string, argv []string, tty bool) bool {
	ss.mu.Lock()
	if ss.started {
		ss.mu.Unlock()
		return false
	}
	ss.started = true
	ss.mu.Unlock()
	if len(argv) == 0 {
		return false
	}

	req := api.RunRequest{Argv: argv, Cwd: agenthome.GuestHome, Tty: tty}
	env := map[string]string{}
	for k, v := range ss.env {
		if k != "TERM" {
			env[k] = v
		}
	}
	if tty {
		// The guest's programs draw for the terminal they are told they
		// have. The pty request's type wins over an env request's.
		term := ss.term
		if term == "" {
			term = ss.env["TERM"]
		}
		if term == "" || term == "dumb" {
			term = "xterm-256color"
		}
		env["TERM"] = term
		ss.mu.Lock()
		req.Rows, req.Cols = ss.rows, ss.cols
		ss.mu.Unlock()
	}
	if len(env) > 0 {
		req.Env = env
	}

	c, id := ss.login.client, ss.login.id
	p, err := c.StartProcess(ss.ctx, id, req)
	if err != nil {
		ss.s.cfg.Logf("ssh: %s in %s for %s failed: %v", kind, id, ss.login.describe(), err)
		return false
	}
	st, err := c.Attach(ss.ctx, id, p.PID)
	if err != nil {
		ss.s.cfg.Logf("ssh: attaching to %s process %d failed: %v", id, p.PID, err)
		// Nobody will ever read from or hang up this process otherwise.
		_ = c.Signal(context.Background(), id, p.PID, "KILL")
		return false
	}
	ss.mu.Lock()
	ss.stream, ss.tty = st, tty
	ss.mu.Unlock()
	// The command line is not logged: it is the user's, and may carry a
	// secret typed on it.
	ss.s.cfg.Logf("ssh: %s started in %s, process %d, for %s", kind, id, p.PID, ss.login.describe())

	go func() {
		_, _ = io.Copy(st, ss.ch)
		// Without a terminal, the client's EOF is the process's: a piped
		// `ssh box cat < file` must end. On a terminal, as in `sandbox-cli
		// shell`, the shell decides when it is done.
		if !tty {
			_ = st.CloseStdin()
		}
	}()
	go ss.finish(st)
	return true
}

// finish copies the process's output to the channel until it exits, then
// reports its status and closes the channel, as sshd does: EOF, exit-status,
// close.
func (ss *session) finish(st *api.Stream) {
	code, err := st.Copy(ss.ch, ss.ch.Stderr())
	st.Close()
	close(ss.done)
	if err != nil {
		// The stream ended without an exit: the node or the connection to it
		// went away. No exit-status, so the client reports the session as
		// ended abnormally rather than as a command that exited 255.
		_, _ = io.WriteString(ss.ch.Stderr(), "\r\nsandbox-gateway: the connection to the sandbox was lost\r\n")
		_ = ss.ch.Close()
		return
	}
	_ = ss.ch.CloseWrite()
	status := uint32(255)
	if code >= 0 && code <= 255 {
		status = uint32(code)
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], status)
	_, _ = ss.ch.SendRequest("exit-status", false, b[:])
	_ = ss.ch.Close()
}

// hangup is the channel going away while its process may still run. sshd
// sends the session SIGHUP when its client disconnects; so does the gateway,
// or every dropped connection would leave a shell running in the sandbox
// with nobody attached.
func (ss *session) hangup() {
	ss.mu.Lock()
	st := ss.stream
	ss.mu.Unlock()
	if st == nil {
		return
	}
	select {
	case <-ss.done:
		return
	default:
	}
	_ = st.Signal("HUP")
	st.Close()
}

// directTCPIP is `ssh -L`: a TCP connection to a port on the sandbox's own
// loopback, and nowhere else. The gateway is not a proxy onto its own network
// or the node's; the tunnel the node opens is to the guest's loopback only,
// and the host named must say so too, so a client asking for anything else
// learns it is refused rather than silently getting the guest.
func (s *SSHServer) directTCPIP(ctx context.Context, login *sshLogin, nch ssh.NewChannel) {
	var m struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if ssh.Unmarshal(nch.ExtraData(), &m) != nil {
		_ = nch.Reject(ssh.ConnectionFailed, "malformed direct-tcpip request")
		return
	}
	switch strings.ToLower(m.Host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
	default:
		_ = nch.Reject(ssh.Prohibited, "only ports on the sandbox's own loopback (localhost) can be forwarded")
		return
	}
	if m.Port < 1 || m.Port > 65535 {
		_ = nch.Reject(ssh.ConnectionFailed, "port out of range")
		return
	}
	tun, err := login.client.Tunnel(ctx, login.id, int(m.Port))
	if err != nil {
		s.cfg.Logf("ssh: tunnel to %s port %d for %s failed: %v", login.id, m.Port, login.describe(), err)
		msg := "the sandbox refused the connection"
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.CodeUnsupported {
			msg = "this sandbox's node cannot open tunnels"
		}
		_ = nch.Reject(ssh.ConnectionFailed, msg)
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		tun.Close()
		return
	}
	s.cfg.Logf("ssh: tunnel to %s port %d for %s", login.id, m.Port, login.describe())
	go func() {
		// No requests are defined on a forward; this ends when the channel
		// does, which must end the tunnel too.
		for r := range reqs {
			if r.WantReply {
				_ = r.Reply(false, nil)
			}
		}
		tun.Close()
	}()
	go func() {
		_, _ = io.Copy(tun, ch)
		// The client finishing only half-closes toward the guest, so a
		// client that sends and then waits for EOF still gets its reply.
		if cw, ok := tun.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	_, _ = io.Copy(ch, tun)
	_ = ch.CloseWrite()
	_ = ch.Close()
	tun.Close()
}
