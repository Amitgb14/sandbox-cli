package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// --- fakes -----------------------------------------------------------------

// memStore is the SSH half of Store, in memory. The rest is never called by
// the SSH server; a call would panic through the nil embedded interface,
// which is the test failing loudly.
type memStore struct {
	Store
	mu      sync.Mutex
	keys    []SSHKey
	tokens  map[string]SSHToken
	revoked map[string]bool     // users whose API keys are all revoked
	scopes  map[string][]string // a user's key's scopes; default ssh and read
	extra   []Key               // further API keys, as given
}

// MemberRole: the SSH fakes hold no organisations.
func (m *memStore) MemberRole(string, string, string) (string, bool) { return "", false }

// OwnerOf: the fake router, not the store, knows the sandboxes.
func (m *memStore) OwnerOf(string) (Owner, bool) { return Owner{}, false }

// Keys reports one API key per user the store knows, revoked for the users in
// revoked: what SSH login checks to see that a user is still active and may
// use SSH.
func (m *memStore) Keys() []Key {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	var out []Key
	add := func(u string) {
		if u != "" && !seen[u] {
			seen[u] = true
			sc, ok := m.scopes[u]
			if !ok {
				sc = []string{ScopeSSH, ScopeRead}
			}
			out = append(out, Key{ID: "k-" + u, User: u, Scopes: sc, Revoked: m.revoked[u]})
		}
	}
	for _, k := range m.keys {
		add(k.User)
	}
	for _, t := range m.tokens {
		add(t.User)
	}
	return append(out, m.extra...)
}

func (m *memStore) SSHKeysByFingerprint(fp string) []SSHKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SSHKey
	for _, k := range m.keys {
		if k.Fingerprint == fp {
			out = append(out, k)
		}
	}
	return out
}

func (m *memStore) SSHKeysFor(user string) []SSHKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SSHKey
	for _, k := range m.keys {
		if k.User == user {
			out = append(out, k)
		}
	}
	return out
}

func (m *memStore) removeKey(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, k := range m.keys {
		if k.ID == id {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			return
		}
	}
}

// set changes the store under its lock, as a revocation would.
func (m *memStore) set(f func(m *memStore)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f(m)
}

func (m *memStore) RedeemSSHToken(tok string) (SSHToken, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[tok]
	return t, ok
}

func (m *memStore) addKey(t *testing.T, id, user, sandbox string, pub ssh.PublicKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys = append(m.keys, SSHKey{ID: id, User: user, Fingerprint: ssh.FingerprintSHA256(pub),
		AuthorizedKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))), Sandbox: sandbox})
}

type fakeSandbox struct{ id, name, owner string }

// fakeRouter resolves against a fixed table, with the rules the real router
// is specified to keep: the scope, the owner, and a principal's confinement.
type fakeRouter struct {
	mu        sync.Mutex
	client    *api.Client
	sandboxes []fakeSandbox
	err       error // when set, every Resolve fails with it
	calls     int
	// lax drops a principal's confinement, and redirect answers every
	// Resolve with that sandbox: router bugs the SSH server must not rely on
	// being absent.
	lax      bool
	redirect string
}

func (r *fakeRouter) set(f func(r *fakeRouter)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(r)
}

func (r *fakeRouter) Resolve(_ context.Context, p Principal, ref, scope string) (string, *api.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return "", nil, r.err
	}
	if r.lax {
		p.Sandbox = ""
	}
	if r.redirect != "" {
		p.Sandbox, ref = "", r.redirect
	}
	if !p.Can(scope) {
		return "", nil, ErrForbidden
	}
	for _, sb := range r.sandboxes {
		if sb.id != ref && !(sb.name == ref && sb.owner == p.User) {
			continue
		}
		if sb.owner != p.User || (p.Sandbox != "" && p.Sandbox != sb.id) {
			return "", nil, ErrNotFound
		}
		return sb.id, r.client, nil
	}
	return "", nil, ErrNotFound
}

// tunnelBackend is the fake backend with a guest loopback that echoes, so a
// forward can be followed end to end.
type tunnelBackend struct {
	*fake.Backend
	mu    sync.Mutex
	ports []int
}

func (b *tunnelBackend) DialGuest(_ context.Context, id string, port int) (io.ReadWriteCloser, error) {
	b.mu.Lock()
	b.ports = append(b.ports, port)
	b.mu.Unlock()
	a, c := net.Pipe()
	go func() {
		defer c.Close()
		_, _ = io.Copy(c, c)
	}()
	return a, nil
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) logf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(&s.b, format+"\n", args...)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// --- harness ---------------------------------------------------------------

type harness struct {
	t      *testing.T
	store  *memStore
	router *fakeRouter
	srv    *SSHServer
	addr   string
	logs   *syncBuf
	node   *api.Client
	be     *tunnelBackend
	a, b   string // sandboxes owned by alice
	other  string // a sandbox owned by bob
}

// newHarness starts a node (server.Server over the fake backend, over
// httptest), a router onto it and the SSH server, with three sandboxes: A and
// B owned by alice, and one owned by bob. Each holds a file, marker, naming
// it, so a session can tell which sandbox it landed in.
func newHarness(t *testing.T, mod func(*SSHConfig)) *harness {
	t.Helper()
	be := &tunnelBackend{Backend: fake.New(api.CapEgressAllowlist, api.CapTunnel)}
	node := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}).Handler())
	t.Cleanup(node.Close)
	c, err := api.NewClient(node.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h := &harness{t: t, store: &memStore{tokens: map[string]SSHToken{}}, logs: &syncBuf{}, node: c, be: be}
	mk := func(name, owner string) string {
		sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.WriteFile(ctx, sb.ID, "/sandbox/home/marker", []byte(name+"\n")); err != nil {
			t.Fatal(err)
		}
		return sb.ID
	}
	h.a, h.b, h.other = mk("alpha", "alice"), mk("beta", "alice"), mk("gamma", "bob")
	h.router = &fakeRouter{client: c, sandboxes: []fakeSandbox{
		{h.a, "alpha", "alice"}, {h.b, "beta", "alice"}, {h.other, "gamma", "bob"},
	}}
	cfg := SSHConfig{
		Store: h.store, Router: h.router, HostKeyFile: filepath.Join(t.TempDir(), "host_ed25519"),
		Host: "ssh.example.test", Port: 2222, Logf: h.logs.logf,
		HandshakeTimeout: 10 * time.Second,
		// The fake backend runs builtins only: the shell and sftp-server are
		// cat (input echoed back), and exec's command line is split into
		// argv instead of going through /bin/sh.
		ShellArgv: func() []string { return []string{"cat"} },
		ExecArgv:  func(cmd string) []string { return strings.Fields(cmd) },
		SFTPArgv:  func() []string { return []string{"cat"} },
	}
	if mod != nil {
		mod(&cfg)
	}
	srv, err := NewSSHServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { srv.Close() })
	h.srv, h.addr = srv, ln.Addr().String()
	return h
}

func newKey(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv
}

// dial logs in as user with the given auth methods, pinning the gateway's
// host key. banner collects what the server said on refusal.
func (h *harness) dial(user string, banner *strings.Builder, auth ...ssh.AuthMethod) (*ssh.Client, error) {
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(h.srv.Info().HostKeys[0]))
	if err != nil {
		h.t.Fatal(err)
	}
	cfg := &ssh.ClientConfig{User: user, Auth: auth, HostKeyCallback: ssh.FixedHostKey(hostKey), Timeout: 10 * time.Second}
	if banner != nil {
		cfg.BannerCallback = func(m string) error { banner.WriteString(m); return nil }
	}
	return ssh.Dial("tcp", h.addr, cfg)
}

func (h *harness) mustDial(user string, auth ...ssh.AuthMethod) *ssh.Client {
	h.t.Helper()
	c, err := h.dial(user, nil, auth...)
	if err != nil {
		h.t.Fatalf("login as %s: %v", user, err)
	}
	h.t.Cleanup(func() { c.Close() })
	return c
}

func run(t *testing.T, c *ssh.Client, cmd string) (string, error) {
	t.Helper()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, err := s.CombinedOutput(cmd)
	return string(out), err
}

func exitStatus(err error) int {
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return ee.ExitStatus()
	}
	if err == nil {
		return 0
	}
	return -1
}

// --- tests -----------------------------------------------------------------

func TestSSHExecReturnsOutputAndExitStatus(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	c := h.mustDial("alpha", ssh.PublicKeys(k))

	out, err := run(t, c, "echo hello there")
	if err != nil || out != "hello there\n" {
		t.Errorf("echo: %q, %v", out, err)
	}
	if _, err := run(t, c, "false"); exitStatus(err) != 1 {
		t.Errorf("false: %v, want exit status 1", err)
	}
	// It runs in the sandbox user's home, of the sandbox the username named.
	if out, _ := run(t, c, "pwd"); out != "/sandbox/home\n" {
		t.Errorf("pwd: %q", out)
	}
	if out, _ := run(t, c, "cat marker"); out != "alpha\n" {
		t.Errorf("landed in %q, want alpha", out)
	}
	// A command the guest does not have fails the request, not the
	// connection.
	if _, err := run(t, c, "no-such-command"); err == nil {
		t.Error("an unknown command succeeded")
	}
	if out, err := run(t, c, "echo still here"); err != nil || out != "still here\n" {
		t.Errorf("after a failed exec: %q, %v", out, err)
	}
	// Login by id works as well as by name, and resolves once per
	// connection, not per session.
	h.router.mu.Lock()
	before := h.router.calls
	h.router.mu.Unlock()
	c2 := h.mustDial(h.b, ssh.PublicKeys(k))
	run(t, c2, "true")
	run(t, c2, "true")
	h.router.mu.Lock()
	calls := h.router.calls - before
	h.router.mu.Unlock()
	if calls != 1 {
		t.Errorf("Resolve was called %d times for one connection, want 1", calls)
	}
	if out, _ := run(t, c2, "cat marker"); out != "beta\n" {
		t.Errorf("by id: landed in %q, want beta", out)
	}
}

func TestSSHStdinWithoutATerminalReachesEOF(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	c := h.mustDial("alpha", ssh.PublicKeys(k))
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	s.Stdin = strings.NewReader("piped through\n")
	out, err := s.Output("cat")
	if err != nil || string(out) != "piped through\n" {
		t.Errorf("cat < input: %q, %v", out, err)
	}
}

func TestSSHPtyShellRoundTripsAndResizes(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	c := h.mustDial("alpha", ssh.PublicKeys(k))

	// TERM comes from the pty request; LANG is passed; LD_PRELOAD is not.
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Setenv("LANG", "C.UTF-8"); err != nil {
		t.Errorf("LANG refused: %v", err)
	}
	if err := s.Setenv("LD_PRELOAD", "/tmp/evil.so"); err == nil {
		t.Error("LD_PRELOAD was accepted")
	}
	if err := s.RequestPty("vt220", 40, 100, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	out, err := s.Output("printenv")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "TERM=vt220") || !strings.Contains(string(out), "LANG=C.UTF-8") || strings.Contains(string(out), "LD_PRELOAD") {
		t.Errorf("environment: %q", out)
	}

	s, err = c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	stdin, _ := s.StdinPipe()
	stdout, _ := s.StdoutPipe()
	if err := s.Shell(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(stdin, "ping\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(stdout, buf); err != nil || string(buf) != "ping\n" {
		t.Fatalf("round trip: %q, %v", buf, err)
	}
	if err := s.WindowChange(50, 132); err != nil {
		t.Errorf("window-change: %v", err)
	}
	// Still alive after the resize.
	io.WriteString(stdin, "pong\n")
	if _, err := io.ReadFull(stdout, buf); err != nil || string(buf) != "pong\n" {
		t.Fatalf("after resize: %q, %v", buf, err)
	}
	// A signal reaches the process, and its exit status comes back.
	if err := s.Signal(ssh.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := s.Wait(); exitStatus(err) != 130 {
		t.Errorf("after SIGINT: %v, want exit status 130", err)
	}
}

func TestSSHSFTPSubsystem(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	c := h.mustDial("alpha", ssh.PublicKeys(k))
	s, _ := c.NewSession()
	if err := s.RequestSubsystem("nope"); err == nil {
		t.Error("an unknown subsystem was accepted")
	}
	s.Close()

	s, _ = c.NewSession()
	defer s.Close()
	stdin, _ := s.StdinPipe()
	stdout, _ := s.StdoutPipe()
	if err := s.RequestSubsystem("sftp"); err != nil {
		t.Fatal(err)
	}
	io.WriteString(stdin, "\x00\x00\x00\x05\x01binary")
	stdin.Close()
	got, _ := io.ReadAll(stdout)
	if string(got) != "\x00\x00\x00\x05\x01binary" {
		t.Errorf("sftp stream: %q", got)
	}
}

func TestSSHUnregisteredKeyIsRefused(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	stranger, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	if c, err := h.dial("alpha", nil, ssh.PublicKeys(stranger)); err == nil {
		c.Close()
		t.Fatal("a key nobody registered logged in")
	}
	// No password or keyboard-interactive either, and no "none" for a
	// username that is not a token.
	if c, err := h.dial("alpha", nil, ssh.Password("x"), ssh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) { return nil, nil })); err == nil {
		c.Close()
		t.Fatal("logged in without a key")
	}
	// A key stored with no user is never a login.
	h.store.addKey(t, "sk_orphan", "", "", stranger.PublicKey())
	if c, err := h.dial("alpha", nil, ssh.PublicKeys(stranger)); err == nil {
		c.Close()
		t.Fatal("a key with no owner logged in")
	}
}

func TestSSHKeyReachesOnlyItsOwnersSandboxes(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	var b1, b2 strings.Builder
	if c, err := h.dial(h.other, &b1, ssh.PublicKeys(k)); err == nil {
		c.Close()
		t.Fatal("alice's key logged in to bob's sandbox")
	}
	if c, err := h.dial("sbx_nonexistent", &b2, ssh.PublicKeys(k)); err == nil {
		c.Close()
		t.Fatal("logged in to a sandbox that does not exist")
	}
	// Someone else's sandbox and no sandbox at all read the same.
	if b1.String() != b2.String() || !strings.Contains(b1.String(), "no such sandbox") {
		t.Errorf("refusals differ or are unclear: %q vs %q", b1.String(), b2.String())
	}
}

func TestSSHKeyLimitedToASandboxCannotReachAnother(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_lim", "alice", h.a, k.PublicKey())
	c := h.mustDial("alpha", ssh.PublicKeys(k))
	if out, _ := run(t, c, "cat marker"); out != "alpha\n" {
		t.Errorf("limited key: %q", out)
	}
	for _, ref := range []string{"beta", h.b} {
		if c, err := h.dial(ref, nil, ssh.PublicKeys(k)); err == nil {
			c.Close()
			t.Errorf("a key limited to A logged in to %s", ref)
		}
	}
}

// The limit holds even if the router were to ignore Principal.Sandbox: the
// SSH server checks the id it was given back.
func TestSSHKeyLimitHoldsWhateverTheRouterDoes(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_lim", "alice", h.a, k.PublicKey())
	h.router.set(func(r *fakeRouter) { r.lax = true })
	if c, err := h.dial("beta", nil, ssh.PublicKeys(k)); err == nil {
		c.Close()
		t.Fatal("a key limited to A reached B through a router that forgot the limit")
	}
}

func TestSSHSameKeyForTwoUsersPicksTheOwner(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_a", "alice", "", k.PublicKey())
	h.store.addKey(t, "sk_b", "bob", "", k.PublicKey())
	c := h.mustDial("gamma", ssh.PublicKeys(k))
	if out, _ := run(t, c, "cat marker"); out != "gamma\n" {
		t.Errorf("landed in %q, want bob's gamma", out)
	}
	// A name both owners have is ambiguous and refused, not guessed.
	h.router.set(func(r *fakeRouter) {
		r.sandboxes = append(r.sandboxes, fakeSandbox{h.other + "x", "alpha", "bob"})
	})
	var banner strings.Builder
	if c, err := h.dial("alpha", &banner, ssh.PublicKeys(k)); err == nil {
		c.Close()
		t.Fatal("an ambiguous name logged in")
	}
	if !strings.Contains(banner.String(), "sandbox id") {
		t.Errorf("ambiguous refusal: %q", banner.String())
	}
}

func TestSSHTokenLogsInWithoutAKey(t *testing.T) {
	h := newHarness(t, nil)
	h.store.tokens["sgt_valid"] = SSHToken{User: "alice", Sandbox: h.a, Expires: time.Now().Add(time.Minute)}
	h.store.tokens["sgt_expired"] = SSHToken{User: "alice", Sandbox: h.a, Expires: time.Now().Add(-time.Second)}
	h.store.tokens["sgt_noexpiry"] = SSHToken{User: "alice", Sandbox: h.a}
	h.store.tokens["sgt_bobs"] = SSHToken{User: "alice", Sandbox: h.other, Expires: time.Now().Add(time.Minute)}

	c := h.mustDial("sgt_valid")
	if out, _ := run(t, c, "cat marker"); out != "alpha\n" {
		t.Errorf("token login landed in %q, want alpha", out)
	}
	for _, tok := range []string{"sgt_expired", "sgt_noexpiry", "sgt_unknown", "sgt_bobs"} {
		if c, err := h.dial(tok, nil); err == nil {
			c.Close()
			t.Errorf("%s logged in", tok)
		}
	}
	// A token is not a sandbox reference: a key does not log in with one.
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	if c, err := h.dial("sgt_unknown", nil, ssh.PublicKeys(k)); err == nil {
		c.Close()
		t.Error("a key logged in with a token-shaped username")
	}
	// A plain username gets no "none" login.
	if c, err := h.dial("alpha", nil); err == nil {
		c.Close()
		t.Error("a sandbox name logged in with no credential")
	}
}

// A token confined to A cannot be turned toward B: the principal it becomes
// carries the confinement, and the id the router returns is checked.
func TestSSHTokenForAReachesOnlyA(t *testing.T) {
	h := newHarness(t, nil)
	h.store.tokens["sgt_a"] = SSHToken{User: "alice", Sandbox: h.a, Expires: time.Now().Add(time.Minute)}
	h.router.set(func(r *fakeRouter) { r.redirect = h.b })
	if c, err := h.dial("sgt_a", nil); err == nil {
		c.Close()
		t.Fatal("a token for A logged in to B")
	}
}

func TestSSHRouterNotFoundEndsTheLogin(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	h.router.set(func(r *fakeRouter) { r.err = ErrNotFound })
	var banner strings.Builder
	if c, err := h.dial("alpha", &banner, ssh.PublicKeys(k)); err == nil {
		c.Close()
		t.Fatal("logged in although the router found no sandbox")
	}
	if !strings.Contains(banner.String(), "no such sandbox") {
		t.Errorf("banner: %q", banner.String())
	}
	h.router.set(func(r *fakeRouter) { r.err = fmt.Errorf("dial node-7 at 10.1.2.3: %w", ErrNodeDown) })
	banner.Reset()
	if c, err := h.dial("alpha", &banner, ssh.PublicKeys(k)); err == nil {
		c.Close()
		t.Fatal("logged in although the node is down")
	}
	if !strings.Contains(banner.String(), "not answering") || strings.Contains(banner.String(), "10.1.2.3") {
		t.Errorf("node-down banner: %q", banner.String())
	}
}

func TestSSHLocalForwardOnlyToTheSandboxLoopback(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	c := h.mustDial("alpha", ssh.PublicKeys(k))
	for _, addr := range []string{"10.0.0.1:80", "169.254.169.254:80", "example.com:443", "0.0.0.0:22"} {
		if conn, err := c.Dial("tcp", addr); err == nil {
			conn.Close()
			t.Errorf("forward to %s was opened", addr)
		}
	}
	h.be.mu.Lock()
	n := len(h.be.ports)
	h.be.mu.Unlock()
	if n != 0 {
		t.Fatalf("a refused forward reached the node (%d tunnels)", n)
	}
	conn, err := c.Dial("tcp", "localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "through the tunnel")
	buf := make([]byte, len("through the tunnel"))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "through the tunnel" {
		t.Errorf("tunnel echo: %q, %v", buf, err)
	}
	h.be.mu.Lock()
	ports := append([]int(nil), h.be.ports...)
	h.be.mu.Unlock()
	if len(ports) != 1 || ports[0] != 8080 {
		t.Errorf("tunnels opened: %v, want [8080]", ports)
	}
}

func TestSSHRemoteForwardingAgentAndX11AreRefused(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	c := h.mustDial("alpha", ssh.PublicKeys(k))
	if ln, err := c.Listen("tcp", "127.0.0.1:0"); err == nil {
		ln.Close()
		t.Error("remote forwarding (tcpip-forward) was accepted")
	}
	s, _ := c.NewSession()
	defer s.Close()
	if err := agent.RequestAgentForwarding(s); err == nil {
		t.Error("agent forwarding was accepted")
	}
	if ok, _ := s.SendRequest("x11-req", true, ssh.Marshal(struct {
		Single        bool
		Proto, Cookie string
		Screen        uint32
	}{false, "MIT-MAGIC-COOKIE-1", "00", 0})); ok {
		t.Error("x11 forwarding was accepted")
	}
	for _, typ := range []string{"x11", "auth-agent@openssh.com", "forwarded-tcpip", "direct-streamlocal@openssh.com"} {
		if ch, _, err := c.OpenChannel(typ, nil); err == nil {
			ch.Close()
			t.Errorf("a %s channel was opened", typ)
		}
	}
}

func TestSSHChannelAndConnectionCaps(t *testing.T) {
	h := newHarness(t, func(c *SSHConfig) { c.MaxConns = 1; c.MaxChannels = 1 })
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	c := h.mustDial("alpha", ssh.PublicKeys(k))
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.NewSession(); err == nil {
		t.Error("a second channel opened over MaxChannels")
	}
	s.Close()
	if c2, err := h.dial("alpha", nil, ssh.PublicKeys(k)); err == nil {
		c2.Close()
		t.Error("a second connection was served over MaxConns")
	}
}

func TestSSHHandshakeTimeout(t *testing.T) {
	h := newHarness(t, func(c *SSHConfig) { c.HandshakeTimeout = 200 * time.Millisecond })
	nc, err := net.Dial("tcp", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	// Say nothing; the server must give up on us.
	_ = nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.ReadAll(nc)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("the server held a silent connection open past its handshake timeout")
	}
}

func TestSSHHostKeyCreated0600AndReused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "host_ed25519")
	cfg := SSHConfig{Store: &memStore{}, Router: &fakeRouter{}, HostKeyFile: path, Host: "h", Port: 22}
	s1, err := NewSSHServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("host key mode %04o, want 0600", fi.Mode().Perm())
	}
	info := s1.Info()
	if info.Host != "h" || info.Port != 22 || len(info.HostKeys) != 1 ||
		!strings.HasPrefix(info.HostKeys[0], "ssh-ed25519 ") || !strings.HasPrefix(info.Fingerprint, "SHA256:") {
		t.Errorf("info: %+v", info)
	}
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(info.HostKeys[0]))
	if err != nil || ssh.FingerprintSHA256(pk) != info.Fingerprint {
		t.Errorf("host key line and fingerprint disagree: %v", err)
	}
	s2, err := NewSSHServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Info().Fingerprint != info.Fingerprint {
		t.Error("the host key was not reused")
	}

	// Readable by others: refused, not repaired.
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSSHServer(cfg); err == nil {
		t.Error("a group-readable host key was accepted")
	}
	// A symlink to a key: refused.
	os.Chmod(path, 0o600)
	link := filepath.Join(dir, "link")
	os.Symlink(path, link)
	cfg.HostKeyFile = link
	if _, err := NewSSHServer(cfg); err == nil {
		t.Error("a symlinked host key was accepted")
	}
	// No file named at all: refused.
	cfg.HostKeyFile = ""
	if _, err := NewSSHServer(cfg); err == nil {
		t.Error("no host key file was accepted")
	}
}

// GET /v1/ssh and ssh-access hand out Host and Port; a server with none
// clients could use is refused at start rather than published.
func TestSSHServerRefusesUnusablePublicAddress(t *testing.T) {
	for _, tc := range []struct {
		host string
		port int
	}{
		{"", 2222},
		{"0.0.0.0", 2222},
		{"::", 2222},
		{"gw.example", 0},
		{"gw.example", 70000},
		{"gw.example:2222", -1},
		{"-oProxyCommand=x", 2222},
		{"gw example", 2222},
		{"user@gw.example", 2222},
	} {
		cfg := SSHConfig{Store: &memStore{}, Router: &fakeRouter{},
			HostKeyFile: filepath.Join(t.TempDir(), "k"), Host: tc.host, Port: tc.port}
		if _, err := NewSSHServer(cfg); err == nil {
			t.Errorf("host %q port %d was accepted", tc.host, tc.port)
		}
	}
	for _, host := range []string{"gw.example", "127.0.0.1", "::1", "ssh-1.fleet.internal"} {
		cfg := SSHConfig{Store: &memStore{}, Router: &fakeRouter{},
			HostKeyFile: filepath.Join(t.TempDir(), "k"), Host: host, Port: 22}
		s, err := NewSSHServer(cfg)
		if err != nil {
			t.Errorf("host %q: %v", host, err)
			continue
		}
		if info := s.Info(); info.Host != host || info.Port != 22 {
			t.Errorf("info %+v", info)
		}
	}
}

func TestSSHLogsCarryNoSecrets(t *testing.T) {
	h := newHarness(t, nil)
	k, priv := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	const token = "sgt_s3cr3tTOKENvalue"
	h.store.tokens[token] = SSHToken{User: "alice", Sandbox: h.a, Expires: time.Now().Add(time.Minute)}

	c := h.mustDial("alpha", ssh.PublicKeys(k))
	run(t, c, "echo TOPSECRETARG")
	c.Close()
	c = h.mustDial(token)
	run(t, c, "true")
	c.Close()
	if c, err := h.dial("sgt_another_secret_value", nil); err == nil {
		c.Close()
	}
	if c, err := h.dial("sgt_another_secret_value", nil, ssh.PublicKeys(k)); err == nil {
		c.Close()
	}
	h.srv.Close()

	logs := h.logs.String()
	pubB64 := strings.Fields(string(ssh.MarshalAuthorizedKey(k.PublicKey())))[1]
	for _, secret := range []string{token, "s3cr3t", "another_secret", pubB64, string(priv), fmt.Sprintf("%x", []byte(priv)), "TOPSECRETARG"} {
		if strings.Contains(logs, secret) {
			t.Errorf("the log carries %q:\n%s", secret, logs)
		}
	}
	// What it should carry instead: the key's fingerprint and id, the user,
	// the sandbox.
	for _, want := range []string{ssh.FingerprintSHA256(k.PublicKey()), "sk_1", `"alice"`, h.a, "a token"} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log lacks %q:\n%s", want, logs)
		}
	}
}

// Disconnecting with a process still running hangs it up, as sshd does.
func TestSSHDisconnectHangsUpTheProcess(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	c := h.mustDial("alpha", ssh.PublicKeys(k))
	s, _ := c.NewSession()
	if err := s.Start("sleep 60"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var pid int
	for range 100 {
		ps, _ := h.node.Processes(ctx, h.a)
		if len(ps) > 0 {
			pid = ps[len(ps)-1].PID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the process never started")
	}
	c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p, err := h.node.Process(ctx, h.a, pid)
		if err == nil && p.State != api.ProcessRunning {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the process kept running after its client disconnected")
}

// Revoking a user's API keys ends their SSH access too: their registered SSH
// keys and any token they already hold stop working at once. Before this a
// revoked user kept every sandbox they owned over SSH.
func TestSSHRevokedUserCannotLogIn(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	h.store.tokens["sgt_valid"] = SSHToken{User: "alice", Sandbox: h.a, Expires: time.Now().Add(time.Hour)}
	h.mustDial(h.a, ssh.PublicKeys(k)).Close()
	h.mustDial("sgt_valid").Close()

	h.store.mu.Lock()
	h.store.revoked = map[string]bool{"alice": true}
	h.store.mu.Unlock()
	if c, err := h.dial(h.a, nil, ssh.PublicKeys(k)); err == nil {
		c.Close()
		t.Error("a revoked user's SSH key still logged in")
	}
	if c, err := h.dial("sgt_valid", nil); err == nil {
		c.Close()
		t.Error("a revoked user's token still logged in")
	}
}

// A user is a name within a tenant. Alice of the default tenant, revoked,
// once kept her SSH access because some other tenant had an active "alice":
// the check matched the name alone.
func TestSSHRevokedUserIsNotKeptActiveByAnotherTenantsNamesake(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	h.store.tokens["sgt_valid"] = SSHToken{User: "alice", Sandbox: h.a, Expires: time.Now().Add(time.Hour)}
	h.store.mu.Lock()
	h.store.revoked = map[string]bool{"alice": true}
	h.store.extra = []Key{{ID: "k-other", User: "alice", Tenant: "elsewhere"}}
	h.store.mu.Unlock()
	if !userActive(h.store, "alice", "elsewhere") {
		t.Fatal("precondition: the other tenant's alice is not active")
	}
	if c, err := h.dial(h.a, nil, ssh.PublicKeys(k)); err == nil {
		c.Close()
		t.Error("a revoked user's SSH key logged in on another tenant's namesake")
	}
	if c, err := h.dial("sgt_valid", nil); err == nil {
		c.Close()
		t.Error("a revoked user's token logged in on another tenant's namesake")
	}
}
