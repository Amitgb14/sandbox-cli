package gateway

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

const week = 7 * 86400

// bobSignedIn registers bob's invite (policy demo, the account ending in a
// week) and returns his sign-in.
func bobSignedIn(t *testing.T, tg *testGateway) *api.Client {
	t.Helper()
	reg, err := tg.anon().Register(ctxT(t), api.RegisterRequest{Token: inviteBob(t, tg), Password: bobPassword})
	if err != nil {
		t.Fatal(err)
	}
	return tg.signedIn(api.Login{Secret: reg.Secret})
}

func (tg *testGateway) withSecret(secret string) *api.Client {
	return api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client())
}

// A signed-in user makes a key for a program: it acts as them, under their
// policy, with only the scopes they gave it, and is listed with its name.
func TestAPersonalKeyActsAsItsUser(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	bob := bobSignedIn(t, tg)

	k, err := bob.CreatePersonalKey(ctx, api.CreatePersonalKeyRequest{Name: " ci ", Scopes: []string{ScopeRead, ScopeCreate}, ExpiresInSecs: 86400})
	if err != nil {
		t.Fatal(err)
	}
	if k.Secret == "" || k.Name != "ci" || !k.Personal || k.User != "bob" || k.Policy != "demo" || k.Expires == nil ||
		time.Until(*k.Expires) > 24*time.Hour {
		t.Fatalf("the key: %+v", k)
	}
	ci := tg.withSecret(k.Secret)
	who, err := ci.Whoami(ctx)
	if err != nil || who.User != "bob" || who.Policy == nil || who.Policy.Name != "demo" {
		t.Fatalf("whoami with it: %+v, %v", who, err)
	}
	sb, err := ci.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil || sb.LifetimeSecs != 1800 {
		t.Fatalf("a sandbox made with it: lifetime %d, %v; want the policy's", sb.LifetimeSecs, err)
	}
	// Not given sandbox:delete, it cannot terminate, even bob's own.
	wantCode(t, ci.TerminateSandbox(ctx, sb.ID), api.CodeRefused)
	if err := bob.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}

	list, err := bob.PersonalKeys(ctx)
	if err != nil || len(list) != 1 || list[0].ID != k.ID || list[0].Name != "ci" {
		t.Fatalf("bob's keys: %+v, %v", list, err)
	}
	// The admin sees it, beside bob's account.
	all, err := tg.admin.Keys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range all {
		found = found || (a.ID == k.ID && a.Personal && a.Name == "ci")
	}
	if !found {
		t.Errorf("the admin's list lacks it: %+v", all)
	}
}

// A personal key is never more than what made it, and makes nothing.
func TestAPersonalKeyIsLessThanItsMaker(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	bob := bobSignedIn(t, tg)
	ok := api.CreatePersonalKeyRequest{Name: "ci", Scopes: []string{ScopeRead}, ExpiresInSecs: 86400}

	for name, c := range map[string]struct {
		req  api.CreatePersonalKeyRequest
		code string
	}{
		"the admin scope":         {api.CreatePersonalKeyRequest{Name: "x", Scopes: []string{ScopeAdmin}, ExpiresInSecs: 86400}, api.CodeRefused},
		"a scope bob lacks":       {api.CreatePersonalKeyRequest{Name: "x", Scopes: []string{ScopeSSH}, ExpiresInSecs: 86400}, api.CodeRefused},
		"no scopes":               {api.CreatePersonalKeyRequest{Name: "x", ExpiresInSecs: 86400}, api.CodeInvalidRequest},
		"an unknown scope":        {api.CreatePersonalKeyRequest{Name: "x", Scopes: []string{"everything"}, ExpiresInSecs: 86400}, api.CodeInvalidRequest},
		"no expiry":               {api.CreatePersonalKeyRequest{Name: "x", Scopes: []string{ScopeRead}}, api.CodeInvalidRequest},
		"under an hour":           {api.CreatePersonalKeyRequest{Name: "x", Scopes: []string{ScopeRead}, ExpiresInSecs: 60}, api.CodeInvalidRequest},
		"over a year":             {api.CreatePersonalKeyRequest{Name: "x", Scopes: []string{ScopeRead}, ExpiresInSecs: 400 * 86400}, api.CodeInvalidRequest},
		"past the account's end":  {api.CreatePersonalKeyRequest{Name: "x", Scopes: []string{ScopeRead}, ExpiresInSecs: 2 * week}, api.CodeInvalidRequest},
		"no name":                 {api.CreatePersonalKeyRequest{Name: "  ", Scopes: []string{ScopeRead}, ExpiresInSecs: 86400}, api.CodeInvalidRequest},
		"a name with a line feed": {api.CreatePersonalKeyRequest{Name: "a\nb", Scopes: []string{ScopeRead}, ExpiresInSecs: 86400}, api.CodeInvalidRequest},
	} {
		if _, err := bob.CreatePersonalKey(ctx, c.req); !api.IsCode(err, c.code) {
			t.Errorf("%s: %v; want %s", name, err, c.code)
		}
	}

	k, err := bob.CreatePersonalKey(ctx, ok)
	if err != nil {
		t.Fatal(err)
	}
	ci := tg.withSecret(k.Secret)
	_, err = ci.CreatePersonalKey(ctx, ok)
	wantCode(t, err, api.CodeRefused)
	_, err = ci.PersonalKeys(ctx)
	wantCode(t, err, api.CodeRefused)
	wantCode(t, ci.RevokePersonalKey(ctx, k.ID), api.CodeRefused)
	wantCode(t, ci.ChangePassword(ctx, api.PasswordRequest{Current: bobPassword, New: "taken over now"}), api.CodeInvalidRequest)
	wantCode(t, ci.Logout(ctx), api.CodeInvalidRequest)
	if _, err := ci.Whoami(ctx); err != nil {
		t.Errorf("signing out with a personal key ended it: %v", err)
	}

	for i := 1; i < maxPersonalKeys; i++ {
		if _, err := bob.CreatePersonalKey(ctx, ok); err != nil {
			t.Fatalf("key %d: %v", i+1, err)
		}
	}
	_, err = bob.CreatePersonalKey(ctx, ok)
	wantCode(t, err, api.CodeConflict)
}

// Its user revokes it; nobody else can, or see it.
func TestRevokingAPersonalKey(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	bob := bobSignedIn(t, tg)
	k, err := bob.CreatePersonalKey(ctx, api.CreatePersonalKeyRequest{Name: "ci", Scopes: []string{ScopeRead}, ExpiresInSecs: 86400})
	if err != nil {
		t.Fatal(err)
	}
	carol := tg.client("carol", "carol", ScopeRead, ScopeCreate)
	wantCode(t, carol.RevokePersonalKey(ctx, k.ID), api.CodeNotFound)
	if theirs, err := carol.PersonalKeys(ctx); err != nil || len(theirs) != 0 {
		t.Fatalf("carol's keys: %+v, %v", theirs, err)
	}
	if err := bob.RevokePersonalKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	_, err = tg.withSecret(k.Secret).Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
	if list, _ := bob.PersonalKeys(ctx); len(list) != 1 || !list[0].Revoked {
		t.Errorf("a revoked key is listed, revoked: %+v", list)
	}
}

// What stops the account stops its personal keys: a revocation, a
// suspension (for good: a restore does not bring them back), an expiry,
// an admin's password reset. Signing out and changing the password do not.
func TestAPersonalKeyEndsWithItsAccount(t *testing.T) {
	ctx := ctxT(t)
	make := func(t *testing.T) (*testGateway, *api.Client, Key, *api.Client) {
		tg := startGateway(t, nil, startNode(t, "n1"))
		bob := bobSignedIn(t, tg)
		k, err := bob.CreatePersonalKey(ctx, api.CreatePersonalKeyRequest{Name: "ci", Scopes: []string{ScopeRead}, ExpiresInSecs: 3 * 86400})
		if err != nil {
			t.Fatal(err)
		}
		acct, _ := tg.store.Account("bob@example.com")
		return tg, bob, acct, tg.withSecret(k.Secret)
	}

	t.Run("sign-out and a new password keep it", func(t *testing.T) {
		_, bob, _, ci := make(t)
		if err := bob.ChangePassword(ctx, api.PasswordRequest{Current: bobPassword, New: "another password"}); err != nil {
			t.Fatal(err)
		}
		if err := bob.Logout(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := ci.Whoami(ctx); err != nil {
			t.Fatalf("after a sign-out and a password change: %v", err)
		}
	})
	t.Run("revoked", func(t *testing.T) {
		tg, _, acct, ci := make(t)
		if err := tg.admin.RevokeKey(ctx, acct.ID); err != nil {
			t.Fatal(err)
		}
		_, err := ci.Whoami(ctx)
		wantCode(t, err, api.CodeUnauthorized)
	})
	t.Run("suspended and restored", func(t *testing.T) {
		tg, _, acct, ci := make(t)
		if _, err := tg.admin.SuspendKey(ctx, acct.ID); err != nil {
			t.Fatal(err)
		}
		_, err := ci.Whoami(ctx)
		wantCode(t, err, api.CodeUnauthorized)
		if _, err := tg.admin.RestoreKey(ctx, acct.ID); err != nil {
			t.Fatal(err)
		}
		_, err = ci.Whoami(ctx)
		wantCode(t, err, api.CodeUnauthorized)
	})
	t.Run("shortened, and renewed by an admin", func(t *testing.T) {
		tg, bob, acct, ci := make(t)
		if _, err := tg.admin.SetKeyExpiry(ctx, acct.ID, 3600); err != nil {
			t.Fatal(err)
		}
		list, _ := bob.PersonalKeys(ctx)
		if len(list) != 1 || list[0].Expires == nil || time.Until(*list[0].Expires) > time.Hour {
			t.Fatalf("after the account was shortened to an hour: %+v", list)
		}
		// An admin renewing the personal key alone cannot take it past
		// the account.
		got, err := tg.admin.SetKeyExpiry(ctx, list[0].ID, 0)
		if err != nil || got.Expires == nil || time.Until(*got.Expires) > time.Hour {
			t.Fatalf("renewed for ever: %+v, %v; want the account's hour", got, err)
		}
		if _, err := tg.admin.SetKeyExpiry(ctx, acct.ID, -1); err != nil {
			t.Fatal(err)
		}
		_, err = ci.Whoami(ctx)
		wantCode(t, err, api.CodeUnauthorized)
	})
	t.Run("an admin's reset", func(t *testing.T) {
		tg, _, acct, ci := make(t)
		reset, err := tg.admin.ResetPassword(ctx, acct.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tg.anon().Register(ctx, api.RegisterRequest{Token: reset.Token, Password: "a new password"}); err != nil {
			t.Fatal(err)
		}
		_, err = ci.Whoami(ctx)
		wantCode(t, err, api.CodeUnauthorized)
	})
	t.Run("an admin suspends it alone", func(t *testing.T) {
		tg, bob, _, _ := make(t)
		list, _ := bob.PersonalKeys(ctx)
		_, err := tg.admin.SuspendKey(ctx, list[0].ID)
		wantCode(t, err, api.CodeInvalidRequest)
	})
}

// A user with a plain key, no account, makes personal keys under it, and
// they end with it.
func TestAPersonalKeyUnderAPlainKey(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	secret, dana := tg.keyed("dana", "dana", ScopeRead, ScopeCreate)
	k, err := dana.CreatePersonalKey(ctx, api.CreatePersonalKeyRequest{Name: "laptop", Scopes: []string{ScopeRead}, ExpiresInSecs: 86400})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tg.withSecret(k.Secret).Whoami(ctx); err != nil {
		t.Fatal(err)
	}
	root, ok := tg.store.KeyBySecret(secret)
	if !ok {
		t.Fatal("dana's key")
	}
	if err := tg.admin.RevokeKey(ctx, root.ID); err != nil {
		t.Fatal(err)
	}
	_, err = tg.withSecret(k.Secret).Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
}

// A personal key acts in the organisation it was made in, and nowhere
// else: not its user's first one, not another they are in, asked or not.
// It is listed there, and stops when its user leaves the organisation.
func TestAPersonalKeyActsOnlyInItsOrganisation(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	dana := tg.client("dana", "dana", ScopeRead, ScopeCreate, ScopeOrgCreate)
	mustOrg(t, dana, "acme")
	erinKey, erin := tg.keyed("erin", "erin", ScopeRead, ScopeCreate)
	if _, err := dana.SetOrgMember(ctx, "acme", api.OrgMemberRequest{User: "erin", Tenant: "erin", Role: api.RoleMember}); err != nil {
		t.Fatal(err)
	}
	_ = erinKey
	req := api.CreatePersonalKeyRequest{Name: "ci", Scopes: []string{ScopeRead, ScopeCreate}, ExpiresInSecs: 86400}
	inAcme, err := dana.WithOrg("acme").CreatePersonalKey(ctx, req)
	if err != nil || inAcme.Org != "acme" {
		t.Fatalf("a key made in acme: %+v, %v", inAcme, err)
	}
	atHome, err := dana.CreatePersonalKey(ctx, req)
	if err != nil || atHome.Org != "dana" {
		t.Fatalf("a key made at home: %+v, %v", atHome, err)
	}

	// Unasked, it acts in acme: what it makes is acme's.
	ci := tg.withSecret(inAcme.Secret)
	if w, err := ci.Whoami(ctx); err != nil || w.Org != "acme" {
		t.Fatalf("whoami with the acme key: %+v, %v", w, err)
	}
	sb, err := ci.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !sandboxIDs(t, dana.WithOrg("acme"))[sb.ID] || sandboxIDs(t, dana)[sb.ID] {
		t.Errorf("the acme key's sandbox is not acme's alone")
	}
	// Asked for elsewhere, it is refused.
	for _, org := range []string{"dana", "default", "team-x"} {
		_, err := ci.WithOrg(org).Sandboxes(ctx)
		wantCode(t, err, api.CodeRefused)
	}
	if _, err := ci.WithOrg("ACME").Sandboxes(ctx); err != nil {
		t.Errorf("acme by another case: %v", err)
	}
	// The home key does not reach acme either.
	_, err = tg.withSecret(atHome.Secret).WithOrg("acme").Sandboxes(ctx)
	wantCode(t, err, api.CodeRefused)

	// Each organisation lists its own.
	if l, err := dana.WithOrg("acme").PersonalKeys(ctx); err != nil || len(l) != 1 || l[0].ID != inAcme.ID {
		t.Errorf("acme's keys: %+v, %v", l, err)
	}
	if l, err := dana.PersonalKeys(ctx); err != nil || len(l) != 1 || l[0].ID != atHome.ID {
		t.Errorf("dana's own keys: %+v, %v", l, err)
	}

	// A member's key in acme ends with their membership.
	ek, err := erin.WithOrg("acme").CreatePersonalKey(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tg.withSecret(ek.Secret).Sandboxes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := dana.RemoveOrgMember(ctx, "acme", "erin", "erin"); err != nil {
		t.Fatal(err)
	}
	_, err = tg.withSecret(ek.Secret).Sandboxes(ctx)
	wantCode(t, err, api.CodeNotFound)
	// And one cannot be made in an organisation its maker is not in.
	_, err = erin.WithOrg("acme").CreatePersonalKey(ctx, req)
	wantCode(t, err, api.CodeNotFound)
}

// The super admin makes personal keys for their own work, with any scope
// but admin, in an organisation they are in; selecting another as an
// admin does not make them a member of it.
func TestASuperAdminMakesPersonalKeys(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	amit := tg.client("amit", "amit", ScopeAdmin)
	k, err := amit.CreatePersonalKey(ctx, api.CreatePersonalKeyRequest{Name: "work", Scopes: []string{ScopeRead, ScopeCreate, ScopeSSH}, ExpiresInSecs: 86400})
	if err != nil {
		t.Fatal(err)
	}
	w, err := tg.withSecret(k.Secret).Whoami(ctx)
	if err != nil || w.User != "amit" || w.Org != "amit" || slices.Contains(w.Scopes, ScopeAdmin) {
		t.Fatalf("the admin's personal key: %+v, %v", w, err)
	}
	_, err = tg.withSecret(k.Secret).Keys(ctx)
	wantCode(t, err, api.CodeRefused)
	_, err = amit.CreatePersonalKey(ctx, api.CreatePersonalKeyRequest{Name: "x", Scopes: []string{ScopeAdmin}, ExpiresInSecs: 86400})
	wantCode(t, err, api.CodeInvalidRequest)

	dana := tg.client("dana", "dana", ScopeRead, ScopeCreate, ScopeOrgCreate)
	mustOrg(t, dana, "acme")
	_, err = amit.WithOrg("acme").CreatePersonalKey(ctx, api.CreatePersonalKeyRequest{Name: "x", Scopes: []string{ScopeRead}, ExpiresInSecs: 86400})
	wantCode(t, err, api.CodeRefused)
}

// A sign-up's own organisation is theirs: they own it, can add members to
// it, and it does not count against the organisations they may make.
func TestASignUpOwnsItsFirstOrganisation(t *testing.T) {
	tg := startGateway(t, func(c *Config) { c.MaxOrgsPerUser = 1 }, startNode(t, "n1"))
	ctx := ctxT(t)
	iv, err := tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "neo@example.com", User: "neo",
		Scopes: []string{ScopeRead, ScopeCreate, ScopeOrgCreate}})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := tg.anon().Register(ctx, api.RegisterRequest{Token: iv.Token, Password: bobPassword})
	if err != nil {
		t.Fatal(err)
	}
	neo := tg.signedIn(reg)
	orgs, err := neo.Orgs(ctx)
	if err != nil || len(orgs) != 1 || orgs[0].Name != "neo" || orgs[0].Role != api.RoleOwner {
		t.Fatalf("neo's organisations: %+v, %v", orgs, err)
	}
	tg.client("trin", "trin", ScopeRead)
	if _, err := neo.SetOrgMember(ctx, "neo", api.OrgMemberRequest{User: "trin", Tenant: "trin", Role: api.RoleMember}); err != nil {
		t.Fatalf("adding a member to one's first organisation: %v", err)
	}
	if ms, err := neo.OrgMembers(ctx, "neo"); err != nil || len(ms) != 2 {
		t.Errorf("neo's members: %+v, %v", ms, err)
	}
	mustOrg(t, neo, "zion") // the one it may make
	// The admin sees it among every organisation.
	found := false
	for _, o := range tg.store.Orgs() {
		found = found || o.Name == "neo"
	}
	if !found {
		t.Error("neo's organisation is not among the gateway's")
	}
	// A tenant an admin issues plain keys into stays private.
	tg.client("ci", "ci", ScopeRead)
	if kind := sqlValue(t, tg.store, "SELECT kind FROM orgs WHERE key = 'ci'"); kind != orgPersonal {
		t.Errorf("a plain key's tenant is %q; want personal", kind)
	}
}

// Migration 8 gives each account that existed its first organisation, and
// binds the personal keys there were to their user's.
func TestMigration8OwnsAccountsAndBindsKeys(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	bob := bobSignedIn(t, tg)
	k, err := bob.CreatePersonalKey(ctx, api.CreatePersonalKeyRequest{Name: "ci", Scopes: []string{ScopeRead}, ExpiresInSecs: 86400})
	if err != nil {
		t.Fatal(err)
	}
	tg.client("ci", "ci", ScopeRead)
	// Back to how migration 7 left them.
	tg.store.exec("UPDATE api_keys SET org_id = NULL")
	tg.store.exec("DELETE FROM org_members")
	tg.store.exec("UPDATE orgs SET kind = ?, created_by = NULL WHERE key <> ''", orgPersonal)
	body, err := migrationFiles.ReadFile("migrations/sqlite/0008_org_bound_keys.sql")
	if err != nil {
		t.Fatal(err)
	}
	stmts := strings.Split(string(body), ";")
	for _, s := range stmts[1:] { // the column is there
		if strings.TrimSpace(s) != "" {
			tg.store.exec(s)
		}
	}
	if kind := sqlValue(t, tg.store, "SELECT kind FROM orgs WHERE key = 'bob'"); kind != orgTeam {
		t.Errorf("bob's tenant is %q; want team", kind)
	}
	if kind := sqlValue(t, tg.store, "SELECT kind FROM orgs WHERE key = 'ci'"); kind != orgPersonal {
		t.Errorf("ci's tenant is %q; want personal", kind)
	}
	if role, ok, _ := tg.store.Store.MemberRole(ctx, "bob", "bob", "bob"); !ok || role != api.RoleOwner {
		t.Errorf("bob in bob: %q %v", role, ok)
	}
	if w, err := tg.withSecret(k.Secret).Whoami(ctx); err != nil || w.Org != "bob" {
		t.Errorf("bob's key after the migration: %+v, %v", w, err)
	}
	if got, _, _ := tg.store.Store.KeyBySecret(ctx, k.Secret); !got.Bound || got.Org != "bob" {
		t.Errorf("bob's key is bound to %v %q", got.Bound, got.Org)
	}
}
