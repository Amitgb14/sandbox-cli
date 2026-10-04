package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Authorisation is the router's alone; these tests hold the HTTP front and
// the router to it together.

func TestAnotherUsersSandboxIsNotFound(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...))
	alice, bob := tg.user("alice"), tg.user("bob")
	ctx := ctxT(t)
	sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if sb.Labels[LabelOwner] != "alice" {
		t.Fatalf("labels %v: the owner was not stamped", sb.Labels)
	}
	// Not found, never forbidden: bob cannot even learn the id exists.
	for _, ref := range []string{sb.ID, "web"} {
		_, err := bob.Sandbox(ctx, ref)
		wantCode(t, err, api.CodeNotFound)
		_, err = bob.Run(ctx, ref, api.RunRequest{Argv: []string{"true"}})
		wantCode(t, err, api.CodeNotFound)
		wantCode(t, bob.TerminateSandbox(ctx, ref), api.CodeNotFound)
		_, err = bob.ReadFile(ctx, ref, "/tmp/x")
		wantCode(t, err, api.CodeNotFound)
		_, err = bob.Events(ctx, ref)
		wantCode(t, err, api.CodeNotFound)
		_, err = bob.Attach(ctx, ref, 1)
		wantCode(t, err, api.CodeNotFound)
	}
	if list, _ := bob.Sandboxes(ctx); len(list) != 0 {
		t.Fatalf("bob lists %d sandboxes", len(list))
	}
	// A label filter naming alice does not widen bob's listing.
	if list, _ := bob.Sandboxes(ctx, LabelOwner+"=alice"); len(list) != 0 {
		t.Fatalf("bob lists alice's sandboxes by label: %d", len(list))
	}
	// Bob may have his own "web": names are per user.
	bsb, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web"})
	if err != nil {
		t.Fatalf("bob's own web: %v", err)
	}
	got, err := bob.Sandbox(ctx, "web")
	if err != nil || got.ID != bsb.ID {
		t.Fatalf("bob's web resolves to %+v, %v", got, err)
	}
	got, err = alice.Sandbox(ctx, "web")
	if err != nil || got.ID != sb.ID {
		t.Fatalf("alice's web resolves to %+v, %v", got, err)
	}
	// The admin sees both, and acts on either by id.
	list, err := tg.admin.Sandboxes(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("admin lists %d, %v", len(list), err)
	}
	if _, err := tg.admin.Sandbox(ctx, sb.ID); err != nil {
		t.Fatalf("admin get: %v", err)
	}
}

// The same user name in another tenant is another user.
func TestTenantsAreSeparate(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	a := tg.client("sam", "acme", ScopeRead, ScopeCreate, ScopeDelete)
	b := tg.client("sam", "globex", ScopeRead, ScopeCreate, ScopeDelete)
	sb, err := a.CreateSandbox(ctxT(t), api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if sb.Labels[LabelTenant] != "acme" {
		t.Fatalf("labels %v", sb.Labels)
	}
	_, err = b.Sandbox(ctxT(t), sb.ID)
	wantCode(t, err, api.CodeNotFound)
}

// The owner labels decide who may act on a sandbox; a request that sets one
// is refused, fail closed, not quietly overwritten.
func TestOwnerLabelsInARequestAreRefused(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	alice := tg.user("alice")
	for _, l := range []map[string]string{
		{LabelOwner: "bob"}, {LabelTenant: "acme"}, {"gateway.anything": "x"},
	} {
		_, err := alice.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Labels: l})
		wantCode(t, err, api.CodeInvalidRequest)
	}
	if n := tg.nodes[0].count(); n != 0 {
		t.Fatalf("a refused create reached the node: %d sandboxes", n)
	}
}

func TestScopesAreEnforced(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	owner := tg.user("alice")
	sb, err := owner.CreateSandbox(ctxT(t), api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	reader := tg.client("alice", "", ScopeRead)
	ctx := ctxT(t)
	if _, err := reader.Sandbox(ctx, sb.ID); err != nil {
		t.Fatalf("read with read scope: %v", err)
	}
	_, err = reader.CreateSandbox(ctx, api.CreateSandboxRequest{})
	wantCode(t, err, api.CodeRefused)
	_, err = reader.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"true"}})
	wantCode(t, err, api.CodeRefused)
	wantCode(t, reader.WriteFile(ctx, sb.ID, "/tmp/x", []byte("x")), api.CodeRefused)
	wantCode(t, reader.TerminateSandbox(ctx, sb.ID), api.CodeRefused)
	_, err = reader.Attach(ctx, sb.ID, 1)
	wantCode(t, err, api.CodeRefused)

	// A missing scope is refused before the sandbox is looked up, so it
	// says nothing about whether one exists.
	_, err = reader.Run(ctx, "sbx_0000000000000000", api.RunRequest{Argv: []string{"true"}})
	wantCode(t, err, api.CodeRefused)

	creator := tg.client("alice", "", ScopeCreate)
	_, err = creator.Sandbox(ctx, sb.ID)
	wantCode(t, err, api.CodeRefused)
	_, err = creator.Sandboxes(ctx)
	wantCode(t, err, api.CodeRefused)

	_, err = owner.Sandbox(ctx, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Admin endpoints need admin.
	resp := tg.raw(http.MethodGet, "/v1/admin/keys", "", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin keys with no key: %d", resp.StatusCode)
	}
}

func TestCredentialIsRequired(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	secret, k, err := tg.store.CreateKey("alice", "", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "sgk_wrong", "node-token-n1-0123456789"} {
		resp := tg.raw(http.MethodGet, "/v1/sandboxes", key, "")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), `"unauthorized"`) {
			t.Errorf("key %q: %d %s", key, resp.StatusCode, body)
		}
	}
	resp := tg.raw(http.MethodGet, "/v1/health", "", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health without a key: %d", resp.StatusCode)
	}
	resp = tg.raw(http.MethodGet, "/v1/sandboxes", secret, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a good key: %d", resp.StatusCode)
	}
	if err := tg.store.RevokeKey(k.ID); err != nil {
		t.Fatal(err)
	}
	resp = tg.raw(http.MethodGet, "/v1/sandboxes", secret, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a revoked key: %d", resp.StatusCode)
	}
}

func TestRequestGuard(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	secret, _, _ := tg.store.CreateKey("alice", "", []string{ScopeRead, ScopeCreate})
	// A page on another origin is refused before its credential is looked at.
	resp := tg.raw(http.MethodPost, "/v1/sandboxes", secret, "{}", "Origin", "https://evil.example")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin create: %d", resp.StatusCode)
	}
	// A JSON body needs its content type, so a cross-origin form post cannot
	// stay "simple".
	resp = tg.raw(http.MethodPost, "/v1/sandboxes", secret, "{}", "Content-Type", "text/plain")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain body: %d", resp.StatusCode)
	}
	resp = tg.raw(http.MethodPost, "/v1/sandboxes", secret, `{"name":"x","privileged":true}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: %d", resp.StatusCode)
	}
	resp = tg.raw(http.MethodGet, "/v1/nothing", secret, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown endpoint: %d", resp.StatusCode)
	}
	if tg.nodes[0].count() != 0 {
		t.Fatal("a refused request created a sandbox")
	}
}

// Resolve is what the SSH front calls; its rules are the HTTP front's.
func TestResolve(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	sb, err := tg.user("alice").CreateSandbox(ctxT(t), api.CreateSandboxRequest{Name: "box"})
	if err != nil {
		t.Fatal(err)
	}
	alice := Principal{User: "alice", Scopes: []string{ScopeSSH}}
	ctx := context.Background()
	for _, ref := range []string{sb.ID, "box"} {
		id, c, err := tg.g.Resolve(ctx, alice, ref, ScopeSSH)
		if err != nil || id != sb.ID || c == nil {
			t.Fatalf("Resolve(%s) = %s, %v, %v", ref, id, c, err)
		}
		if _, err := c.Sandbox(ctx, id); err != nil {
			t.Fatalf("the client Resolve returned does not reach the node: %v", err)
		}
	}
	for _, c := range []struct {
		name  string
		p     Principal
		ref   string
		scope string
		want  error
	}{
		{"no user", Principal{Scopes: []string{ScopeSSH}}, sb.ID, ScopeSSH, ErrUnauthenticated},
		{"no scope", Principal{User: "alice", Scopes: []string{ScopeRead}}, sb.ID, ScopeSSH, ErrForbidden},
		{"empty scope", alice, sb.ID, "", ErrForbidden},
		{"other user", Principal{User: "bob", Scopes: []string{ScopeSSH}}, sb.ID, ScopeSSH, ErrNotFound},
		{"other user by name", Principal{User: "bob", Scopes: []string{ScopeSSH}}, "box", ScopeSSH, ErrNotFound},
		{"other tenant", Principal{User: "alice", Tenant: "x", Scopes: []string{ScopeSSH}}, sb.ID, ScopeSSH, ErrNotFound},
		{"confined elsewhere", Principal{User: "alice", Scopes: []string{ScopeSSH}, Sandbox: "sbx_0000000000000000"}, sb.ID, ScopeSSH, ErrNotFound},
		{"unknown id", alice, "sbx_0000000000000000", ScopeSSH, ErrNotFound},
		{"not a reference", alice, "../etc", ScopeSSH, ErrNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := tg.g.Resolve(ctx, c.p, c.ref, c.scope); !errors.Is(err, c.want) {
				t.Fatalf("err = %v; want %v", err, c.want)
			}
		})
	}
	// Confined to this sandbox: allowed.
	if _, _, err := tg.g.Resolve(ctx, Principal{User: "alice", Scopes: []string{ScopeSSH}, Sandbox: sb.ID}, sb.ID, ScopeSSH); err != nil {
		t.Fatal(err)
	}
	// An admin reaches it by id.
	if _, _, err := tg.g.Resolve(ctx, Principal{User: "root", Scopes: []string{ScopeAdmin}}, sb.ID, ScopeSSH); err != nil {
		t.Fatal(err)
	}
}

// An id naming a node other than the one the store recorded is routed
// nowhere: the store is the authority, and a disagreement means something
// is wrong.
func TestIDNamingAnotherNodeIsRefused(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...))
	id := "sbx_n2_0123456789abcdef"
	if err := tg.store.SetOwner(id, Owner{User: "alice", Node: "n1"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := tg.g.Resolve(context.Background(), Principal{User: "alice", Scopes: []string{ScopeRead}}, id, ScopeRead)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// The router authorises the sandbox the path names; the path is forwarded
// decoded, so an encoded walk out of it (..%2F) must not reach the node as a
// path to another sandbox.
func TestEncodedPathWalkIsRefused(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	mine, err := tg.user("alice").CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := tg.user("bob").CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	secret, _, _ := tg.store.CreateKey("alice", "", []string{ScopeRead, ScopeCreate})
	for _, p := range []string{
		"/v1/sandboxes/" + mine.ID + "/processes/..%2F..%2F" + theirs.ID + "%2Fprocesses%2F1/output",
		"/v1/sandboxes/" + mine.ID + "/processes/..%2F..%2F" + theirs.ID,
		"/v1/sandboxes/" + mine.ID + "/processes/%2E%2E%2F%2E%2E%2F" + theirs.ID + "%2Fevents/output",
	} {
		resp := tg.raw(http.MethodGet, p, secret, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	if tg.nodes[0].sawPath(theirs.ID) {
		t.Fatal("a path naming bob's sandbox reached the node on alice's authority")
	}
}

// A lifetime so large it overflows a duration is refused, not wrapped.
func TestSSHAccessHugeTTLIsRefused(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	tg.g.SetSSH(fakeSSH{})
	if _, err := tg.user("alice").CreateSandbox(ctxT(t), api.CreateSandboxRequest{Name: "box"}); err != nil {
		t.Fatal(err)
	}
	secret, _, _ := tg.store.CreateKey("alice", "", []string{ScopeSSH})
	if code := tg.do(secret, http.MethodPost, "/v1/sandboxes/box/ssh-access", api.SSHAccessRequest{TTLSecs: 9223372037}, nil); code != http.StatusBadRequest {
		t.Fatalf("ttl_secs that overflows: %d", code)
	}
}
