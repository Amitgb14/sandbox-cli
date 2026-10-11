package gateway

import (
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A suspension is a revocation at once, which an admin may take back for an
// hour: the account is refused everywhere, its sign-ins end, and its
// address stays its own; restored, it works again; past the hour, it is
// revoked for good.
func TestASuspendedAccountIsRefusedAndMayBeRestoredForAnHour(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	token := inviteBob(t, tg)
	anon := tg.anon()
	if _, err := anon.Register(ctx, api.RegisterRequest{Token: token, Password: bobPassword}); err != nil {
		t.Fatal(err)
	}
	l, err := anon.Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword})
	if err != nil {
		t.Fatal(err)
	}
	bob := tg.signedIn(l)
	acct, ok := tg.store.Account("bob@example.com")
	if !ok {
		t.Fatal("no account")
	}

	k, err := tg.admin.SuspendKey(ctx, acct.ID)
	if err != nil || !k.Suspended || !k.Revoked || k.RestoreUntil == nil ||
		time.Until(*k.RestoreUntil) < 59*time.Minute || time.Until(*k.RestoreUntil) > time.Hour {
		t.Fatalf("suspend: %+v, %v", k, err)
	}
	_, err = bob.Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
	_, err = anon.Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword})
	wantCode(t, err, api.CodeUnauthorized)
	// Its address and user are still its own.
	_, err = tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "bob@example.com", User: "bob2", Scopes: demoScopes})
	wantCode(t, err, api.CodeConflict)
	_, err = tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "other@example.com", User: "bob", Tenant: "bob", Scopes: demoScopes})
	wantCode(t, err, api.CodeConflict)
	_, err = tg.admin.SuspendKey(ctx, acct.ID)
	wantCode(t, err, api.CodeConflict)

	if _, err := tg.admin.RestoreKey(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	// The account is back; the sign-in it had stays ended.
	_, err = bob.Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
	l, err = anon.Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword})
	if err != nil {
		t.Fatalf("signing in once restored: %v", err)
	}
	if who, err := tg.signedIn(l).Whoami(ctx); err != nil || who.User != "bob" {
		t.Fatalf("whoami once restored: %+v, %v", who, err)
	}
	_, err = tg.admin.RestoreKey(ctx, acct.ID)
	wantCode(t, err, api.CodeConflict) // not suspended

	// Past the hour, it is revoked for good.
	if _, err := tg.admin.SuspendKey(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	tg.store.setNow(func() time.Time { return time.Now().Add(61 * time.Minute) })
	_, err = tg.admin.RestoreKey(ctx, acct.ID)
	wantCode(t, err, api.CodeConflict)
	tg.store.setNow(time.Now)
	keys, err := tg.admin.Keys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k.ID == acct.ID && (!k.Revoked || !k.Suspended) {
			t.Errorf("listed while suspended: %+v", k)
		}
	}
}

// A revocation for good ends a suspension's grace; a sign-in's key and the
// admin's own key or account are not suspended.
func TestSuspendRefusals(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	_, k, err := tg.store.CreateKey("carl", "", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tg.admin.SuspendKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if err := tg.admin.RevokeKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	_, err = tg.admin.RestoreKey(ctx, k.ID)
	wantCode(t, err, api.CodeConflict)

	rootSecret, root, err := tg.store.CreateKey("root", "", []string{ScopeAdmin})
	if err != nil {
		t.Fatal(err)
	}
	rootc := api.NewClientWithHTTP(tg.ts.URL, rootSecret, tg.ts.Client())
	_, err = rootc.SuspendKey(ctx, root.ID)
	wantCode(t, err, api.CodeConflict)
	_, err = rootc.SuspendKey(ctx, "key_nope")
	wantCode(t, err, api.CodeNotFound)
	_, err = tg.user("dave").SuspendKey(ctx, root.ID)
	wantCode(t, err, api.CodeRefused)

	token := inviteBob(t, tg)
	if _, err := tg.anon().Register(ctx, api.RegisterRequest{Token: token, Password: bobPassword}); err != nil {
		t.Fatal(err)
	}
	l, err := tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword})
	if err != nil {
		t.Fatal(err)
	}
	signIn, ok := tg.store.KeyBySecret(l.Secret)
	if !ok {
		t.Fatal("no sign-in key")
	}
	_, err = rootc.SuspendKey(ctx, signIn.ID)
	wantCode(t, err, api.CodeInvalidRequest)
}
