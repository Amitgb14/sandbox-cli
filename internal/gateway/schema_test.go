package gateway

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The schema itself (migration 0003): every tenant an organisation, every
// user a row, and every record tied to both by foreign key, so the database
// holds who owns what even where a query forgot to.

// sqlValue is one value a query on the store's database returns, "" for
// none or NULL.
func sqlValue(t *testing.T, st *testStore, query string, args ...any) string {
	t.Helper()
	q := st.sql()
	var v sql.NullString
	err := q.db.QueryRow(q.rebind(query), args...).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("%s: %v", query, err)
	}
	return v.String
}

func TestEveryTenantIsAnOrganisation(t *testing.T) {
	st := newTestStore(t)
	kind := func(key string) string {
		return sqlValue(t, st, "SELECT kind FROM orgs WHERE key = ?", key)
	}
	if got := kind(""); got != orgDefault {
		t.Errorf("the default tenant's organisation is %q; want it there from the start, of kind default", got)
	}

	// A key into a tenant makes it a personal organisation, and its user a
	// user at home there.
	if _, _, err := st.CreateKey("bob", "Globex", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	if got := kind("Globex"); got != orgPersonal {
		t.Errorf("Globex is %q; want personal", got)
	}
	// Tenants that differ in case were different tenants, and stay so.
	if _, _, err := st.CreateKey("bob", "globex", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	if n := sqlValue(t, st, "SELECT COUNT(*) FROM orgs WHERE lower(key) = 'globex'"); n != "2" {
		t.Errorf("%s organisations for Globex and globex; want two", n)
	}
	// "default" is a tenant like any other, not the default one.
	if _, _, err := st.CreateKey("dan", "default", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	if got := kind("default"); got != orgPersonal {
		t.Errorf("the tenant named default is %q; want a personal organisation of its own", got)
	}

	// An invite makes the organisation it will put its account in.
	if _, _, err := st.CreateInvite(api.CreateInviteRequest{Email: "ivy@example.com", User: "ivy", Tenant: "initech", Scopes: []string{ScopeRead}}); err != nil {
		t.Fatal(err)
	}
	if got := kind("initech"); got != orgPersonal {
		t.Errorf("initech is %q; want personal", got)
	}
	// An organisation may still be made of it: nothing is in it yet.
	if _, _, err := st.CreateKey("ann", "", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrg("Initech", "ann", "", 0); err != nil {
		t.Fatalf("an organisation of a tenant only an invite named: %v", err)
	}
	if got := kind("initech"); got != orgTeam {
		t.Errorf("initech is %q; want team", got)
	}
	ann := sqlValue(t, st, "SELECT u.id FROM users u JOIN orgs o ON o.id = u.org_id WHERE u.name = 'ann' AND o.key = ''")
	if by := sqlValue(t, st, "SELECT created_by FROM orgs WHERE key = 'initech'"); by == "" || by != ann {
		t.Errorf("initech made by %q; want ann's user, %q", by, ann)
	}
	if role := sqlValue(t, st, "SELECT m.role FROM org_members m JOIN orgs o ON o.id = m.org_id WHERE o.key = 'initech' AND m.user_id = ?", ann); role != api.RoleOwner {
		t.Errorf("ann's membership: %q", role)
	}
	// Only team organisations are organisations to the API.
	if orgs := st.Orgs(); len(orgs) != 1 || orgs[0].Name != "Initech" {
		t.Errorf("Orgs: %+v; want Initech alone", orgs)
	}
	// One with keys in it may not be taken over.
	if _, err := st.CreateOrg("GLOBEX", "ann", "", 0); !errors.Is(err, ErrOrgExists) {
		t.Errorf("an organisation over a tenant with keys: %v", err)
	}
}

func TestRecordsBelongToAnOrganisationAndAUser(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := st.CreateKey("ann", "acme", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	o := Owner{User: "ann", Tenant: "acme", Node: "n1"}
	st.must(st.RecordSandbox("sbx_1", o, 1, 512))
	st.must(st.SetVolume("vol1", o))
	st.must(st.SetSnapshot("snap_1", o))
	st.must(st.PutSecret("acme", "TOKEN", []byte("sealed")))
	st.must(st.PutJob(&jobRecord{ID: "job_1", User: "ann", Tenant: "acme", Spec: api.JobSpec{Command: []string{"true"}, Completions: 1},
		State: api.JobRunning, Created: time.Now(), Runs: []api.JobRun{{}}}))
	st.must(st.PutService(serviceRecord{Name: "web", User: "ann", Tenant: "acme"}))

	acme := sqlValue(t, st, "SELECT id FROM orgs WHERE key = 'acme'")
	ann := sqlValue(t, st, "SELECT id FROM users WHERE name = 'ann' AND org_id = ?", acme)
	if acme == "" || ann == "" {
		t.Fatalf("acme %q, ann %q", acme, ann)
	}
	for _, table := range []string{"sandboxes", "volumes", "snapshots", "jobs", "services"} {
		if n := sqlValue(t, st, "SELECT COUNT(*) FROM "+table+" WHERE org_id = ? AND user_id = ?", acme, ann); n != "1" {
			t.Errorf("%s: %s rows of acme's ann; want 1", table, n)
		}
	}
	for _, table := range []string{"secrets", "job_runs"} {
		if n := sqlValue(t, st, "SELECT COUNT(*) FROM "+table+" WHERE org_id = ?", acme); n != "1" {
			t.Errorf("%s: %s rows of acme; want 1", table, n)
		}
	}
	// Read back as the names the gateway uses.
	if got, ok := st.OwnerOf("sbx_1"); !ok || got != o {
		t.Errorf("owner %+v %v; want %+v", got, ok, o)
	}

	// The database refuses a record of an organisation or a user that is
	// not there, whatever a query asks.
	q := st.sql()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO sandboxes (id, org_id, user_id, node, recorded) VALUES (?, ?, ?, ?, ?)", []any{"sbx_x", "org_nope", ann, "n1", q.t(time.Now())}},
		{"INSERT INTO sandboxes (id, org_id, user_id, node, recorded) VALUES (?, ?, ?, ?, ?)", []any{"sbx_y", acme, "usr_nope", "n1", q.t(time.Now())}},
		{"INSERT INTO secrets (org_id, name, sealed, updated) VALUES (?, ?, ?, ?)", []any{"org_nope", "X", []byte("x"), q.t(time.Now())}},
		{"INSERT INTO api_keys (id, hash, user_id, scopes, created) VALUES (?, ?, ?, ?, ?)", []any{"key_x", "h", "usr_nope", "[]", q.t(time.Now())}},
	} {
		if _, err := q.db.Exec(q.rebind(stmt.query), stmt.args...); err == nil {
			t.Errorf("%s %v: stored with nothing to belong to", stmt.query, stmt.args)
		}
	}
}

func TestRemovingAUserRemovesTheirKeysAndMemberships(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := st.CreateKey("ann", "", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateKey("bob", "", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrg("acme", "ann", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetMember("acme", "bob", "", api.RoleMember); err != nil {
		t.Fatal(err)
	}
	st.must(st.AssignPolicy("bob", "", ""))
	bob := sqlValue(t, st, "SELECT id FROM users WHERE name = 'bob'")
	st.exec("DELETE FROM users WHERE id = ?", bob)
	if n := sqlValue(t, st, "SELECT COUNT(*) FROM api_keys WHERE user_id = ?", bob); n != "0" {
		t.Errorf("%s keys of a removed user", n)
	}
	if _, ok := st.MemberRole("acme", "bob", ""); ok {
		t.Error("a removed user is still a member")
	}
	if _, ok := st.MemberRole("acme", "ann", ""); !ok {
		t.Error("ann's membership went with bob")
	}
}

// An account is its user's: the address and password are on the user, and
// an address a revoked account had may be another user's.
func TestAnAccountsAddressIsItsUsers(t *testing.T) {
	st := newTestStore(t)
	redeem := func(user string) Key {
		t.Helper()
		token, _, err := st.CreateInvite(api.CreateInviteRequest{Email: "Pat@Example.com", User: user, Scopes: []string{ScopeRead}})
		if err != nil {
			t.Fatal(err)
		}
		k, err := st.RedeemInvite(token, "hash-"+user)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	first := redeem("pat")
	if got := sqlValue(t, st, "SELECT email FROM users WHERE name = 'pat'"); got != "pat@example.com" {
		t.Errorf("pat's address on their user: %q", got)
	}
	if a, ok := st.Account("pat@example.com"); !ok || a.ID != first.ID || a.Password != "hash-pat" {
		t.Errorf("account %+v %v", a, ok)
	}
	if _, _, err := st.CreateInvite(api.CreateInviteRequest{Email: "pat@example.com", User: "pam", Scopes: []string{ScopeRead}}); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("a second account for a live address: %v", err)
	}
	st.must(st.RevokeKey(first.ID))
	second := redeem("pam")
	if a, ok := st.Account("pat@example.com"); !ok || a.ID != second.ID || a.User != "pam" {
		t.Errorf("the address's account is %+v %v; want pam's", a, ok)
	}
	if got := sqlValue(t, st, "SELECT email FROM users WHERE name = 'pat'"); got != "" {
		t.Errorf("pat's user still holds %q", got)
	}
}
