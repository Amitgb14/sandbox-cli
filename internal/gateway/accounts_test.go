package gateway

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A real work factor would make each of these tests take seconds.
func init() { passwordIterations = 1000 }

const bobPassword = "correct horse battery"

// anon is a client with no key, as hosted Studio is before a sign-in.
func (tg *testGateway) anon() *api.Client {
	return api.NewClientWithHTTP(tg.ts.URL, "", tg.ts.Client())
}

func (tg *testGateway) signedIn(l api.Login) *api.Client {
	return api.NewClientWithHTTP(tg.ts.URL, l.Secret, tg.ts.Client())
}

// inviteBob invites bob, of tenant bob, under policy demo, and returns the
// invite's token.
func inviteBob(t *testing.T, tg *testGateway) string {
	t.Helper()
	ctx := ctxT(t)
	if _, err := tg.admin.PutPolicy(ctx, api.UserPolicy{Name: "demo", MaxLifetimeSecs: 1800}); err != nil {
		t.Fatal(err)
	}
	iv, err := tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: " Bob@Example.com", User: "bob", Tenant: "bob",
		Scopes: demoScopes, Policy: "demo", ExpiresInSecs: 7 * 86400})
	if err != nil {
		t.Fatal(err)
	}
	if iv.Email != "bob@example.com" || iv.Token == "" || iv.Reset {
		t.Fatalf("the invite: %+v", iv)
	}
	return iv.Token
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) || e.Status != status {
		t.Fatalf("error = %v; want status %d", err, status)
	}
}

// The whole of it: an invite, a password set with it, a sign-in that acts
// as the user under their policy, a sign-out that ends it.
func TestInviteRegisterAndSignIn(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	token := inviteBob(t, tg)
	anon := tg.anon()

	info, err := anon.InviteFor(ctx, token)
	if err != nil || info.Email != "bob@example.com" || info.User != "bob" || info.Policy != "demo" {
		t.Fatalf("what the invite is for: %+v, %v", info, err)
	}
	_, err = anon.Register(ctx, api.RegisterRequest{Token: token, Password: "short"})
	wantCode(t, err, api.CodeInvalidRequest)
	reg, err := anon.Register(ctx, api.RegisterRequest{Token: token, Password: bobPassword})
	if err != nil || reg.User != "bob" || reg.Email != "bob@example.com" || reg.Secret == "" {
		t.Fatalf("register: %+v, %v", reg, err)
	}
	if time.Until(reg.Expires) > loginTTL || time.Until(reg.Expires) < loginTTL-time.Minute {
		t.Errorf("the sign-in lasts until %v; want twelve hours", reg.Expires)
	}
	_, err = anon.Register(ctx, api.RegisterRequest{Token: token, Password: bobPassword})
	wantCode(t, err, api.CodeNotFound)
	_, err = anon.InviteFor(ctx, token)
	wantCode(t, err, api.CodeNotFound)

	_, err = anon.Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: "not the password"})
	wantCode(t, err, api.CodeUnauthorized)
	_, err = anon.Login(ctx, api.LoginRequest{Email: "nobody@example.com", Password: bobPassword})
	wantCode(t, err, api.CodeUnauthorized)
	l, err := anon.Login(ctx, api.LoginRequest{Email: "BOB@example.com", Password: bobPassword, TTLSecs: 3600})
	if err != nil || time.Until(l.Expires) > time.Hour {
		t.Fatalf("sign-in: %+v, %v", l, err)
	}
	bob := tg.signedIn(l)
	who, err := bob.Whoami(ctx)
	if err != nil || who.User != "bob" || who.Tenant != "bob" || who.Policy == nil || who.Policy.Name != "demo" {
		t.Fatalf("whoami: %+v, %v", who, err)
	}
	if sb, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{}); err != nil || sb.LifetimeSecs != 1800 {
		t.Errorf("bob's sandbox: lifetime %d, %v; want the policy's", sb.LifetimeSecs, err)
	}

	// The account is listed, with its address; its sign-ins are not.
	keys, err := tg.admin.Keys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var listed []api.KeyInfo
	for _, k := range keys {
		if k.User == "bob" {
			listed = append(listed, k)
		}
	}
	if len(listed) != 1 || listed[0].Email != "bob@example.com" || listed[0].LastLogin == nil || listed[0].Policy != "demo" {
		t.Errorf("bob in the key list: %+v", listed)
	}

	if err := bob.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = bob.Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
	if _, err := tg.signedIn(reg).Whoami(ctx); err != nil {
		t.Errorf("signing out of one sign-in ended another: %v", err)
	}
	// Signing out is for a sign-in's key; an issued key is revoked.
	wantCode(t, tg.admin.Logout(ctx), api.CodeInvalidRequest)
}

// What is done to the account is done to its sign-ins: revoking it, or
// expiring it, ends them; a renewed account signs in again.
func TestAnAccountEndsItsSignIns(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	reg, err := tg.anon().Register(ctx, api.RegisterRequest{Token: inviteBob(t, tg), Password: bobPassword})
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := tg.store.Account("bob@example.com")

	if _, err := tg.admin.SetKeyExpiry(ctx, acct.ID, -1); err != nil {
		t.Fatal(err)
	}
	_, err = tg.signedIn(reg).Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
	_, err = tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword})
	wantCode(t, err, api.CodeRefused)

	if _, err := tg.admin.SetKeyExpiry(ctx, acct.ID, 86400); err != nil {
		t.Fatal(err)
	}
	l, err := tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword, TTLSecs: 7 * 86400})
	if err != nil {
		t.Fatalf("after a renewal: %v", err)
	}
	if time.Until(l.Expires) > 24*time.Hour {
		t.Errorf("a sign-in outlives its account: %v", l.Expires)
	}
	if err := tg.admin.RevokeKey(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	_, err = tg.signedIn(l).Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
	_, err = tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword})
	wantCode(t, err, api.CodeUnauthorized)
}

// A sign-in's key is refused once its account is not active, even if a
// change to the account missed it.
func TestASignInNeedsItsAccount(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	reg, err := tg.anon().Register(ctx, api.RegisterRequest{Token: inviteBob(t, tg), Password: bobPassword})
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := tg.store.Account("bob@example.com")
	// Revoke the account alone, as an older gateway's store would have.
	tg.store.exec("UPDATE api_keys SET revoked = ? WHERE id = ?", true, acct.ID)
	_, err = tg.signedIn(reg).Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
}

// Guessing is held back: an address locks after five failures, the right
// password included, and the lock says so.
func TestSignInLocksAfterFailures(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	if _, err := tg.anon().Register(ctx, api.RegisterRequest{Token: inviteBob(t, tg), Password: bobPassword}); err != nil {
		t.Fatal(err)
	}
	for range lockAfter {
		_, err := tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: "guess guess guess"})
		wantCode(t, err, api.CodeUnauthorized)
	}
	_, err := tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword})
	wantStatus(t, err, http.StatusTooManyRequests)

	// The gateway-wide bucket: many addresses, a failure each.
	tg.g.signIns = newSignInThrottle(time.Now)
	for i := range 20 {
		_, err := tg.anon().Login(ctx, api.LoginRequest{Email: "x" + string(rune('a'+i)) + "@example.com", Password: "guess guess guess"})
		wantCode(t, err, api.CodeUnauthorized)
	}
	_, err = tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword})
	wantStatus(t, err, http.StatusTooManyRequests)
}

// A reset sets a new password and ends every sign-in; the old password
// works until the reset is used. A change of password by the user ends the
// account's other sign-ins and keeps their own.
func TestPasswordResetAndChange(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	first, err := tg.anon().Register(ctx, api.RegisterRequest{Token: inviteBob(t, tg), Password: bobPassword})
	if err != nil {
		t.Fatal(err)
	}
	acct, _ := tg.store.Account("bob@example.com")
	reset, err := tg.admin.ResetPassword(ctx, acct.ID, false)
	if err != nil || !reset.Reset || reset.Email != "bob@example.com" {
		t.Fatalf("reset: %+v, %v", reset, err)
	}
	if _, err := tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword}); err != nil {
		t.Errorf("the old password before the reset is used: %v", err)
	}
	if _, err := tg.anon().Register(ctx, api.RegisterRequest{Token: reset.Token, Password: "a new password"}); err != nil {
		t.Fatal(err)
	}
	_, err = tg.signedIn(first).Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
	_, err = tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: bobPassword})
	wantCode(t, err, api.CodeUnauthorized)

	a, err := tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: "a new password"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := tg.anon().Login(ctx, api.LoginRequest{Email: "bob@example.com", Password: "a new password"})
	wantCode(t, tg.signedIn(a).ChangePassword(ctx, api.PasswordRequest{Current: "wrong password", New: "third password"}), api.CodeRefused)
	if err := tg.signedIn(a).ChangePassword(ctx, api.PasswordRequest{Current: "a new password", New: "third password"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tg.signedIn(a).Whoami(ctx); err != nil {
		t.Errorf("the sign-in that changed the password ended: %v", err)
	}
	_, err = tg.signedIn(b).Whoami(ctx)
	wantCode(t, err, api.CodeUnauthorized)
	wantCode(t, tg.admin.ChangePassword(ctx, api.PasswordRequest{Current: "x", New: "0123456789"}), api.CodeInvalidRequest)
}

// What an invite may not be, and who may make one.
func TestInviteRefusals(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	token := inviteBob(t, tg)

	// A second invite for the address replaces the first.
	again, err := tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "bob@example.com", User: "bob", Tenant: "bob", Scopes: demoScopes})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tg.anon().InviteFor(ctx, token)
	wantCode(t, err, api.CodeNotFound)
	if ivs, _ := tg.admin.Invites(ctx); len(ivs.Invites) != 1 || ivs.Invites[0].ID != again.ID {
		t.Errorf("pending: %+v", ivs)
	}
	if _, err := tg.anon().Register(ctx, api.RegisterRequest{Token: again.Token, Password: bobPassword}); err != nil {
		t.Fatal(err)
	}

	for name, req := range map[string]api.CreateInviteRequest{
		"an address with an account": {Email: "bob@example.com", User: "bob2", Scopes: demoScopes},
		"a user with an account":     {Email: "other@example.com", User: "bob", Tenant: "bob", Scopes: demoScopes},
	} {
		_, err := tg.admin.CreateInvite(ctx, req)
		if !api.IsCode(err, api.CodeConflict) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, req := range map[string]api.CreateInviteRequest{
		"not an address":    {Email: "bob", User: "x", Scopes: demoScopes},
		"no scopes":         {Email: "x@example.com", User: "x"},
		"no such policy":    {Email: "x@example.com", User: "x", Scopes: demoScopes, Policy: "nope"},
		"valid for a month": {Email: "x@example.com", User: "x", Scopes: demoScopes, ValidSecs: 30 * 86400},
	} {
		_, err := tg.admin.CreateInvite(ctx, req)
		if !api.IsCode(err, api.CodeInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	_, err = tg.user("erin").CreateInvite(ctx, api.CreateInviteRequest{Email: "x@example.com", User: "x", Scopes: demoScopes})
	wantCode(t, err, api.CodeRefused)
	_, err = tg.user("erin").ResetPassword(ctx, "key_x", false)
	wantCode(t, err, api.CodeRefused)

	// Withdrawn, and lapsed.
	iv, err := tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "carol@example.com", User: "carol", Scopes: demoScopes})
	if err != nil {
		t.Fatal(err)
	}
	if err := tg.admin.RevokeInvite(ctx, iv.ID); err != nil {
		t.Fatal(err)
	}
	_, err = tg.anon().Register(ctx, api.RegisterRequest{Token: iv.Token, Password: bobPassword})
	wantCode(t, err, api.CodeNotFound)
	iv, _ = tg.admin.CreateInvite(ctx, api.CreateInviteRequest{Email: "carol@example.com", User: "carol", Scopes: demoScopes, ValidSecs: 60})
	tg.store.setNow(func() time.Time { return time.Now().Add(2 * time.Minute) })
	_, err = tg.anon().Register(ctx, api.RegisterRequest{Token: iv.Token, Password: bobPassword})
	wantCode(t, err, api.CodeNotFound)
}

// Accounts and pending invites are kept across a restart.
func TestAccountsSurviveAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st := reopenTestStore(t, path)
	token, _, err := st.CreateInvite(api.CreateInviteRequest{Email: "bob@example.com", User: "bob", Scopes: demoScopes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RedeemInvite(token, hashPassword(bobPassword)); err != nil {
		t.Fatal(err)
	}
	pending, _, _ := st.CreateInvite(api.CreateInviteRequest{Email: "carol@example.com", User: "carol", Scopes: demoScopes})
	st.Close()
	st = reopenTestStore(t, path)
	defer st.Close()
	acct, ok := st.Account("bob@example.com")
	if !ok || !verifyPassword(acct.Password, bobPassword) {
		t.Errorf("bob's account after a reopen: %+v %v", acct, ok)
	}
	if _, ok := st.InviteByToken(pending); !ok {
		t.Error("a pending invite was lost")
	}
}
