package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

var demoScopes = []string{ScopeRead, ScopeCreate, ScopeDelete}

// demoGateway is a gateway with policy "demo" (30 minutes) and a user,
// dana, in her own tenant, issued through the admin API with it.
func demoGateway(t *testing.T) (*testGateway, *api.Client) {
	t.Helper()
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	if _, err := tg.admin.PutPolicy(ctx, api.UserPolicy{Name: "demo", MaxLifetimeSecs: 1800}); err != nil {
		t.Fatal(err)
	}
	k, err := tg.admin.CreateKey(ctx, api.CreateKeyRequest{User: "dana", Tenant: "dana", Scopes: demoScopes, Policy: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if k.Policy != "demo" {
		t.Fatalf("the issued key's policy: %q", k.Policy)
	}
	return tg, api.NewClientWithHTTP(tg.ts.URL, k.Secret, tg.ts.Client())
}

// A policy's user gets its lifetime when asking for none, may ask for less,
// and is refused more; an admin and a user without a policy are not held
// to it.
func TestPolicyHoldsAUsersLifetime(t *testing.T) {
	tg, dana := demoGateway(t)
	ctx := ctxT(t)

	sb, err := dana.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil || sb.LifetimeSecs != 1800 || sb.ExpiresAt == nil {
		t.Fatalf("asking for none: lifetime %d, expires %v, %v; want 1800", sb.LifetimeSecs, sb.ExpiresAt, err)
	}
	if sb, err := dana.CreateSandbox(ctx, api.CreateSandboxRequest{LifetimeSecs: 600}); err != nil || sb.LifetimeSecs != 600 {
		t.Errorf("asking for less: %d, %v", sb.LifetimeSecs, err)
	}
	_, err = dana.CreateSandbox(ctx, api.CreateSandboxRequest{LifetimeSecs: 1801})
	wantCode(t, err, api.CodeInvalidRequest)

	who, err := dana.Whoami(ctx)
	if err != nil || who.Policy == nil || who.Policy.Name != "demo" || who.Policy.MaxLifetimeSecs != 1800 {
		t.Errorf("whoami: %+v, %v", who.Policy, err)
	}

	for name, c := range map[string]*api.Client{"admin": tg.admin, "a user without a policy": tg.user("erin")} {
		if sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{}); err != nil || sb.LifetimeSecs != 0 {
			t.Errorf("%s: lifetime %d, %v; want none", name, sb.LifetimeSecs, err)
		}
		if sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{LifetimeSecs: 7200}); err != nil || sb.LifetimeSecs != 7200 {
			t.Errorf("%s asking for 2h: %d, %v", name, sb.LifetimeSecs, err)
		}
	}
}

// The policy follows its user into an organisation, and to the sandboxes a
// job of theirs makes, where the principal is made from a record and not a
// key.
func TestPolicyFollowsTheUserIntoAnOrg(t *testing.T) {
	tg, _ := demoGateway(t)
	ctx := ctxT(t)
	if pol, ok := tg.store.PolicyActingIn("dana", "dana"); !ok || pol.Name != "demo" {
		t.Errorf("dana in her own tenant: %+v %v", pol, ok)
	}
	_, owner := tg.keyed("olga", "", orgScopes...)
	mustOrg(t, owner, "lab")
	if _, err := owner.SetOrgMember(ctx, "lab", api.OrgMemberRequest{User: "dana", Tenant: "dana", Role: api.RoleMember}); err != nil {
		t.Fatal(err)
	}
	if pol, ok := tg.store.PolicyActingIn("dana", "lab"); !ok || pol.Name != "demo" {
		t.Errorf("dana in an organisation: %+v %v", pol, ok)
	}
	if _, ok := tg.store.PolicyActingIn("olga", "lab"); ok {
		t.Error("the organisation's owner has dana's policy")
	}
}

// The owner may bring a sandbox's end nearer, never push it out; the admin
// may do either. Without this check the owner could lengthen it to the
// node's own limit, past the policy.
func TestOnlyAnAdminExtendsALifetime(t *testing.T) {
	tg, dana := demoGateway(t)
	ctx := ctxT(t)
	sb, err := dana.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	life := func(n int) api.UpdateSandboxRequest { return api.UpdateSandboxRequest{LifetimeSecs: &n} }

	_, err = dana.UpdateSandbox(ctx, sb.ID, life(3600))
	wantCode(t, err, api.CodeRefused)
	got, err := dana.UpdateSandbox(ctx, sb.ID, life(900))
	if err != nil || got.LifetimeSecs != 900 {
		t.Fatalf("the owner shortening: %d, %v", got.LifetimeSecs, err)
	}
	_, err = dana.UpdateSandbox(ctx, sb.ID, life(1800))
	wantCode(t, err, api.CodeRefused)

	got, err = tg.admin.UpdateSandbox(ctx, sb.ID, life(3600))
	if want := sb.CreatedAt.Add(time.Hour); err != nil || got.LifetimeSecs != 3600 || got.ExpiresAt == nil || !got.ExpiresAt.Equal(want) {
		t.Fatalf("the admin extending: %d, %v, %v; want 3600 and %v", got.LifetimeSecs, got.ExpiresAt, err, want)
	}
	// Past the policy now, by the admin's hand; the owner may still shorten
	// it, to anything below what it has.
	if got, err := dana.UpdateSandbox(ctx, sb.ID, life(2400)); err != nil || got.LifetimeSecs != 2400 {
		t.Errorf("the owner shortening an extended sandbox: %d, %v", got.LifetimeSecs, err)
	}
}

// A change to a policy holds for the next sandbox; one running keeps its
// end. A policy a user holds cannot be removed from under them, and one
// that does not exist cannot be assigned.
func TestChangingAPolicy(t *testing.T) {
	tg, dana := demoGateway(t)
	ctx := ctxT(t)
	before, err := dana.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tg.admin.PutPolicy(ctx, api.UserPolicy{Name: "demo", MaxLifetimeSecs: 2700}); err != nil {
		t.Fatal(err)
	}
	after, err := dana.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil || after.LifetimeSecs != 2700 {
		t.Errorf("after the change: %d, %v", after.LifetimeSecs, err)
	}
	if got, _ := dana.Sandbox(ctx, before.ID); got.LifetimeSecs != 1800 {
		t.Errorf("a running sandbox's lifetime moved with the policy: %d", got.LifetimeSecs)
	}

	wantCode(t, tg.admin.DeletePolicy(ctx, "demo"), api.CodeConflict)
	wantCode(t, tg.admin.AssignPolicy(ctx, api.AssignPolicyRequest{User: "dana", Tenant: "dana", Policy: "nope"}), api.CodeInvalidRequest)
	_, err = tg.admin.CreateKey(ctx, api.CreateKeyRequest{User: "fay", Scopes: demoScopes, Policy: "nope"})
	wantCode(t, err, api.CodeInvalidRequest)
	for _, k := range tg.store.Keys() {
		if k.User == "fay" {
			t.Error("a key was issued with a policy that does not exist")
		}
	}

	if err := tg.admin.AssignPolicy(ctx, api.AssignPolicyRequest{User: "dana", Tenant: "dana"}); err != nil {
		t.Fatal(err)
	}
	if sb, err := dana.CreateSandbox(ctx, api.CreateSandboxRequest{}); err != nil || sb.LifetimeSecs != 0 {
		t.Errorf("with the policy cleared: %d, %v", sb.LifetimeSecs, err)
	}
	if err := tg.admin.DeletePolicy(ctx, "demo"); err != nil {
		t.Errorf("removing a policy nobody holds: %v", err)
	}
	if pols, _ := tg.admin.Policies(ctx); len(pols) != 0 {
		t.Errorf("policies left: %+v", pols)
	}
}

// Only an admin manages policies.
func TestPoliciesAreAdminOnly(t *testing.T) {
	tg, dana := demoGateway(t)
	ctx := ctxT(t)
	_, err := dana.Policies(ctx)
	wantCode(t, err, api.CodeRefused)
	_, err = dana.PutPolicy(ctx, api.UserPolicy{Name: "mine", MaxLifetimeSecs: 0})
	wantCode(t, err, api.CodeRefused)
	wantCode(t, dana.AssignPolicy(ctx, api.AssignPolicyRequest{User: "dana", Tenant: "dana"}), api.CodeRefused)
	wantCode(t, dana.DeletePolicy(ctx, "demo"), api.CodeRefused)
	_, err = tg.admin.PutPolicy(ctx, api.UserPolicy{Name: "Bad Name"})
	wantCode(t, err, api.CodeInvalidRequest)
	_, err = tg.admin.PutPolicy(ctx, api.UserPolicy{Name: "neg", MaxLifetimeSecs: -1})
	wantCode(t, err, api.CodeInvalidRequest)
}

// Policies and who holds them are kept across a restart, and a state file
// from before them loads.
func TestPoliciesSurviveAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version": 1, "keys": [], "nodes": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("a state file from before policies: %v", err)
	}
	if err := st.PutPolicy(api.UserPolicy{Name: "demo", MaxLifetimeSecs: 1800}); err != nil {
		t.Fatal(err)
	}
	if err := st.AssignPolicy("dana", "dana", "demo"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if pol, ok := st.PolicyOf("dana", "dana"); !ok || pol.MaxLifetimeSecs != 1800 {
		t.Errorf("after a reopen: %+v %v", pol, ok)
	}
	if _, ok := st.PolicyOf("dana", ""); ok {
		t.Error("a policy assigned to dana of tenant dana holds for dana of the default tenant")
	}
}
