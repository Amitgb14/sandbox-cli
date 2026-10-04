package gateway

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
)

// Revocation ends API requests already open: a followed output stream, an
// attached process, a tunnel. Before, a key was checked only when a request
// arrived, and each of these went on for as long as its client kept it.
// These tests observe only what a client and the node see, so they run
// unchanged against the gateway before the fix; live_registry_test.go holds
// the ones about the registry itself.

// liveGateway is a gateway with an audit log over one node that can tunnel,
// and two keys of one user, alice: the one revoked and the one kept.
type liveGateway struct {
	*testGateway
	node          *testNode
	auditPath     string
	alice, alice2 *api.Client
	aliceKey      string
	sandbox       string
}

func startLiveGateway(t *testing.T, recheck time.Duration) *liveGateway {
	t.Helper()
	n1 := startNodeWith(t, "n1", func(f *fake.Backend) backend.Backend { return &tunnelBackend{Backend: f} },
		append([]string{api.CapTunnel}, allCaps...)...)
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	tg := startGateway(t, func(c *Config) {
		c.Audit = NewAuditLog(auditPath)
		c.AccessRecheckInterval = recheck
	}, n1)
	scopes := []string{ScopeRead, ScopeCreate, ScopeDelete}
	secret, k, err := tg.store.CreateKey("alice", "", scopes)
	if err != nil {
		t.Fatal(err)
	}
	lg := &liveGateway{testGateway: tg, node: n1, auditPath: auditPath, aliceKey: k.ID,
		alice:  api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client()),
		alice2: tg.client("alice", "", scopes...)}
	sb, err := lg.alice.CreateSandbox(ctxT(t), api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	lg.sandbox = sb.ID
	return lg
}

// sawPaths is how many requests the node was sent whose path contains s.
func (tn *testNode) sawPaths(s string) int {
	tn.mu.Lock()
	defer tn.mu.Unlock()
	n := 0
	for _, p := range tn.paths {
		if strings.Contains(p, s) {
			n++
		}
	}
	return n
}

// ended reports whether ch is sent on within a few seconds.
func ended(ch <-chan error) (error, bool) {
	select {
	case err := <-ch:
		return err, true
	case <-time.After(5 * time.Second):
		return nil, false
	}
}

// revokedEntries is the api.revoked entries the audit log holds.
func revokedEntries(t *testing.T, path string) []api.AuditEntry {
	t.Helper()
	_, all := readAudit(t, path)
	var out []api.AuditEntry
	for _, e := range all {
		if e.Action == "api.revoked" {
			out = append(out, e)
		}
	}
	return out
}

// A followed output stream (logs --follow) ends when its key is revoked;
// one of the same user's other key, on the same process, stays open.
func TestRevokeEndsAFollowedOutputStream(t *testing.T) {
	lg := startLiveGateway(t, time.Hour)
	ctx := ctxT(t)
	pr, err := lg.alice.StartProcess(ctx, lg.sandbox, api.RunRequest{Argv: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	follow := func(c *api.Client) (chan error, context.CancelFunc) {
		fctx, cancel := context.WithCancel(ctx)
		ch := make(chan error, 1)
		go func() { ch <- c.FollowOutput(fctx, lg.sandbox, pr.PID, func(api.OutputEvent) error { return nil }) }()
		return ch, cancel
	}
	revoked, cancel1 := follow(lg.alice)
	defer cancel1()
	kept, cancel2 := follow(lg.alice2)
	defer cancel2()
	out := fmt.Sprintf("/processes/%d/output", pr.PID)
	waitFor(t, "both streams to reach the node", func() bool { return lg.node.sawPaths(out) == 2 })

	if err := lg.admin.RevokeKey(ctx, lg.aliceKey); err != nil {
		t.Fatal(err)
	}
	if _, ok := ended(revoked); !ok {
		t.Fatal("a followed output stream stayed open after its key was revoked")
	}
	select {
	case err := <-kept:
		t.Fatalf("the stream of the user's other key ended: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	// The kept stream is still the node's: the process's exit reaches it.
	if err := lg.alice2.Signal(ctx, lg.sandbox, pr.PID, "TERM"); err != nil {
		t.Fatal(err)
	}
	if err, ok := ended(kept); !ok || err != nil {
		t.Fatalf("the kept stream did not end with its process: %v (ended %v)", err, ok)
	}
	es := revokedEntries(t, lg.auditPath)
	if len(es) != 1 {
		t.Fatalf("api.revoked entries = %+v; want one", es)
	}
	e := es[0]
	if e.KeyID != lg.aliceKey || e.User != "alice" || e.Sandbox != lg.sandbox || e.Node != "n1" ||
		e.Target != "GET /v1/sandboxes/{ref}/processes/{pid}/output" || e.Result != "closed" {
		t.Errorf("entry = %+v", e)
	}
}

// An attached process ends for its client when the key is revoked: the
// upgraded connection is closed, not left to the client.
func TestRevokeEndsAnAttachSession(t *testing.T) {
	lg := startLiveGateway(t, time.Hour)
	ctx := ctxT(t)
	pr, err := lg.alice.StartProcess(ctx, lg.sandbox, api.RunRequest{Argv: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := lg.alice.Attach(ctx, lg.sandbox, pr.PID)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := st.Copy(outW, io.Discard)
		outW.CloseWithError(err)
		done <- err
	}()
	// The session is live: what is written comes back.
	if _, err := st.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(outR, buf); err != nil || string(buf) != "ping\n" {
		t.Fatalf("precondition: the attach session echoes: %q %v", buf, err)
	}
	go func() { _, _ = io.Copy(io.Discard, outR) }()

	if err := lg.admin.RevokeKey(ctx, lg.aliceKey); err != nil {
		t.Fatal(err)
	}
	if _, ok := ended(done); !ok {
		t.Fatal("an attach session stayed open after its key was revoked")
	}
	if es := revokedEntries(t, lg.auditPath); len(es) != 1 || es[0].Target != "GET /v1/sandboxes/{ref}/processes/{pid}/attach" {
		t.Errorf("api.revoked entries = %+v", es)
	}
}

// A tunnel to a guest port ends when its key is revoked, here through the
// periodic recheck: the key is revoked in the store, not through the API.
func TestRecheckEndsATunnel(t *testing.T) {
	lg := startLiveGateway(t, 50*time.Millisecond)
	ctx := ctxT(t)
	keep, err := lg.alice2.Tunnel(ctx, lg.sandbox, 8080)
	if err != nil {
		t.Fatal(err)
	}
	defer keep.Close()
	tun, err := lg.alice.Tunnel(ctx, lg.sandbox, 8080)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	echo := func(c io.ReadWriter) error {
		if _, err := c.Write([]byte("hi")); err != nil {
			return err
		}
		b := make([]byte, 2)
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		if string(b) != "hi" {
			return fmt.Errorf("echoed %q", b)
		}
		return nil
	}
	if err := echo(tun); err != nil {
		t.Fatalf("precondition: the tunnel echoes: %v", err)
	}

	if err := lg.store.RevokeKey(lg.aliceKey); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, tun); done <- err }()
	if _, ok := ended(done); !ok {
		t.Fatal("a tunnel stayed open after its key was revoked")
	}
	if err := echo(keep); err != nil {
		t.Errorf("the tunnel of the user's other key was disturbed: %v", err)
	}
}
