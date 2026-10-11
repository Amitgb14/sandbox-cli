package cli

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/gateway"
)

// useGateway points the CLI at a real gateway, with no nodes, as an admin.
func useGateway(t *testing.T) *gateway.FileStore {
	t.Helper()
	st, err := gateway.OpenFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	g, err := gateway.New(gateway.Config{Store: st, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	g.Start(context.Background())
	t.Cleanup(g.Close)
	ts := httptest.NewServer(g.Handler())
	t.Cleanup(ts.Close)
	secret, _, err := st.CreateKey("root", "", []string{gateway.ScopeAdmin})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SANDBOX_CONTEXT", "")
	keyFile := filepath.Join(t.TempDir(), "admin.key")
	if err := os.WriteFile(keyFile, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cf := contextFile{Current: "gw", Contexts: map[string]endpointContext{"gw": {Endpoint: ts.URL, TokenFile: keyFile}}}
	if err := cf.save(); err != nil {
		t.Fatal(err)
	}
	return st
}

// An admin sets a policy, issues a demo user's key with it and an invite
// link, sees both listed, moves the user off it and removes it.
func TestGatewayKeysAndPolicies(t *testing.T) {
	st := useGateway(t)
	run := func(args ...string) string {
		t.Helper()
		out, err := runCLI(t, append([]string{"gateway"}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}

	if out := run("policies", "set", "demo", "--max-lifetime", "30m"); !strings.Contains(out, "max lifetime 30m0s") {
		t.Errorf("set: %q", out)
	}
	out := run("keys", "create", "--user", "bob", "--tenant", "bob", "--scope", "sandbox:read", "--scope", "sandbox:create",
		"--policy", "demo", "--invite-url", "https://studio.example.com/")
	if !strings.Contains(out, "policy: demo") || !regexp.MustCompile(`invite: https://studio\.example\.com/#key=sgk_\S+`).MatchString(out) {
		t.Errorf("create: %q", out)
	}
	if pol, ok := st.PolicyOf("bob", "bob"); !ok || pol.MaxLifetimeSecs != 1800 {
		t.Errorf("bob's policy in the store: %+v %v", pol, ok)
	}
	if out := run("keys", "list"); !regexp.MustCompile(`bob\s+bob\s+\S+\s+demo`).MatchString(out) {
		t.Errorf("keys list: %q", out)
	}
	if out := run("policies", "list"); !regexp.MustCompile(`demo\s+30m0s\s+bob@bob`).MatchString(out) {
		t.Errorf("policies list: %q", out)
	}

	if _, err := runCLI(t, "gateway", "policies", "remove", "demo"); err == nil {
		t.Error("removed a policy a user holds")
	}
	// An admin's invite link, for a Studio run with --allow-admin; a week.
	out, err := runCLI(t, "gateway", "keys", "create", "--user", "eve", "--scope", "admin", "--expires", "7d", "--invite-url", "https://studio.example.com")
	if err != nil || !strings.Contains(out, "#key=sgk_") || !strings.Contains(out, "--allow-admin") {
		t.Errorf("an admin's invite: %q %v", out, err)
	}
	var eve gateway.Key
	for _, k := range st.Keys() {
		if k.User == "eve" {
			eve = k
		}
	}
	if eve.Expires == nil || time.Until(*eve.Expires) < 6*24*time.Hour {
		t.Fatalf("eve's expiry: %v", eve.Expires)
	}
	if out := run("keys", "renew", eve.ID, "--expires", "never"); !strings.Contains(out, "expires never") {
		t.Errorf("renew never: %q", out)
	}
	if out := run("keys", "renew", eve.ID, "--expires", "now"); !strings.Contains(out, "expires 20") {
		t.Errorf("renew now: %q", out)
	}
	if out := run("keys", "list"); !regexp.MustCompile(`eve\s.*expired`).MatchString(out) {
		t.Errorf("an expired key listed: %q", out)
	}
	if _, err := runCLI(t, "gateway", "keys", "create", "--user", "x", "--scope", "sandbox:read", "--expires", "soon"); err == nil {
		t.Error("--expires soon was accepted")
	}
	if _, err := runCLI(t, "gateway", "policies", "assign", "bob"); err == nil {
		t.Error("assign with neither a policy nor --none")
	}
	run("policies", "assign", "bob", "--tenant", "bob", "--none")
	run("policies", "remove", "demo")
	if pols := st.Policies(); len(pols) != 0 {
		t.Errorf("left: %+v", pols)
	}

	var bobs []api.KeyInfo
	for _, k := range st.Keys() {
		if k.User == "bob" {
			bobs = append(bobs, api.KeyInfo{ID: k.ID})
		}
	}
	if out := run("keys", "revoke", bobs[0].ID); !strings.Contains(out, "revoked") {
		t.Errorf("revoke: %q", out)
	}
}

// An admin invites an address, the invite is listed until it is used, the
// account it makes is listed with its address, and a reset makes a new link.
func TestGatewayInvites(t *testing.T) {
	st := useGateway(t)
	run := func(args ...string) string {
		t.Helper()
		out, err := runCLI(t, append([]string{"gateway"}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	out := run("invites", "create", "--email", "Dana@Example.com", "--user", "dana", "--tenant", "dana",
		"--scope", "sandbox:read", "--expires", "7d", "--invite-url", "https://studio.example.com/")
	m := regexp.MustCompile(`link:\s+https://studio\.example\.com/#invite=(sgi_\S+)`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, "dana@example.com") {
		t.Fatalf("create: %q", out)
	}
	if out := run("invites", "list"); !regexp.MustCompile(`dana@example\.com\s+dana\s+dana\s+a new account`).MatchString(out) {
		t.Errorf("invites list: %q", out)
	}
	var pending string
	for _, iv := range st.Invites() {
		pending = iv.ID
	}
	out = run("invites", "resend", pending, "--invite-url", "https://studio.example.com")
	m2 := regexp.MustCompile(`link:\s+https://studio\.example\.com/#invite=(sgi_\S+)`).FindStringSubmatch(out)
	if m2 == nil || m2[1] == m[1] {
		t.Fatalf("resend: %q", out)
	}
	if _, ok := st.InviteByToken(m[1]); ok {
		t.Error("the old link still works after a resend")
	}
	m = m2
	acct, err := st.RedeemInvite(m[1], "pbkdf2-sha256$1$AAAA$AAAA")
	if err != nil {
		t.Fatal(err)
	}
	if out := run("invites", "list"); strings.Contains(out, "dana") {
		t.Errorf("a used invite is still listed: %q", out)
	}
	if out := run("keys", "list"); !regexp.MustCompile(acct.ID + `\s+dana\s.*active\s+dana@example\.com`).MatchString(out) {
		t.Errorf("the account in keys list: %q", out)
	}
	out = run("keys", "reset", acct.ID)
	if !strings.Contains(out, "a new password") || !strings.Contains(out, "token:   sgi_") {
		t.Errorf("reset: %q", out)
	}
	var id string
	for _, iv := range st.Invites() {
		id = iv.ID
	}
	run("invites", "revoke", id)
	if len(st.Invites()) != 0 {
		t.Error("the reset was not withdrawn")
	}
	if _, err := runCLI(t, "gateway", "invites", "create", "--user", "x", "--scope", "sandbox:read"); err == nil {
		t.Error("an invite with no --email")
	}
}
