package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// SSHConfig configures the gateway's SSH server: one port for every sandbox.
type SSHConfig struct {
	Store  Store
	Router Router
	// HostKeyFile holds the server's ed25519 host key; it is created, 0600,
	// when missing. One key for the whole fleet, published by GET /v1/ssh so
	// clients can pin it.
	HostKeyFile string
	// Host and Port are what clients are told to connect to (GET /v1/ssh,
	// ssh-access), which behind a load balancer is not the listen address.
	Host string
	Port int
	Logf func(format string, args ...any)

	// MaxConns caps open connections, authenticated or not (default 256). A
	// connection over the cap is closed before the handshake.
	MaxConns int
	// MaxAuthTries caps authentication attempts per connection (default 6).
	MaxAuthTries int
	// HandshakeTimeout bounds the handshake and authentication together
	// (default 30s), so a client that connects and says nothing does not
	// hold a slot under MaxConns.
	HandshakeTimeout time.Duration
	// MaxChannels caps the channels (sessions and forwards) one connection
	// may hold open at once (default 16).
	MaxChannels int

	// ShellArgv, ExecArgv and SFTPArgv build what a session runs in the
	// sandbox. They are fields so tests can substitute commands the fake
	// backend knows; nil means the defaults (DefaultShellArgv, and so on).
	ShellArgv func() []string
	ExecArgv  func(command string) []string
	SFTPArgv  func() []string

	// Now is the clock tokens are checked against; nil is time.Now.
	Now func() time.Time

	// RecheckInterval is how often every open connection is held to the
	// login check again (default 30s; RecheckAccess). The gateway asks for
	// a recheck itself whenever it revokes a key or removes an SSH key; this
	// is the backstop for a store changed some other way.
	RecheckInterval time.Duration

	// Audit records every login and session (Gateway.AuditLog); Metrics
	// counts them (Gateway.SSHMetrics). Either may be nil.
	Audit   *AuditLog
	Metrics *SSHMetrics
}

// DefaultShellArgv is the guest's best interactive shell, the same one
// `sandbox-cli shell` opens: bash when the image has it, sh otherwise, as a
// login shell so the image's profile applies.
func DefaultShellArgv() []string {
	return []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi"}
}

// DefaultExecArgv runs an exec request's command line the way sshd does:
// through the shell, which is what every scp, rsync and git-over-ssh client
// expects to parse it.
func DefaultExecArgv(command string) []string { return []string{"/bin/sh", "-c", command} }

// DefaultSFTPArgv runs the guest's sftp-server, wherever its distribution put
// it: on PATH, or Debian's libexec-like location, which is where the base
// image's openssh-sftp-server installs it. scp uses sftp by default since
// OpenSSH 9.0, so this one subsystem serves both.
func DefaultSFTPArgv() []string {
	return []string{"/bin/sh", "-c", `exec "$(command -v sftp-server || echo /usr/lib/openssh/sftp-server)"`}
}

// publicHostRE is a host clients may be told to connect to. The CLI writes
// it into known_hosts and ssh's argv, and refuses anything else (cli's
// sshHostRE); a gateway that published one would serve SSH nobody can use.
var publicHostRE = regexp.MustCompile(`^[A-Za-z0-9_.:][A-Za-z0-9_.:-]*$`)

// checkPublicAddr refuses a Host and Port that GET /v1/ssh and ssh-access
// could not usefully hand out: empty, a wildcard address, not a host name,
// or no fixed port.
func checkPublicAddr(host string, port int) error {
	if host == "" {
		return errors.New("ssh: no public host for clients to connect to")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return fmt.Errorf("ssh: public host %s is a wildcard address clients cannot connect to", host)
	}
	if !publicHostRE.MatchString(host) {
		return fmt.Errorf("ssh: public host %q is not a host name or address", host)
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("ssh: public port %d is not a port clients can connect to", port)
	}
	return nil
}

// sshScopes is what an SSH login may do: run things in the sandbox and read
// it. The Router still checks that the user owns the sandbox.
var sshScopes = []string{ScopeSSH, ScopeRead}

// SSHServer is the gateway's SSH server. There is no sshd in any guest: the
// gateway terminates SSH, and a session becomes a process (or a tunnel) on the
// node that holds the sandbox, through the same Router and API every other
// client uses. A login names its sandbox in the SSH username, so one port and
// one host key serve the whole fleet.
type SSHServer struct {
	cfg    SSHConfig
	signer ssh.Signer
	slots  chan struct{}
	mu     sync.Mutex
	closed bool
	lns    map[net.Listener]bool
	conns  map[net.Conn]bool
	// live is every connection that has logged in, with what it logged in
	// as, for RecheckAccess. Under mu.
	live    map[*ssh.ServerConn]*sshLogin
	recheck sync.Once
	wg      sync.WaitGroup
	baseCtx context.Context
	cancel  context.CancelFunc
}

// NewSSHServer loads or creates the host key and returns a server ready to
// Serve.
func NewSSHServer(cfg SSHConfig) (*SSHServer, error) {
	if cfg.Store == nil || cfg.Router == nil {
		return nil, errors.New("ssh: a store and a router are required")
	}
	if err := checkPublicAddr(cfg.Host, cfg.Port); err != nil {
		return nil, err
	}
	signer, err := loadHostKey(cfg.HostKeyFile)
	if err != nil {
		return nil, err
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 256
	}
	if cfg.MaxAuthTries <= 0 {
		cfg.MaxAuthTries = 6
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 30 * time.Second
	}
	if cfg.MaxChannels <= 0 {
		cfg.MaxChannels = 16
	}
	if cfg.ShellArgv == nil {
		cfg.ShellArgv = DefaultShellArgv
	}
	if cfg.ExecArgv == nil {
		cfg.ExecArgv = DefaultExecArgv
	}
	if cfg.SFTPArgv == nil {
		cfg.SFTPArgv = DefaultSFTPArgv
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.RecheckInterval <= 0 {
		cfg.RecheckInterval = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &SSHServer{
		cfg: cfg, signer: signer, slots: make(chan struct{}, cfg.MaxConns),
		lns: map[net.Listener]bool{}, conns: map[net.Conn]bool{}, live: map[*ssh.ServerConn]*sshLogin{},
		baseCtx: ctx, cancel: cancel,
	}, nil
}

// Serve accepts SSH connections on ln until Close.
func (s *SSHServer) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		ln.Close()
		return net.ErrClosed
	}
	s.lns[ln] = true
	// The backstop starts with the first Serve, under mu and before Close
	// can have begun waiting, so Close always waits for it.
	s.recheck.Do(func() {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.recheckLoop() }()
	})
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.lns, ln)
		s.mu.Unlock()
	}()
	var backoff time.Duration
	for {
		nc, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return net.ErrClosed
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				backoff = min(max(2*backoff, 5*time.Millisecond), time.Second)
				time.Sleep(backoff)
				continue
			}
			return err
		}
		backoff = 0
		select {
		case s.slots <- struct{}{}:
		default:
			// Over the cap: closed before any handshake work, so a flood of
			// connections costs a socket each and nothing more.
			s.cfg.Logf("ssh: %s refused: %d connections open", nc.RemoteAddr(), s.cfg.MaxConns)
			nc.Close()
			continue
		}
		if !s.track(nc) {
			<-s.slots
			nc.Close()
			return net.ErrClosed
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.slots }()
			defer s.untrack(nc)
			s.handleConn(nc)
		}()
	}
}

func (s *SSHServer) track(nc net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[nc] = true
	return true
}

func (s *SSHServer) untrack(nc net.Conn) {
	s.mu.Lock()
	delete(s.conns, nc)
	s.mu.Unlock()
	nc.Close()
}

// Info is GET /v1/ssh: where to connect and the host key to pin.
func (s *SSHServer) Info() api.SSHInfo {
	pub := s.signer.PublicKey()
	return api.SSHInfo{
		Host:        s.cfg.Host,
		Port:        s.cfg.Port,
		HostKeys:    []string{strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))},
		Fingerprint: ssh.FingerprintSHA256(pub),
	}
}

// Close stops accepting and ends open sessions. Closing a connection ends its
// sessions, and each session, finding its channel gone, hangs up the process
// it started (see session.hangup).
func (s *SSHServer) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for ln := range s.lns {
		ln.Close()
	}
	for nc := range s.conns {
		nc.Close()
	}
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	return nil
}

// handleConn runs one connection: handshake and authentication (which
// resolves the sandbox, once), then its channels until it ends.
func (s *SSHServer) handleConn(nc net.Conn) {
	s.cfg.Metrics.connOpened()
	defer s.cfg.Metrics.connClosed()
	login := &sshLogin{}
	_ = nc.SetDeadline(time.Now().Add(s.cfg.HandshakeTimeout))
	sconn, chans, reqs, err := ssh.NewServerConn(nc, s.serverConfig(login))
	if err != nil {
		// Authentication failures were logged where they were decided; this
		// is the handshake ending, which is noise unless it says more.
		if login.refused != "" {
			s.cfg.Metrics.authFailed()
			s.cfg.Audit.write(api.AuditEntry{Kind: "ssh", Action: "ssh.login", Result: "refused",
				Remote: remoteIP(nc.RemoteAddr().String()), Fingerprint: login.refused})
		}
		return
	}
	_ = nc.SetDeadline(time.Time{})
	defer sconn.Close()
	login.remote = remoteIP(nc.RemoteAddr().String())
	s.audit(login, "ssh.login", "")
	// Tracked from here, and checked once more at once: a revocation that
	// landed between the login check and this line found no connection to
	// close, and must not be missed until the next backstop.
	s.mu.Lock()
	s.live[sconn] = login
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.live, sconn)
		s.mu.Unlock()
	}()
	if why := s.lostAccess(login, nil); why != "" {
		s.endLogin(sconn, login, why)
		go ssh.DiscardRequests(reqs)
		for nch := range chans {
			_ = nch.Reject(ssh.Prohibited, "access revoked")
		}
		return
	}

	ctx, cancel := context.WithCancel(s.baseCtx)
	defer cancel()
	s.cfg.Logf("ssh: %s: %s logged in to %s", nc.RemoteAddr(), login.describe(), login.id)

	// Global requests: remote forwarding (tcpip-forward) and everything else
	// are refused. A guest listening on a port the gateway opened would be a
	// path from the guest to the gateway's network, which is the direction
	// that is never offered.
	go func() {
		for r := range reqs {
			if r.WantReply {
				_ = r.Reply(false, nil)
			}
		}
	}()

	open := make(chan struct{}, s.cfg.MaxChannels)
	var wg sync.WaitGroup
	for nch := range chans {
		select {
		case open <- struct{}{}:
		default:
			_ = nch.Reject(ssh.ResourceShortage, "too many channels open on this connection")
			continue
		}
		release := func() { <-open }
		if t := nch.ChannelType(); t == "session" || t == "direct-tcpip" {
			s.cfg.Metrics.sessionOpened()
			release = func() { s.cfg.Metrics.sessionClosed(); <-open }
		}
		switch nch.ChannelType() {
		case "session":
			wg.Add(1)
			go func() { defer wg.Done(); defer release(); s.session(ctx, login, nch) }()
		case "direct-tcpip":
			wg.Add(1)
			go func() { defer wg.Done(); defer release(); s.directTCPIP(ctx, login, nch) }()
		default:
			// x11, auth-agent@openssh.com, forwarded-tcpip, and anything
			// newer: the gateway offers sessions and local forwards only.
			release()
			_ = nch.Reject(ssh.UnknownChannelType, "the sandbox gateway does not offer "+nch.ChannelType()+" channels")
		}
	}
	cancel()
	wg.Wait()
}

// RecheckAccess holds every open connection to the check its login passed,
// and closes those that no longer pass it, returning how many. Closing a
// connection ends its channels, and each session hangs up the process it
// started (session.hangup), so a revoked user is out at once, not at their
// next login.
//
// A connection fails the check when its user holds no active API key with
// sandbox:ssh in its tenant any more (userMaySSH) — the key revoked, or the
// last ssh-capable one — or when it logged in with a registered SSH key that
// has since been removed. A token login stays as long as its user passes:
// the token was spent on the login, and what it granted is the user's.
//
// The gateway calls this whenever it revokes a key or removes an SSH key;
// recheckLoop calls it every RecheckInterval as well, for a store changed
// any other way. It costs one read of the store's keys per user and tenant
// with a connection open, and nothing for a server with none.
func (s *SSHServer) RecheckAccess() int {
	s.mu.Lock()
	live := make(map[*ssh.ServerConn]*sshLogin, len(s.live))
	for c, l := range s.live {
		live[c] = l
	}
	s.mu.Unlock()
	if len(live) == 0 {
		return 0
	}
	cache := map[[2]string]bool{}
	n := 0
	for c, l := range live {
		if why := s.lostAccess(l, cache); why != "" {
			s.endLogin(c, l, why)
			n++
		}
	}
	return n
}

// lostAccess says why login may no longer stay connected, or "". cache, when
// not nil, keeps userMaySSH's answer per user and tenant for one recheck.
func (s *SSHServer) lostAccess(login *sshLogin, cache map[[2]string]bool) string {
	p := login.principal
	who := [2]string{p.User, p.Tenant}
	ok, seen := cache[who]
	if !seen {
		ok = userMaySSH(s.cfg.Store, p.User, p.Tenant)
		if cache != nil {
			cache[who] = ok
		}
	}
	if !ok {
		return "the user holds no active API key with " + ScopeSSH
	}
	if login.sshKeyID != "" {
		for _, k := range s.cfg.Store.SSHKeysFor(p.User) {
			if k.ID == login.sshKeyID && k.Tenant == p.KeyTenant {
				return ""
			}
		}
		return "the SSH key it logged in with was removed"
	}
	return ""
}

// endLogin closes a connection that lost its access, and says so in the log
// and the audit record, by the credential's id and fingerprint only.
func (s *SSHServer) endLogin(c *ssh.ServerConn, login *sshLogin, why string) {
	// Once: a revocation's recheck and the backstop's may find the same
	// connection at the same time.
	s.mu.Lock()
	_, open := s.live[c]
	delete(s.live, c)
	s.mu.Unlock()
	if !open {
		return
	}
	s.cfg.Logf("ssh: %s: closed: %s", login.describe(), why)
	if s.cfg.Audit != nil {
		node := ""
		if o, ok := s.cfg.Store.OwnerOf(login.id); ok {
			node = o.Node
		}
		p := login.principal
		s.cfg.Audit.write(api.AuditEntry{
			Kind: "ssh", Action: "ssh.revoked", KeyID: p.KeyID, User: p.User, Tenant: p.Tenant, Remote: login.remote,
			Sandbox: login.id, Node: node, Result: "closed", Fingerprint: login.fingerprint,
		})
	}
	_ = c.Close()
}

// recheckLoop runs RecheckAccess every RecheckInterval until Close.
func (s *SSHServer) recheckLoop() {
	t := time.NewTicker(s.cfg.RecheckInterval)
	defer t.Stop()
	for {
		select {
		case <-s.baseCtx.Done():
			return
		case <-t.C:
			s.RecheckAccess()
		}
	}
}
