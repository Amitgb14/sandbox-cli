package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

const (
	agentCred      = `{"claudeAiOauth":{"accessToken":"sk-ant-oat-first-do-not-leak","refreshToken":"rt-first"}}`
	agentCredFresh = `{"claudeAiOauth":{"accessToken":"sk-ant-oat-fresh-do-not-leak","refreshToken":"rt-fresh"}}`
	credPath       = "/sandbox/home/.claude/.credentials.json"
	claudeJSONPath = "/sandbox/home/.claude.json"
)

// loginClient is a user's client and the id of the user its key belongs to.
func (tg *testGateway) loginClient(user, tenant string) (*api.Client, string) {
	tg.t.Helper()
	secret, k, err := tg.store.CreateKey(user, tenant, []string{ScopeRead, ScopeCreate, ScopeDelete})
	if err != nil {
		tg.t.Fatal(err)
	}
	uid, ok, err := tg.store.Store.UserOfKey(context.Background(), k.ID)
	if err != nil || !ok {
		tg.t.Fatalf("user of key %s: %v, %v", k.ID, ok, err)
	}
	return api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client()), uid
}

func readGuest(t *testing.T, c *api.Client, sb, p string) string {
	t.Helper()
	b, err := c.ReadFile(ctxT(t), sb, p)
	if err != nil {
		t.Fatalf("read %s in %s: %v", p, sb, err)
	}
	return string(b)
}

// A user's agent login is copied into the sandboxes they ask for it in,
// saved back from them when it changes, and reaches no one else: not
// another organisation, and not a member of their own.
func TestAnAgentLoginIsKeptForItsUserAlone(t *testing.T) {
	tg := startGateway(t, withKey, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana, anaID := tg.loginClient("ana", "aa")
	cy, cyID := tg.loginClient("cy", "aa") // in ana's organisation
	bo, _ := tg.loginClient("bo", "bb")

	// The first sandbox has nothing to restore; ana logs in inside it.
	sb1, err := ana.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if kept, err := ana.KeepAgentLogin(ctx, sb1.ID, "claude"); err != nil || kept.Restored {
		t.Fatalf("first keep: %+v, %v", kept, err)
	}
	if err := ana.WriteFile(ctx, sb1.ID, credPath, []byte(agentCred)); err != nil {
		t.Fatal(err)
	}
	// ~/.claude.json carries the account, and — written by the guest — an
	// MCP server, a command the agent would start, which is never kept.
	if err := ana.WriteFile(ctx, sb1.ID, claudeJSONPath, []byte(`{"oauthAccount":{"emailAddress":"ana@example.com"},"mcpServers":{"x":{"command":"/tmp/evil"}}}`)); err != nil {
		t.Fatal(err)
	}
	tg.g.saveAgentLogins(ctx)

	list, err := ana.AgentLogins(ctx)
	if err != nil || len(list) != 1 || list[0].Agent != "claude" || strings.Join(list[0].Files, ",") != ".claude.json,.claude/.credentials.json" || list[0].Updated.IsZero() {
		t.Fatalf("logins = %+v, %v", list, err)
	}
	sec, _, _ := tg.store.CreateKey("ana", "aa", []string{ScopeRead})
	resp := tg.raw(http.MethodGet, "/v1/agent-logins", sec, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "do-not-leak") {
		t.Fatalf("the listing holds the token: %s", body)
	}
	if bytes.Contains(tg.store.rawBytes(), []byte("do-not-leak")) {
		t.Fatal("the store holds the login's plaintext")
	}

	// The next sandbox starts logged in, without the MCP server.
	sb2, err := ana.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if kept, err := ana.KeepAgentLogin(ctx, sb2.ID, "claude"); err != nil || !kept.Restored {
		t.Fatalf("second keep: %+v, %v", kept, err)
	}
	if got := readGuest(t, ana, sb2.ID, credPath); got != agentCred {
		t.Fatalf("restored credentials = %q", got)
	}
	if got := readGuest(t, ana, sb2.ID, claudeJSONPath); strings.Contains(got, "mcpServers") || !strings.Contains(got, "oauthAccount") {
		t.Fatalf("restored ~/.claude.json = %s", got)
	}

	// No one else's sandbox gets it: not another organisation's user, not
	// a member of ana's own.
	for name, c := range map[string]*api.Client{"bo": bo, "cy": cy} {
		sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if kept, err := c.KeepAgentLogin(ctx, sb.ID, "claude"); err != nil || kept.Restored {
			t.Fatalf("%s was given a login: %+v, %v", name, kept, err)
		}
		if _, err := c.ReadFile(ctx, sb.ID, credPath); err == nil {
			t.Fatalf("%s's sandbox holds credentials", name)
		}
		if l, err := c.AgentLogins(ctx); err != nil || len(l) != 0 {
			t.Fatalf("%s sees %+v, %v", name, l, err)
		}
		// Nor can they have it kept from ana's sandbox.
		_, err = c.KeepAgentLogin(ctx, sb2.ID, "claude")
		wantCode(t, err, api.CodeNotFound)
	}

	// Sealed to its user: moved to another, it does not open, and the
	// refusal does not quote it.
	sealed, _, err := tg.store.Store.AgentLogin(ctx, anaID, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := tg.store.Store.PutAgentLogin(ctx, cyID, "claude", sealed); err != nil {
		t.Fatal(err)
	}
	sbc, _ := cy.CreateSandbox(ctx, api.CreateSandboxRequest{})
	_, err = cy.KeepAgentLogin(ctx, sbc.ID, "claude")
	if err == nil || strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("a login moved to another user: %v", err)
	}
	if err := cy.ForgetAgentLogin(ctx, "claude"); err != nil {
		t.Fatal(err)
	}

	// The agent refreshes its token in sb2, and sb2 is deleted before the
	// next periodic save: the delete saves it first.
	if err := ana.WriteFile(ctx, sb2.ID, credPath, []byte(agentCredFresh)); err != nil {
		t.Fatal(err)
	}
	if err := ana.TerminateSandbox(ctx, sb2.ID); err != nil {
		t.Fatal(err)
	}
	// sb1 still holds the login it saved, now stale: unchanged since, it is
	// not put back over the fresh one.
	tg.g.saveAgentLogins(ctx)
	sb3, _ := ana.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if _, err := ana.KeepAgentLogin(ctx, sb3.ID, "claude"); err != nil {
		t.Fatal(err)
	}
	if got := readGuest(t, ana, sb3.ID, credPath); got != agentCredFresh {
		t.Fatalf("after the refresh, the next sandbox got %q", got)
	}
	// The deleted sandbox is no longer kept from.
	if _, ok, _ := tg.store.Store.AgentLoginKeep(ctx, sb2.ID); ok {
		t.Fatal("a deleted sandbox is still kept from")
	}

	// Forgotten, it is gone and no sandbox puts it back.
	if err := ana.ForgetAgentLogin(ctx, "claude"); err != nil {
		t.Fatal(err)
	}
	if err := ana.WriteFile(ctx, sb1.ID, credPath, []byte(`{"claudeAiOauth":{"accessToken":"again"}}`)); err != nil {
		t.Fatal(err)
	}
	tg.g.saveAgentLogins(ctx)
	if l, _ := ana.AgentLogins(ctx); len(l) != 0 {
		t.Fatalf("after forgetting: %+v", l)
	}
	wantCode(t, ana.ForgetAgentLogin(ctx, "claude"), api.CodeNotFound)
}

// What a guest writes is read back within bounds: an oversized file is not
// kept, and an agent that keeps no login files, or none known, is refused.
func TestAnAgentLoginIsReadWithinBounds(t *testing.T) {
	tg := startGateway(t, withKey, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana, anaID := tg.loginClient("ana", "aa")
	sb, err := ana.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"no-such-agent", "../claude", ""} {
		_, err := ana.KeepAgentLogin(ctx, sb.ID, agent)
		wantCode(t, err, api.CodeInvalidRequest)
	}
	if _, err := ana.KeepAgentLogin(ctx, sb.ID, "claude"); err != nil {
		t.Fatal(err)
	}
	if err := ana.WriteFile(ctx, sb.ID, credPath, bytes.Repeat([]byte("x"), maxAgentLoginFile+1)); err != nil {
		t.Fatal(err)
	}
	tg.g.saveAgentLogins(ctx)
	if _, ok, _ := tg.store.Store.AgentLogin(ctx, anaID, "claude"); ok {
		t.Fatal("an oversized login file was kept")
	}

	// A sandbox ended on its node, not through the gateway (an idle
	// timeout), is no longer kept from.
	if err := tg.g.nodes.get("n1").client.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	tg.g.saveAgentLogins(ctx)
	if _, ok, _ := tg.store.Store.AgentLoginKeep(ctx, sb.ID); ok {
		t.Fatal("an ended sandbox is still kept from")
	}
}

// Without a secrets key there is nowhere sealed to keep a login.
func TestAgentLoginsNeedASecretsKey(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana, _ := tg.loginClient("ana", "aa")
	sb, err := ana.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ana.KeepAgentLogin(ctx, sb.ID, "claude")
	wantCode(t, err, api.CodeUnsupported)
	_, err = ana.AgentLogins(ctx)
	wantCode(t, err, api.CodeUnsupported)
	// The delete goes on as before.
	if err := ana.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
}
