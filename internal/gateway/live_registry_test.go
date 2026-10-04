package gateway

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The registry of open forwarded requests: what live_test.go cannot see
// from outside.

// Normal traffic leaves nothing registered: every forwarded request
// deregisters when it ends, whether it succeeded, was refused by the node or
// streamed to its end. A leak would keep every principal ever seen and make
// each recheck slower.
func TestLiveRegistryEmptiesAfterNormalRequests(t *testing.T) {
	lg := startLiveGateway(t, time.Hour)
	ctx := ctxT(t)
	if _, err := lg.alice.Sandbox(ctx, lg.sandbox); err != nil {
		t.Fatal(err)
	}
	if _, err := lg.alice.Run(ctx, lg.sandbox, api.RunRequest{Argv: []string{"echo", "hi"}}); err != nil {
		t.Fatal(err)
	}
	if err := lg.alice.WriteFile(ctx, lg.sandbox, "/sandbox/home/f", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := lg.alice.ReadFile(ctx, lg.sandbox, "/sandbox/home/missing"); err == nil {
		t.Fatal("read a file that does not exist")
	}
	pr, err := lg.alice.StartProcess(ctx, lg.sandbox, api.RunRequest{Argv: []string{"echo", "out"}})
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := lg.alice.FollowOutput(ctx, lg.sandbox, pr.PID, func(e api.OutputEvent) error {
		got.Write(e.Data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, err := lg.alice.Attach(ctx, lg.sandbox, pr.PID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Copy(&got, &got); err != nil {
		t.Fatal(err)
	}
	st.Close()
	// Not a tunnel: one stays open, and registered, until the guest's side
	// closes as well as the client's (the proxy waits for both halves), and
	// this test's guest port echoes for ever.
	// A client that gives up part way.
	cctx, cancel := context.WithCancel(ctx)
	sl, err := lg.alice.StartProcess(ctx, lg.sandbox, api.RunRequest{Argv: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		errc <- lg.alice.FollowOutput(cctx, lg.sandbox, sl.PID, func(api.OutputEvent) error { return nil })
	}()
	waitFor(t, "the follow to register", func() bool { return lg.g.live.len() == 1 })
	cancel()
	<-errc
	if err := lg.alice.TerminateSandbox(ctx, lg.sandbox); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the registry to empty", func() bool { return lg.g.live.len() == 0 })
	if n := lg.g.recheckLive(); n != 0 {
		t.Errorf("a recheck with nothing revoked ended %d requests", n)
	}
	if es := revokedEntries(t, lg.auditPath); len(es) != 0 {
		t.Errorf("api.revoked entries with nothing revoked: %+v", es)
	}
}

// What a key may lose: being revoked, leaving the store, or the scope the
// request was resolved with.
func TestLiveLostAccess(t *testing.T) {
	p := Principal{User: "alice", Tenant: "t", KeyID: "k1", Scopes: []string{ScopeRead, ScopeCreate}}
	key := Key{ID: "k1", User: "alice", Tenant: "t", Scopes: []string{ScopeRead, ScopeCreate}}
	for _, c := range []struct {
		name  string
		scope string
		mod   func(*Key)
		lost  bool
	}{
		{"unchanged", ScopeCreate, nil, false},
		{"revoked", ScopeRead, func(k *Key) { k.Revoked = true }, true},
		{"gone", ScopeRead, func(k *Key) { k.ID = "k2" }, true},
		{"other user", ScopeRead, func(k *Key) { k.User = "bob" }, true},
		{"other tenant", ScopeRead, func(k *Key) { k.Tenant = "" }, true},
		{"scope lost", ScopeCreate, func(k *Key) { k.Scopes = []string{ScopeRead} }, true},
		{"another scope lost", ScopeRead, func(k *Key) { k.Scopes = []string{ScopeRead} }, false},
		{"admin now", ScopeCreate, func(k *Key) { k.Scopes = []string{ScopeAdmin} }, false},
	} {
		k := key
		k.Scopes = append([]string(nil), key.Scopes...)
		if c.mod != nil {
			c.mod(&k)
		}
		why := liveLostAccess(&liveReq{p: p, scope: c.scope}, map[string]Key{k.ID: k})
		if (why != "") != c.lost {
			t.Errorf("%s: lost access = %q; want lost %v", c.name, why, c.lost)
		}
	}
}

// A request ended twice — a revocation's recheck and the backstop's finding
// it at once — is recorded once.
func TestLiveEndIsOnce(t *testing.T) {
	lg := startLiveGateway(t, time.Hour)
	_, cancel := context.WithCancelCause(context.Background())
	lr := &liveReq{p: Principal{User: "alice", KeyID: lg.aliceKey}, route: "GET x", cancel: cancel}
	if !lg.g.endLive(lr, "test") || lg.g.endLive(lr, "test") {
		t.Fatal("endLive did not end exactly once")
	}
	if es := revokedEntries(t, lg.auditPath); len(es) != 1 {
		t.Fatalf("api.revoked entries = %+v", es)
	}
}
