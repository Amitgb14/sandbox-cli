package gateway

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Revocation, and what it ends: SSH logins and connections, and jobs.

// SSH needs a key carrying sandbox:ssh. A user whose only active key is
// read-only once logged in with a registered SSH key, or a token, for as
// long as either existed: the check asked only for some active key.
func TestSSHNeedsAKeyWithTheSSHScope(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	h.store.tokens["sgt_valid"] = SSHToken{User: "alice", Sandbox: h.a, Expires: time.Now().Add(time.Hour)}
	h.store.set(func(m *memStore) { m.scopes = map[string][]string{"alice": {ScopeRead, ScopeCreate}} })
	if !userActive(h.store, "alice", "") {
		t.Fatal("precondition: alice holds no active key at all")
	}
	if c, err := h.dial(h.a, nil, ssh.PublicKeys(k)); err == nil {
		c.Close()
		t.Error("an SSH key logged in for a user whose keys lack sandbox:ssh")
	}
	if c, err := h.dial("sgt_valid", nil); err == nil {
		c.Close()
		t.Error("a token logged in for a user whose keys lack sandbox:ssh")
	}
	// Admin carries every scope.
	h.store.set(func(m *memStore) { m.scopes = map[string][]string{"alice": {ScopeAdmin}} })
	h.mustDial(h.a, ssh.PublicKeys(k)).Close()
	h.mustDial("sgt_valid").Close()
}

// waitClosed reports whether c's connection ends within a few seconds.
func waitClosed(c *ssh.Client) bool {
	done := make(chan struct{})
	go func() { _ = c.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

// The backstop: a store changed without the gateway asking for a recheck —
// edited by hand, or shared with another process — still ends the
// connections it no longer allows, within RecheckInterval.
func TestSSHRecheckClosesConnectionsTheStoreNoLongerAllows(t *testing.T) {
	h := newHarness(t, func(c *SSHConfig) { c.RecheckInterval = 50 * time.Millisecond })
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	h.store.tokens["sgt_valid"] = SSHToken{User: "alice", Sandbox: h.a, Expires: time.Now().Add(time.Hour)}
	bk, _ := newKey(t)
	h.store.addKey(t, "sk_2", "bob", "", bk.PublicKey())
	byKey := h.mustDial(h.a, ssh.PublicKeys(k))
	byToken := h.mustDial("sgt_valid")
	bob := h.mustDial(h.other, ssh.PublicKeys(bk))

	h.store.set(func(m *memStore) { m.scopes = map[string][]string{"alice": {ScopeRead}} })
	if !waitClosed(byKey) {
		t.Error("a key login stayed open after its user lost sandbox:ssh")
	}
	if !waitClosed(byToken) {
		t.Error("a token login stayed open after its user lost sandbox:ssh")
	}
	if out, err := run(t, bob, "echo still"); err != nil || out != "still\n" {
		t.Errorf("another user's connection was disturbed: %q %v", out, err)
	}

	// Removing the SSH key a connection logged in with ends it, though its
	// user still may use SSH.
	h.store.removeKey("sk_2")
	if !waitClosed(bob) {
		t.Error("a connection stayed open after the SSH key it logged in with was removed")
	}
}

// A token login is the user's: it stays while the user still may use SSH,
// whichever key they lose, and ends when the last ssh-capable one goes.
func TestSSHRecheckKeepsWhatStillPasses(t *testing.T) {
	h := newHarness(t, nil)
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	h.store.tokens["sgt_valid"] = SSHToken{User: "alice", Sandbox: h.a, Expires: time.Now().Add(time.Hour)}
	byKey := h.mustDial(h.a, ssh.PublicKeys(k))
	byToken := h.mustDial("sgt_valid")
	h.store.set(func(m *memStore) {
		m.extra = []Key{{ID: "k-alice-ro", User: "alice", Scopes: []string{ScopeRead}, Revoked: true}}
	})
	if n := h.srv.RecheckAccess(); n != 0 {
		t.Fatalf("revoking a key alice did not need closed %d connections", n)
	}
	for _, c := range []*ssh.Client{byKey, byToken} {
		if out, err := run(t, c, "echo still"); err != nil || out != "still\n" {
			t.Fatalf("a connection that still passes: %q %v", out, err)
		}
	}
	h.store.set(func(m *memStore) { m.revoked = map[string]bool{"alice": true} })
	if n := h.srv.RecheckAccess(); n != 2 {
		t.Fatalf("closed %d connections; want 2", n)
	}
	if !waitClosed(byKey) || !waitClosed(byToken) {
		t.Fatal("a connection stayed open")
	}
	if !strings.Contains(h.logs.String(), "closed: the user holds no active API key with sandbox:ssh") {
		t.Errorf("the log does not say why:\n%s", h.logs.String())
	}
}

// sshGateway is a gateway with its SSH server, as serve wires them.
func sshGateway(t *testing.T, audit string) (*testGateway, *SSHServer, string) {
	t.Helper()
	tg := startGateway(t, func(c *Config) {
		if audit != "" {
			c.Audit = NewAuditLog(audit)
		}
	}, startNode(t, "n1", allCaps...))
	cfg := SSHConfig{
		Store: tg.store, Router: tg.g, HostKeyFile: filepath.Join(t.TempDir(), "host_ed25519"),
		Host: "ssh.example.test", Port: 2222, Logf: t.Logf, HandshakeTimeout: 10 * time.Second,
		// The fake backend runs builtins, so exec's command line is argv.
		ExecArgv: func(cmd string) []string { return strings.Fields(cmd) },
		Audit:    tg.g.audit,
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
	tg.g.SetSSH(srv)
	return tg, srv, ln.Addr().String()
}

func dialAs(t *testing.T, srv *SSHServer, addr, user string, auth ...ssh.AuthMethod) *ssh.Client {
	t.Helper()
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(srv.Info().HostKeys[0]))
	if err != nil {
		t.Fatal(err)
	}
	c, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: user, Auth: auth,
		HostKeyCallback: ssh.FixedHostKey(hostKey), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("login as %s: %v", user, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// Revoking a key through the admin API ends, before the call returns, every
// open SSH connection its user may no longer hold — a running session
// included — and removing an SSH key ends the connections made with it.
// Before, revocation stopped the next login only: an open shell stayed open
// for as long as its client kept it.
func TestRevokingAKeyEndsOpenSSHSessions(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	tg, srv, addr := sshGateway(t, auditPath)
	ctx := ctxT(t)
	secret, key, err := tg.store.CreateKey("alice", "", []string{ScopeRead, ScopeCreate, ScopeSSH})
	if err != nil {
		t.Fatal(err)
	}
	// A read-only key alice keeps: she stays active, but not for SSH.
	if _, _, err := tg.store.CreateKey("alice", "", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	alice := api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client())
	sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "box"})
	if err != nil {
		t.Fatal(err)
	}
	k, _ := newKey(t)
	sk, err := tg.store.AddSSHKey("alice", "", "", strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k.PublicKey()))))
	if err != nil {
		t.Fatal(err)
	}
	access, err := alice.SSHAccess(ctx, sb.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	byKey := dialAs(t, srv, addr, "box", ssh.PublicKeys(k))
	byToken := dialAs(t, srv, addr, access.User)
	s, err := byKey.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start("sleep 60"); err != nil {
		t.Fatal(err)
	}

	if code := tg.do(adminSecret(t, tg), http.MethodDelete, "/v1/admin/keys/"+key.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if !waitClosed(byKey) || !waitClosed(byToken) {
		t.Fatal("an SSH connection stayed open after its user's ssh key was revoked")
	}
	sessionEnded := make(chan error, 1)
	go func() { sessionEnded <- s.Wait() }()
	select {
	case <-sessionEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("the running session outlived the revocation")
	}

	// A fresh ssh key, a fresh login, and the SSH key removed by an admin.
	if _, _, err := tg.store.CreateKey("alice", "", []string{ScopeSSH}); err != nil {
		t.Fatal(err)
	}
	again := dialAs(t, srv, addr, "box", ssh.PublicKeys(k))
	if code := tg.do(adminSecret(t, tg), http.MethodDelete, "/v1/admin/ssh-keys/"+sk.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("remove ssh key: %d", code)
	}
	if !waitClosed(again) {
		t.Fatal("an SSH connection stayed open after its SSH key was removed")
	}

	entries, _ := tg.g.audit.read(time.Time{}, 1000)
	closed := 0
	for _, e := range entries {
		if e.Action == "ssh.revoked" && e.Result == "closed" && e.User == "alice" {
			closed++
			if strings.Contains(e.KeyID+e.Fingerprint, "sgt_") {
				t.Errorf("the audit record carries a token: %+v", e)
			}
		}
	}
	if closed != 3 {
		t.Errorf("%d ssh.revoked entries; want 3:\n%+v", closed, entries)
	}
}

func adminSecret(t *testing.T, tg *testGateway) string {
	t.Helper()
	s, _, err := tg.store.CreateKey("root", "", []string{ScopeAdmin})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Revoking a user's last key cancels their running jobs as DELETE
// /v1/jobs/{id} does: running sandboxes terminated, queued runs never
// started, the job saying why, the audit record saying which. Before, a job
// outlived its owner's keys and went on making sandboxes in their name.
func TestRevokingAKeyCancelsItsOwnersJobs(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	tg := startGateway(t, func(c *Config) { c.Audit = NewAuditLog(auditPath) }, n1)
	ctx := ctxT(t)
	secret, key, err := tg.store.CreateKey("ana", "", []string{ScopeRead, ScopeCreate})
	if err != nil {
		t.Fatal(err)
	}
	ana := api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client())
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "60"}, Completions: 3, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	// bob's job is not his business.
	bob := tg.jobUser("bob")
	bj, err := bob.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, ana, j.ID, "the first run to start", func(j api.Job) bool { return j.Runs[0].PID != 0 })
	waitJob(t, bob, bj.ID, "bob's run to start", func(j api.Job) bool { return j.Runs[0].PID != 0 })

	if code := tg.do(adminSecret(t, tg), http.MethodDelete, "/v1/admin/keys/"+key.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	waitFor(t, "ana's job to end", func() bool { r, _ := tg.store.Job(j.ID); return r.State != api.JobRunning })
	rec, _ := tg.store.Job(j.ID)
	if rec.State != api.JobCancelled || rec.Error != reasonOwnerRevoked {
		t.Fatalf("job = %+v", rec)
	}
	for _, r := range rec.Runs {
		if r.State != api.RunCancelled || r.Error != reasonOwnerRevoked {
			t.Errorf("run = %+v", r)
		}
		if r.N > 0 && r.Attempts != 0 {
			t.Errorf("queued run %d was started after the revocation: %+v", r.N, r)
		}
	}
	waitFor(t, "ana's sandbox to be terminated", func() bool { return n1.count() == 1 })
	if b, err := bob.Job(ctx, bj.ID); err != nil || b.State != api.JobRunning {
		t.Fatalf("bob's job: %+v %v", b, err)
	}

	entries, _ := tg.g.audit.read(time.Time{}, 1000)
	found := false
	for _, e := range entries {
		if e.Kind == "job" && e.Action == "job.revoked" && e.Target == j.ID && e.User == "ana" && e.Result == "cancelled" {
			found = true
		}
	}
	if !found {
		t.Errorf("no job.revoked entry for %s:\n%+v", j.ID, entries)
	}
}

// A job whose owner was revoked while the gateway was down — the CLI's keys
// revoke, which needs the gateway stopped — is cancelled at Start, before
// any of its runs makes a sandbox.
func TestJobOfARevokedOwnerIsCancelledAtStart(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	st, err := OpenFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_, key, err := st.CreateKey("ana", "", []string{ScopeCreate})
	if err != nil {
		t.Fatal(err)
	}
	rec := &jobRecord{ID: newID("job_"), User: "ana", Spec: api.JobSpec{Command: []string{"true"}, Completions: 2,
		Parallelism: 1, TimeoutSecs: 10}, State: api.JobRunning, Created: time.Now(),
		Runs: []api.JobRun{{N: 0, State: api.RunQueued}, {N: 1, State: api.RunQueued}}}
	if err := st.PutJob(rec); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeKey(key.ID); err != nil {
		t.Fatal(err)
	}
	g, err := New(Config{Store: st, PollInterval: 100 * time.Millisecond, Logf: t.Logf, StaticNodes: []NodeConfig{n1.config()}})
	if err != nil {
		t.Fatal(err)
	}
	g.Start(context.Background())
	t.Cleanup(g.Close)
	waitFor(t, "the job to end", func() bool { r, _ := st.Job(rec.ID); return r.State != api.JobRunning })
	r, _ := st.Job(rec.ID)
	if r.State != api.JobCancelled || r.Error != reasonOwnerRevoked {
		t.Fatalf("job = %+v", r)
	}
	for _, run := range r.Runs {
		if run.Attempts != 0 || run.Sandbox != "" {
			t.Errorf("run %d made a sandbox for a revoked owner: %+v", run.N, run)
		}
	}
}

// Each new sandbox is made in the owner's name, so each attempt checks the
// owner first: a key revoked behind the gateway's back (no accessChanged,
// and the periodic recheck not yet due) still stops the next run.
func TestJobAttemptChecksItsOwnerFirst(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, func(c *Config) { c.AccessRecheckInterval = time.Hour }, n1)
	ctx := ctxT(t)
	secret, key, err := tg.store.CreateKey("ana", "", []string{ScopeRead, ScopeCreate})
	if err != nil {
		t.Fatal(err)
	}
	ana := api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client())
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "1"}, Completions: 2, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, ana, j.ID, "the first run to start", func(j api.Job) bool { return j.Runs[0].PID != 0 })
	if err := tg.store.RevokeKey(key.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the job to end", func() bool { r, _ := tg.store.Job(j.ID); return r.State != api.JobRunning })
	rec, _ := tg.store.Job(j.ID)
	if rec.State != api.JobCancelled || rec.Runs[1].Attempts != 0 || rec.Runs[1].State != api.RunCancelled {
		t.Fatalf("job = %+v", rec)
	}
	if n1.count() != 0 {
		t.Fatalf("%d sandboxes left", n1.count())
	}
}
