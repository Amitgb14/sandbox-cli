package gateway

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A sandbox whose owner can no longer act on it — removed from its
// organisation, or suspended — is listed for the admin with why, and only
// such ones; the admin ends it by id, and it leaves the list.
func TestOrphanedSandboxesAreListedForTheAdmin(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	alice := tg.client("alice", "alice", ScopeRead, ScopeCreate)
	dana := tg.client("dana", "dana", ScopeRead, ScopeCreate, ScopeOrgCreate)
	_, bob := tg.keyed("bob", "bob", ScopeRead, ScopeCreate)
	carolKey, carol := tg.keyed("carol", "carol", ScopeRead, ScopeCreate)
	mustOrg(t, dana, "acme")
	if _, err := dana.SetOrgMember(ctx, "acme", api.OrgMemberRequest{User: "bob", Tenant: "bob", Role: api.RoleMember}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{}); err != nil {
		t.Fatal(err)
	}
	bobs, err := bob.WithOrg("acme").CreateSandbox(ctx, api.CreateSandboxRequest{Name: "left"})
	if err != nil {
		t.Fatal(err)
	}
	carols, err := carol.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if l, err := tg.admin.Orphans(ctx); err != nil || len(l) != 0 {
		t.Fatalf("before anyone left: %+v, %v", l, err)
	}

	if err := dana.RemoveOrgMember(ctx, "acme", "bob", "bob"); err != nil {
		t.Fatal(err)
	}
	k, _ := tg.store.KeyBySecret(carolKey)
	if _, err := tg.admin.SuspendKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	l, err := tg.admin.Orphans(ctx)
	if err != nil || len(l) != 2 {
		t.Fatalf("orphans: %+v, %v; want bob's and carol's", l, err)
	}
	byID := map[string]api.OrphanSandbox{}
	for _, o := range l {
		byID[o.ID] = o
	}
	if o := byID[bobs.ID]; o.User != "bob" || o.Org != "acme" || o.Name != "left" || o.Reason != "its owner is no longer a member of acme" {
		t.Errorf("bob's: %+v", o)
	}
	if o := byID[carols.ID]; o.User != "carol" || o.Reason == "" || o.Reason == byID[bobs.ID].Reason {
		t.Errorf("carol's: %+v", o)
	}
	if st, err := tg.admin.Stats(ctx); err != nil || st.Sandboxes.Orphaned != 2 {
		t.Errorf("stats count %d orphans, %v; want 2", st.Sandboxes.Orphaned, err)
	}

	if err := tg.admin.TerminateSandbox(ctx, bobs.ID); err != nil {
		t.Fatal(err)
	}
	if l, err := tg.admin.Orphans(ctx); err != nil || len(l) != 1 || l[0].ID != carols.ID {
		t.Errorf("after ending bob's: %+v, %v", l, err)
	}
	_, err = alice.Orphans(ctx)
	wantCode(t, err, api.CodeRefused)
}
