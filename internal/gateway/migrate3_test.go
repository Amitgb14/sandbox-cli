package gateway

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Migration 3 on a database a gateway already used: every row carried into
// organisations and users, every secret handed out still working, and a
// database it cannot carry whole left as it was.

// v2Database is a database as a gateway before 0003 left it — migrations 1
// and 2 run (SQLite had no 2) — and a store-shaped handle on it for
// writing rows in that shape.
type v2Database struct {
	s    *sqlStore
	path string // SQLite's file; the test's database name on PostgreSQL
}

func newV2Database(t *testing.T) *v2Database {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.db")
	s := &sqlStore{now: time.Now, path: path}
	var err error
	if dsn := os.Getenv("SANDBOX_TEST_POSTGRES_DSN"); dsn != "" {
		s.d, s.path = postgresDialect, ""
		s.db, err = sql.Open("pgx", testDatabase(t, dsn, path))
	} else {
		s.d = sqliteDialect
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		// Without foreign keys, as SQLite opens by default, so a test can
		// write a row a version-2 gateway never would.
		s.db, err = sql.Open("sqlite", "file:"+path)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	dir := "migrations/sqlite"
	if s.d == postgresDialect {
		dir = "migrations/postgres"
	}
	entries, err := fs.ReadDir(migrationFiles, dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") && (strings.HasPrefix(e.Name(), "0001_") || strings.HasPrefix(e.Name(), "0002_")) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	db := &v2Database{s: s, path: path}
	db.exec(t, "CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied "+s.timeType()+" NOT NULL)")
	for i, n := range names {
		body, err := fs.ReadFile(migrationFiles, dir+"/"+n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		db.exec(t, "INSERT INTO schema_migrations (version, applied) VALUES (?, ?)", i+1, s.t(time.Now()))
	}
	return db
}

func (db *v2Database) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := db.s.db.Exec(db.s.rebind(query), args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// open opens the database as the gateway does, migrating it.
func (db *v2Database) open(t *testing.T, migrateData bool) (*testStore, error) {
	t.Helper()
	ctx := context.Background()
	var st Store
	var err error
	if db.s.d == postgresDialect {
		st, err = OpenPostgres(ctx, testDatabase(t, os.Getenv("SANDBOX_TEST_POSTGRES_DSN"), db.path), OpenOptions{MigrateData: migrateData})
	} else {
		st, err = OpenSQLite(ctx, db.path)
	}
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { st.Close() })
	return &testStore{Store: st, t: t}, nil
}

// fill writes a version-2 gateway's worth of state: keys of every kind, an
// account with a sign-in, an organisation with a member from another
// tenant, and records of every kind in it. It returns secrets by name.
func (db *v2Database) fill(t *testing.T) map[string]string {
	t.Helper()
	s := db.s
	now := time.Now()
	later := now.Add(time.Hour)
	secrets := map[string]string{}
	key := func(id, user, tenant string, scopes string, extra ...any) {
		secret := newSecret(keySecretPrefix)
		secrets[id] = secret
		email, password, parent, revoked := any(nil), any(nil), any(nil), false
		if len(extra) > 0 {
			email, password, parent, revoked = extra[0], extra[1], extra[2], extra[3].(bool)
		}
		db.exec(t, `INSERT INTO api_keys (id, hash, user_name, tenant, scopes, created, revoked, expires, email, password, last_login, parent)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, hashSecret(secret), user, tenant, scopes, s.t(now), revoked, nil,
			email, password, nil, parent)
	}
	key("key_ann", "ann", "", `["read","create"]`)
	key("key_bob", "bob", "Globex", `["read"]`)
	key("key_cat", "cat", "acme", `["read"]`)
	key("key_pat", "pat", "", `["read"]`, "pat@example.com", "pwhash", nil, false)
	key("key_patin", "pat", "", `["read"]`, nil, nil, "key_pat", false)
	key("key_olly", "olly", "", `["read"]`, "olly@example.com", "oldhash", nil, true)

	db.exec(t, "INSERT INTO policies (name, max_lifetime_secs) VALUES (?, ?)", "trial", 1800)
	db.exec(t, "INSERT INTO user_policies (user_name, tenant, policy) VALUES (?, ?, ?)", "pat", "", "trial")
	db.exec(t, "INSERT INTO orgs (name_key, display_name, created, created_by, created_by_tenant) VALUES (?, ?, ?, ?, ?)",
		"acme", "Acme", s.t(now), "ann", "")
	db.exec(t, "INSERT INTO org_members (org, user_name, user_tenant, role, added) VALUES (?, ?, ?, ?, ?)", "acme", "ann", "", api.RoleOwner, s.t(now))
	db.exec(t, "INSERT INTO org_members (org, user_name, user_tenant, role, added) VALUES (?, ?, ?, ?, ?)", "acme", "bob", "Globex", api.RoleMember, s.t(now))
	db.exec(t, "INSERT INTO nodes (name, endpoint) VALUES (?, ?)", "n1", "https://n1:7443")
	db.exec(t, `INSERT INTO sandboxes (id, tenant, user_name, node, cpus, memory_mb, recorded, name, state, labels, network, details_updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "sbx_1", "acme", "bob", "n1", 1.0, 512, s.t(now), "web", "running", `{"a":"b"}`, "{}", s.t(now))
	db.exec(t, `INSERT INTO sandboxes (id, tenant, user_name, node, recorded, terminated_at, terminated_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, "sbx_2", "", "ann", "n1", s.t(now), s.t(now), "terminated")
	db.exec(t, "INSERT INTO volumes (name, tenant, user_name, node) VALUES (?, ?, ?, ?)", "vol1", "acme", "cat", "n1")
	db.exec(t, "INSERT INTO snapshots (id, tenant, user_name, node) VALUES (?, ?, ?, ?)", "snap_1", "", "ann", "n1")
	db.exec(t, "INSERT INTO secrets (tenant, name, sealed, updated) VALUES (?, ?, ?, ?)", "acme", "TOKEN", []byte("sealed"), s.t(now))
	db.exec(t, `INSERT INTO jobs (id, tenant, user_name, spec, state, created) VALUES (?, ?, ?, ?, ?, ?)`,
		"job_1", "acme", "ann", `{"command":["true"],"completions":2}`, api.JobRunning, s.t(now))
	for n := 0; n < 2; n++ {
		db.exec(t, "INSERT INTO job_runs (tenant, job, n, run) VALUES (?, ?, ?, ?)", "acme", "job_1", n, `{}`)
	}
	db.exec(t, "INSERT INTO services (tenant, name, user_name, record) VALUES (?, ?, ?, ?)",
		"acme", "web", "cat", `{"name":"web","user":"cat","tenant":"acme","spec":{},"revision":1,"serving":{}}`)
	db.exec(t, "INSERT INTO ssh_keys (id, user_name, tenant, fingerprint, authorized_key, created) VALUES (?, ?, ?, ?, ?, ?)",
		"ssh_1", "ann", "", "SHA256:abc", "ssh-ed25519 AAAA", s.t(now))
	token := newSecret(tokenSecretPrefix)
	secrets["token"] = token
	db.exec(t, "INSERT INTO ssh_tokens (hash, user_name, tenant, sandbox, expires) VALUES (?, ?, ?, ?, ?)",
		hashSecret(token), "bob", "acme", "sbx_1", s.t(later))
	db.exec(t, "INSERT INTO ssh_tokens (hash, user_name, tenant, sandbox, expires) VALUES (?, ?, ?, ?, ?)",
		"expired", "bob", "acme", "sbx_1", s.t(now.Add(-time.Hour)))
	db.exec(t, `INSERT INTO invites (id, hash, email, user_name, tenant, scopes, created, expires) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"inv_1", "h1", "ivy@example.com", "ivy", "initech", `["read"]`, s.t(now), s.t(later))
	db.exec(t, `INSERT INTO invites (id, hash, email, user_name, tenant, scopes, account_key, created, expires) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"inv_2", "h2", "pat@example.com", "pat", "", `["read"]`, "key_pat", s.t(now), s.t(later))
	return secrets
}

func TestMigration3CarriesAVersion2Database(t *testing.T) {
	db := newV2Database(t)
	secrets := db.fill(t)
	if db.s.d == postgresDialect {
		// Rows to move and no word that they were backed up: refused.
		if _, err := db.open(t, false); err == nil || !strings.Contains(err.Error(), "pg_dump") {
			t.Fatalf("migrating PostgreSQL rows without --store-migrate: %v", err)
		}
	}
	st, err := db.open(t, true)
	if err != nil {
		t.Fatal(err)
	}
	// Migrated through 0003, and on to every migration after it.
	if v, err := SchemaVersion(context.Background(), st.Store); err != nil || v < 4 {
		t.Fatalf("schema version %d, %v; want every migration applied", v, err)
	}
	if db.s.d == sqliteDialect {
		if fi, err := os.Stat(db.path + ".before-0003"); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("the copy made first: %v %v", fi, err)
		}
	}

	// Every key still works by the secret it was handed out with.
	for id, want := range map[string]Key{
		"key_ann":   {User: "ann", Tenant: ""},
		"key_bob":   {User: "bob", Tenant: "Globex"},
		"key_cat":   {User: "cat", Tenant: "acme"},
		"key_patin": {User: "pat", Tenant: "", Parent: "key_pat"},
	} {
		k, ok := st.KeyBySecret(secrets[id])
		if !ok || k.ID != id || k.User != want.User || k.Tenant != want.Tenant || k.Parent != want.Parent {
			t.Errorf("%s: %+v %v", id, k, ok)
		}
	}
	if _, ok := st.KeyBySecret(secrets["key_olly"]); ok {
		t.Error("a revoked key works after the migration")
	}
	if a, ok := st.Account("pat@example.com"); !ok || a.ID != "key_pat" || a.Password != "pwhash" {
		t.Errorf("pat's account: %+v %v", a, ok)
	}
	if k, ok := st.KeyBySecret(secrets["key_ann"]); !ok || len(k.Scopes) != 2 {
		t.Errorf("ann's scopes: %+v", k.Scopes)
	}

	o, ok := st.Org("acme")
	if !ok || o.Name != "Acme" || o.CreatedBy != "ann" || len(o.Members) != 2 {
		t.Fatalf("acme: %+v %v", o, ok)
	}
	if role, ok := st.MemberRole("acme", "bob", "Globex"); !ok || role != api.RoleMember {
		t.Errorf("bob of Globex in acme: %q %v", role, ok)
	}
	if got, ok := st.OwnerOf("sbx_1"); !ok || got != (Owner{User: "bob", Tenant: "acme", Node: "n1"}) {
		t.Errorf("sbx_1: %+v %v", got, ok)
	}
	if rows, err := st.Store.SandboxHistory(context.Background(), SandboxFilter{AllTenant: true}); err != nil || len(rows) != 2 {
		t.Errorf("history: %d rows, %v", len(rows), err)
	} else {
		for _, r := range rows {
			if r.ID == "sbx_1" && (r.Details == nil || r.Details.Labels["a"] != "b") {
				t.Errorf("sbx_1's details: %+v", r.Details)
			}
		}
	}
	if got, ok := st.VolumeOwner("vol1"); !ok || got != (Owner{User: "cat", Tenant: "acme", Node: "n1"}) {
		t.Errorf("vol1: %+v %v", got, ok)
	}
	if got, ok := st.SnapshotOwner("snap_1"); !ok || got != (Owner{User: "ann", Node: "n1"}) {
		t.Errorf("snap_1: %+v %v", got, ok)
	}
	if b, ok := st.SealedSecret("acme", "TOKEN"); !ok || string(b) != "sealed" {
		t.Errorf("acme's secret: %q %v", b, ok)
	}
	if j, ok := st.Job("job_1"); !ok || j.Tenant != "acme" || j.User != "ann" || len(j.Runs) != 2 {
		t.Errorf("job_1: %+v %v", j, ok)
	}
	if svcs := st.Services(); len(svcs) != 1 || svcs[0].Name != "web" || svcs[0].User != "cat" {
		t.Errorf("services: %+v", svcs)
	}
	if ks := st.SSHKeysFor("ann"); len(ks) != 1 || ks[0].ID != "ssh_1" {
		t.Errorf("ann's SSH keys: %+v", ks)
	}
	if tok, ok := st.RedeemSSHToken(secrets["token"]); !ok || tok.User != "bob" || tok.Tenant != "acme" || tok.Sandbox != "sbx_1" {
		t.Errorf("the SSH token: %+v %v", tok, ok)
	}
	if p, ok := st.PolicyOf("pat", ""); !ok || p.Name != "trial" {
		t.Errorf("pat's policy: %+v %v", p, ok)
	}
	invites := map[string]inviteRecord{}
	for _, iv := range st.Invites() {
		invites[iv.ID] = iv
	}
	if iv := invites["inv_1"]; iv.Tenant != "initech" || iv.User != "ivy" {
		t.Errorf("ivy's invite: %+v", iv)
	}
	if iv := invites["inv_2"]; iv.Key != "key_pat" {
		t.Errorf("pat's reset: %+v", iv)
	}

	for key, kind := range map[string]string{"": orgDefault, "Globex": orgPersonal, "acme": orgTeam, "initech": orgPersonal} {
		if got := sqlValue(t, st, "SELECT kind FROM orgs WHERE key = ?", key); got != kind {
			t.Errorf("organisation %q is %q; want %s", key, got, kind)
		}
	}
	if n := sqlValue(t, st, "SELECT COUNT(*) FROM users"); n != "5" {
		t.Errorf("%s users; want ann, bob, cat, pat and olly", n)
	}
	// And it goes on working: a new key, sandbox and member.
	if _, _, err := st.CreateKey("dee", "acme", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	st.must(st.RecordSandbox("sbx_3", Owner{User: "dee", Tenant: "acme", Node: "n1"}, 1, 256))
	if u := st.UsageOf("acme"); u.Sandboxes != 2 {
		t.Errorf("acme's usage: %+v", u)
	}
}

// A row the migration cannot carry rolls it all back: the database stays at
// the version it was (2 on PostgreSQL; SQLite had no migration 2).
func TestMigration3LeavesADatabaseItCannotCarry(t *testing.T) {
	db := newV2Database(t)
	db.fill(t)
	// A run of a job that is not there: a version-2 gateway never wrote one.
	if db.s.d == postgresDialect {
		db.exec(t, "ALTER TABLE job_runs DROP CONSTRAINT job_runs_job_fkey")
	}
	db.exec(t, "INSERT INTO job_runs (tenant, job, n, run) VALUES (?, ?, ?, ?)", "acme", "job_gone", 0, `{}`)

	_, err := db.open(t, true)
	if err == nil || !strings.Contains(err.Error(), "job_runs") {
		t.Fatalf("migrating with an orphaned run: %v", err)
	}
	var v int
	if err := db.s.db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&v); err != nil || v >= 3 {
		t.Errorf("schema version %d, %v; want it as it was", v, err)
	}
	var n int
	if err := db.s.db.QueryRow("SELECT COUNT(*) FROM api_keys WHERE user_name = 'ann'").Scan(&n); err != nil || n != 1 {
		t.Errorf("the version-2 keys: %d, %v", n, err)
	}
	for _, table := range []string{"users", "api_keys_v2", "orgs_v3"} {
		var x int
		if err := db.s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&x); err == nil {
			t.Errorf("%s was left behind", table)
		}
	}
}
