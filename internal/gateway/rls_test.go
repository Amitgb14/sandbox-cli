package gateway

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The second wall: on PostgreSQL a store held to a tenant (ForTenant) sees
// that tenant's rows only, even through a query that asks for every
// tenant's — what a forgotten filter in a future query would be. The
// gateway's own checks are the first wall, and are tested everywhere else.
func TestRowLevelSecurityHoldsATenantToItsRows(t *testing.T) {
	if os.Getenv("SANDBOX_TEST_POSTGRES_DSN") == "" {
		t.Skip("row-level security is PostgreSQL's: set SANDBOX_TEST_POSTGRES_DSN (make test-postgres)")
	}
	st := newTestStore(t)
	ctx := context.Background()
	for _, r := range []struct{ id, tenant, user string }{{"sbx_a1", "acme", "ann"}, {"sbx_a2", "acme", "art"}, {"sbx_b1", "globex", "bob"}} {
		if err := st.Store.RecordSandbox(ctx, r.id, Owner{User: r.user, Tenant: r.tenant, Node: "n1"}, api.NodeResources{CPUs: 1}, &api.Sandbox{ID: r.id, Name: r.id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutSecret("globex", "TOKEN", []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	if err := st.Store.PutRegistryLogin(ctx, "globex", "ghcr.io", "bob", []byte("sealed")); err != nil {
		t.Fatal(err)
	}

	acme := st.Store.ForTenant("acme")
	// AllTenant leaves out the tenant filter, as a mistaken query would.
	rows, err := acme.SandboxHistory(ctx, SandboxFilter{AllTenant: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("acme sees %d sandboxes; want its own 2: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Owner.Tenant != "acme" {
			t.Errorf("acme sees %s, of %s", r.ID, r.Owner.Tenant)
		}
	}
	if _, ok, err := acme.OwnerOf(ctx, "sbx_b1"); err != nil || ok {
		t.Errorf("acme found globex's sandbox: %v %v", ok, err)
	}
	if _, ok, err := acme.SealedSecret(ctx, "globex", "TOKEN"); err != nil || ok {
		t.Errorf("acme read globex's secret by naming its tenant: %v %v", ok, err)
	}
	if _, _, ok, err := acme.RegistryLogin(ctx, "globex", "ghcr.io"); err != nil || ok {
		t.Errorf("acme read globex's registry login by naming its tenant: %v %v", ok, err)
	}
	if l, err := acme.RegistryLoginsOf(ctx, "globex"); err != nil || len(l) != 0 {
		t.Errorf("acme listed globex's registry logins: %+v %v", l, err)
	}
	if u, err := acme.UsageOf(ctx, "globex"); err != nil || u.Sandboxes != 0 {
		t.Errorf("acme counted globex's usage: %+v %v", u, err)
	}
	// Writing another tenant's row is refused by the policy's check.
	err = acme.RecordSandbox(ctx, "sbx_x", Owner{User: "eve", Tenant: "globex", Node: "n1"}, api.NodeResources{}, nil)
	if err == nil {
		t.Error("acme recorded a sandbox in globex")
	}
	// History: a terminated sandbox stays listed until the sweep.
	if err := st.Store.ForgetSandbox(ctx, "sbx_a1"); err != nil {
		t.Fatal(err)
	}
	if n, err := st.Store.PruneSandboxHistory(ctx, time.Now().Add(time.Minute)); err != nil || n != 1 {
		t.Errorf("pruned %d, %v; want the one terminated", n, err)
	}
	if err := st.Store.RecordSandbox(ctx, "sbx_a1", Owner{User: "ann", Tenant: "acme", Node: "n1"}, api.NodeResources{CPUs: 1}, nil); err != nil {
		t.Fatal(err)
	}
	// The gateway's own work across tenants is the system store's.
	if all, err := st.Store.SandboxHistory(ctx, SandboxFilter{AllTenant: true}); err != nil || len(all) != 3 {
		t.Errorf("the system store sees %d, %v; want 3", len(all), err)
	}
}
