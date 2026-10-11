package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The gateway's tests run against a real store: SQLite in the test's
// directory, or PostgreSQL when SANDBOX_TEST_POSTGRES_DSN names a server
// (make test-postgres), each test in a database of its own.

// openTestStore opens a fresh store for t.
func openTestStore(t testing.TB) Store {
	t.Helper()
	st, err := openTestStoreAt(t, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// openTestStoreAt opens the store at path (SQLite), or the test's own
// PostgreSQL database, for a test that reopens what it wrote.
func openTestStoreAt(t testing.TB, path string) (Store, error) {
	t.Helper()
	if dsn := os.Getenv("SANDBOX_TEST_POSTGRES_DSN"); dsn != "" {
		return OpenPostgres(context.Background(), testDatabase(t, dsn, path))
	}
	return OpenSQLite(context.Background(), path)
}

// testDatabase is a database of the test's own on the server dsn names,
// one per path a test opens, dropped when the test ends.
func testDatabase(t testing.TB, dsn, path string) string {
	t.Helper()
	key := "t_" + strings.ToLower(hashSecret(t.Name() + "\x00" + path)[:24])
	if db, ok := testDatabases[key]; ok {
		return db
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec("CREATE DATABASE " + key); err != nil {
		t.Fatalf("creating the test's database: %v", err)
	}
	t.Cleanup(func() {
		a, err := sql.Open("pgx", dsn)
		if err != nil {
			return
		}
		defer a.Close()
		_, _ = a.Exec("DROP DATABASE IF EXISTS " + key + " WITH (FORCE)")
	})
	db := withDatabase(t, dsn, key)
	testDatabases[key] = db
	return db
}

var testDatabases = map[string]string{}

// withDatabase is dsn with its database replaced.
func withDatabase(t testing.TB, dsn, db string) string {
	t.Helper()
	if i := strings.Index(dsn, "://"); i >= 0 {
		rest := dsn[i+3:]
		slash := strings.Index(rest, "/")
		q := ""
		if j := strings.Index(rest, "?"); j >= 0 {
			q = rest[j:]
			if slash > j {
				slash = -1
			}
			rest = rest[:j]
		}
		if slash >= 0 {
			rest = rest[:slash]
		}
		return dsn[:i+3] + rest + "/" + db + q
	}
	return dsn + " dbname=" + db
}

// testStore is a store with the shape the tests were written to: no
// context, and a failure of the store failing the test rather than being
// handled. Store is the real one, which the gateway is given.
type testStore struct {
	Store
	t testing.TB
}

func (s *testStore) ctx() context.Context { return context.Background() }

func (s *testStore) must(err error) {
	s.t.Helper()
	if err != nil {
		s.t.Fatalf("store: %v", err)
	}
}

// sql is the store's own database, for a test that changes a row directly.
func (s *testStore) sql() *sqlStore { return s.Store.(*sqlStore) }

// exec runs a statement on the database, ? placeholders as the store's.
func (s *testStore) exec(query string, args ...any) {
	s.t.Helper()
	q := s.sql()
	if _, err := q.db.Exec(q.rebind(query), args...); err != nil {
		s.t.Fatalf("exec %q: %v", query, err)
	}
}

// rawBytes is everything the store holds on disk, for a test that checks a
// value never reaches it: the SQLite file with its write-ahead log, or, on
// PostgreSQL, every row of the tables that could hold one, as text.
func (s *testStore) rawBytes() []byte {
	s.t.Helper()
	q := s.sql()
	if q.path != "" {
		var out []byte
		for _, sfx := range []string{"", "-wal"} {
			b, _ := os.ReadFile(q.path + sfx)
			out = append(out, b...)
		}
		return out
	}
	var out []byte
	for _, table := range []string{"secrets", "jobs", "job_runs", "services", "api_keys", "users", "invites"} {
		rows, err := q.db.Query("SELECT t::text FROM " + table + " t")
		if err != nil {
			s.t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				s.t.Fatal(err)
			}
			out = append(out, line...)
		}
		rows.Close()
	}
	return out
}

// snapshotsOn is every snapshot record on node.
func (s *testStore) snapshotsOn(node string) []string {
	s.t.Helper()
	q := s.sql()
	rows, err := q.db.Query(q.rebind("SELECT id FROM snapshots WHERE node = ?"), node)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			s.t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// setNow sets the store's clock.
func (s *testStore) setNow(now func() time.Time) { s.sql().now = now }

func (s *testStore) CreateKey(user, tenant string, scopes []string) (string, Key, error) {
	return s.Store.CreateKey(s.ctx(), user, tenant, scopes)
}

func (s *testStore) CreateKeyExpiring(user, tenant string, scopes []string, expires *time.Time) (string, Key, error) {
	return s.Store.CreateKeyExpiring(s.ctx(), user, tenant, scopes, expires)
}

func (s *testStore) KeyBySecret(secret string) (Key, bool) {
	k, ok, err := s.Store.KeyBySecret(s.ctx(), secret)
	s.must(err)
	return k, ok
}

func (s *testStore) RevokeKey(id string) error { return s.Store.RevokeKey(s.ctx(), id) }

func (s *testStore) Keys() []Key {
	ks, err := s.Store.Keys(s.ctx())
	s.must(err)
	return ks
}

func (s *testStore) Account(email string) (Key, bool) {
	k, ok, err := s.Store.Account(s.ctx(), email)
	s.must(err)
	return k, ok
}

func (s *testStore) CreateInvite(req api.CreateInviteRequest) (string, inviteRecord, error) {
	return s.Store.CreateInvite(s.ctx(), req)
}

func (s *testStore) Invites() []inviteRecord {
	ivs, err := s.Store.Invites(s.ctx())
	s.must(err)
	return ivs
}

func (s *testStore) InviteByToken(token string) (inviteRecord, bool) {
	iv, ok, err := s.Store.InviteByToken(s.ctx(), token)
	s.must(err)
	return iv, ok
}

func (s *testStore) RedeemInvite(token, hash string) (Key, error) {
	return s.Store.RedeemInvite(s.ctx(), token, hash)
}

func (s *testStore) AddSSHKey(user, tenant, sandbox, authorizedKey string) (SSHKey, error) {
	return s.Store.AddSSHKey(s.ctx(), user, tenant, sandbox, authorizedKey)
}

func (s *testStore) SSHKeysFor(user string) []SSHKey {
	ks, err := s.Store.SSHKeysFor(s.ctx(), user)
	s.must(err)
	return ks
}

func (s *testStore) RedeemSSHToken(token string) (SSHToken, bool) {
	t, ok, err := s.Store.RedeemSSHToken(s.ctx(), token)
	s.must(err)
	return t, ok
}

// SetOwner records a sandbox with no size and no details.
func (s *testStore) SetOwner(id string, o Owner) error {
	return s.Store.RecordSandbox(s.ctx(), id, o, api.NodeResources{}, nil)
}

func (s *testStore) RecordSandbox(id string, o Owner, cpus float64, memoryMB int) error {
	return s.Store.RecordSandbox(s.ctx(), id, o, api.NodeResources{CPUs: cpus, MemoryMB: memoryMB}, nil)
}

func (s *testStore) OwnerOf(id string) (Owner, bool) {
	o, ok, err := s.Store.OwnerOf(s.ctx(), id)
	s.must(err)
	return o, ok
}

func (s *testStore) ForgetSandbox(id string) error { return s.Store.ForgetSandbox(s.ctx(), id) }

func (s *testStore) UsageOf(tenant string) Usage {
	u, err := s.Store.UsageOf(s.ctx(), tenant)
	s.must(err)
	return u
}

func (s *testStore) VolumeOwner(name string) (Owner, bool) {
	o, ok, err := s.Store.VolumeOwner(s.ctx(), name)
	s.must(err)
	return o, ok
}

func (s *testStore) SetVolume(name string, o Owner) error { return s.Store.SetVolume(s.ctx(), name, o) }

func (s *testStore) SnapshotOwner(id string) (Owner, bool) {
	o, ok, err := s.Store.SnapshotOwner(s.ctx(), id)
	s.must(err)
	return o, ok
}

func (s *testStore) SetSnapshot(id string, o Owner) error { return s.Store.SetSnapshot(s.ctx(), id, o) }

func (s *testStore) Nodes() []NodeConfig {
	ns, err := s.Store.Nodes(s.ctx())
	s.must(err)
	return ns
}

func (s *testStore) PutNode(n NodeConfig) error { return s.Store.PutNode(s.ctx(), n) }

func (s *testStore) CreateOrg(name, user, userTenant string, maxOwned int) (orgRecord, error) {
	return s.Store.CreateOrg(s.ctx(), name, user, userTenant, maxOwned)
}

func (s *testStore) Org(name string) (orgRecord, bool) {
	o, ok, err := s.Store.Org(s.ctx(), name)
	s.must(err)
	return o, ok
}

func (s *testStore) Orgs() []orgRecord {
	os, err := s.Store.Orgs(s.ctx())
	s.must(err)
	return os
}

func (s *testStore) MemberRole(org, user, userTenant string) (string, bool) {
	r, ok, err := s.Store.MemberRole(s.ctx(), org, user, userTenant)
	s.must(err)
	return r, ok
}

func (s *testStore) MembershipsOf(user, userTenant string) []orgMembership {
	ms, err := s.Store.MembershipsOf(s.ctx(), user, userTenant)
	s.must(err)
	return ms
}

func (s *testStore) SetMember(org, user, userTenant, role string) (string, error) {
	return s.Store.SetMember(s.ctx(), org, user, userTenant, role)
}

func (s *testStore) PutPolicy(p api.UserPolicy) error { return s.Store.PutPolicy(s.ctx(), p) }

func (s *testStore) AssignPolicy(user, tenant, name string) error {
	return s.Store.AssignPolicy(s.ctx(), user, tenant, name)
}

func (s *testStore) PolicyOf(user, keyTenant string) (api.UserPolicy, bool) {
	p, ok, err := s.Store.PolicyOf(s.ctx(), user, keyTenant)
	s.must(err)
	return p, ok
}

func (s *testStore) PolicyActingIn(user, tenant string) (api.UserPolicy, bool) {
	p, ok, err := s.Store.PolicyActingIn(s.ctx(), user, tenant)
	s.must(err)
	return p, ok
}

func (s *testStore) PutJob(r *jobRecord) error { return s.Store.PutJob(s.ctx(), r) }

func (s *testStore) Job(id string) (*jobRecord, bool) {
	r, ok, err := s.Store.Job(s.ctx(), id)
	s.must(err)
	return r, ok
}

func (s *testStore) AllJobs() []*jobRecord {
	rs, err := s.Store.AllJobs(s.ctx())
	s.must(err)
	return rs
}

func (s *testStore) UpdateJob(id string, fn func(*jobRecord)) error {
	return s.Store.UpdateJob(s.ctx(), id, fn)
}

func (s *testStore) PutSecret(tenant, name string, sealed []byte) error {
	return s.Store.PutSecret(s.ctx(), tenant, name, sealed)
}

func (s *testStore) SealedSecret(tenant, name string) ([]byte, bool) {
	b, ok, err := s.Store.SealedSecret(s.ctx(), tenant, name)
	s.must(err)
	return b, ok
}

func (s *testStore) Services() []serviceRecord {
	rs, err := s.Store.Services(s.ctx())
	s.must(err)
	return rs
}

func (s *testStore) PutService(r serviceRecord) error { return s.Store.PutService(s.ctx(), r) }

func (s *testStore) Retiring() []string {
	ids, err := s.Store.Retiring(s.ctx())
	s.must(err)
	return ids
}

func (s *testStore) Retire(ids ...string) error { return s.Store.Retire(s.ctx(), ids...) }

// newTestStore is openTestStore in the tests' shape.
func newTestStore(t testing.TB) *testStore { return &testStore{Store: openTestStore(t), t: t} }

// reopenTestStore opens the store at path again, as a restarted gateway
// would.
func reopenTestStore(t testing.TB, path string) *testStore {
	t.Helper()
	st, err := openTestStoreAt(t, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &testStore{Store: st, t: t}
}

var _ = fmt.Sprintf

// lost is the gateway's lost sandboxes, failing the test on a store error.
func (tg *testGateway) lost() []api.LostSandbox {
	tg.t.Helper()
	l, err := tg.g.lostSandboxes(context.Background())
	if err != nil {
		tg.t.Fatal(err)
	}
	return l
}

// usage is a tenant's usage as its quota is checked.
func (tg *testGateway) usage(tenant string) Usage {
	tg.t.Helper()
	u, err := tg.g.usageOf(context.Background(), tenant)
	if err != nil {
		tg.t.Fatal(err)
	}
	return u
}

// owner is the gateway's owner of a sandbox: stored, or a tombstone's.
func (tg *testGateway) owner(id string) (Owner, bool) {
	tg.t.Helper()
	o, ok, err := tg.g.ownerOf(context.Background(), Principal{Scopes: []string{ScopeAdmin}}, id)
	if err != nil {
		tg.t.Fatal(err)
	}
	return o, ok
}

// active is userActive, failing the test on a store error.
func active(t testing.TB, st Store, user, tenant string) bool {
	t.Helper()
	ok, err := userActive(context.Background(), st, user, tenant)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// maySSH is userMaySSH, failing the test on a store error.
func maySSH(t testing.TB, st Store, user, tenant string) bool {
	t.Helper()
	ok, err := userMaySSH(context.Background(), st, user, tenant)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}
