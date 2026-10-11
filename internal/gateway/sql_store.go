package gateway

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" database/sql driver
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// The gateway's state in a database: SQLite for one gateway on one machine,
// PostgreSQL for one or more gateways in front of a fleet
// (docs/fleet.md#the-gateways-database).
//
// One implementation serves both, over database/sql. Queries are written
// with ? placeholders and rewritten to $n for PostgreSQL; the schema of each
// is in migrations/, applied in order at open. What differs is kept to the
// few helpers here: how a time is stored (unix nanoseconds on SQLite, whose
// text times do not sort; timestamptz on PostgreSQL), and isolation, which
// PostgreSQL also enforces itself (ForTenant, migrations/postgres/0002).
//
// Each change is one transaction. SQLite takes its write lock as a
// transaction begins (_txlock=immediate), so two writers wait for each other
// rather than fail; PostgreSQL locks the rows a change reads with FOR UPDATE.

//go:embed migrations
var migrationFiles embed.FS

type dialect int

const (
	sqliteDialect dialect = iota
	postgresDialect
)

type sqlStore struct {
	db *sql.DB
	d  dialect
	// tenant, when set, is the tenant every statement runs as (ForTenant):
	// on PostgreSQL under row-level security.
	tenant *string
	now    func() time.Time
	closer func() error
	path   string // the SQLite file; "" on PostgreSQL
	// migrateData lets a migration that moves existing rows run on
	// PostgreSQL (OpenOptions.MigrateData); SQLite copies its file aside
	// instead.
	migrateData bool
}

// OpenOptions are how a store is opened.
type OpenOptions struct {
	// MigrateData lets PostgreSQL run a migration that moves the rows a
	// database already holds into a new shape. Off, such a migration is
	// refused, with the pg_dump to take first: SQLite copies its own file
	// aside, a PostgreSQL database is the operator's to back up.
	MigrateData bool
}

// OpenSQLite opens (creating when missing) the SQLite database at path, mode
// 0600 in a directory of 0700, and brings its schema up to date. A file
// others can read is refused, as the state file was: it holds every key's
// hash and who owns what.
func OpenSQLite(ctx context.Context, path string) (Store, error) {
	if path == "" {
		return nil, errors.New("no database file named")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil {
		if fi.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%s is readable by others (mode %v); chmod 600 it", path, fi.Mode().Perm())
		}
	} else if errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		f.Close()
	} else {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// One writer at a time is SQLite's; a few connections let reads run
	// beside it (WAL).
	db.SetMaxOpenConns(8)
	s := &sqlStore{db: db, d: sqliteDialect, now: time.Now, closer: db.Close, path: path}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	// The WAL and shared-memory files hold the same rows.
	for _, sfx := range []string{"-wal", "-shm"} {
		_ = os.Chmod(path+sfx, 0o600)
	}
	return s, nil
}

// OpenPostgres opens the PostgreSQL database dsn names and brings its schema
// up to date. The DSN comes from a file the caller has checked is private
// (never an argument: a password in an argv is every local user's); a
// connection that is not encrypted and verified is refused unless it stays
// on this machine.
func OpenPostgres(ctx context.Context, dsn string, opts ...OpenOptions) (Store, error) {
	if err := checkPostgresDSN(dsn); err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(32)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(30 * time.Minute)
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("reaching the database: %w", err)
	}
	s := &sqlStore{db: db, d: postgresDialect, now: time.Now, closer: db.Close}
	for _, o := range opts {
		s.migrateData = s.migrateData || o.MigrateData
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// checkPostgresDSN refuses a connection that could be read or redirected on
// the way: sslmode must be verify-full, unless the host is loopback or a
// unix socket.
func checkPostgresDSN(dsn string) error {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("the database address: %w", err)
	}
	local := strings.HasPrefix(cfg.Host, "/")
	if ip := net.ParseIP(cfg.Host); ip != nil && ip.IsLoopback() || cfg.Host == "localhost" {
		local = true
	}
	if local {
		return nil
	}
	mode := ""
	if strings.Contains(dsn, "://") {
		if u, err := url.Parse(dsn); err == nil {
			mode = u.Query().Get("sslmode")
		}
	} else {
		for _, f := range strings.Fields(dsn) {
			if v, ok := strings.CutPrefix(f, "sslmode="); ok {
				mode = v
			}
		}
	}
	if mode != "verify-full" {
		return fmt.Errorf("the database at %s is not on this machine: its address needs sslmode=verify-full (and sslrootcert), not %q", cfg.Host, mode)
	}
	return nil
}

// stamp is now, as the store keeps times: UTC, to the microsecond, so a
// record handed back is the record stored.
func (s *sqlStore) stamp() time.Time { return s.now().UTC().Truncate(time.Microsecond) }

func (s *sqlStore) Close() error   { return s.closer() }
func (s *sqlStore) Now() time.Time { return s.now() }

// ForTenant is the store as tenant: on PostgreSQL each statement runs as
// sandbox_gateway_tenant with app.org set, so row-level security holds
// every tenant-scoped table to tenant. SQLite has none; its queries filter.
func (s *sqlStore) ForTenant(tenant string) Store {
	cp := *s
	cp.tenant = &tenant
	cp.closer = func() error { return nil }
	return &cp
}

// --- transactions ------------------------------------------------------------------

// querier is what a statement runs on: the database, or a transaction.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// tx runs fn in one transaction, committed when fn returns nil. On
// PostgreSQL as a tenant it first takes the tenant's role and setting, which
// last until the transaction ends.
func (s *sqlStore) tx(ctx context.Context, fn func(q *sqlTx) error) error {
	t, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storeErr(err)
	}
	q := &sqlTx{s: s, tx: t}
	if err := s.asTenant(ctx, t); err != nil {
		t.Rollback()
		return storeErr(err)
	}
	if err := fn(q); err != nil {
		t.Rollback()
		return err
	}
	if err := t.Commit(); err != nil {
		return storeErr(err)
	}
	return nil
}

// read runs fn for a read. As a tenant on PostgreSQL that needs a
// transaction, for the role; otherwise the database itself, so reads on
// SQLite do not take its write lock.
func (s *sqlStore) read(ctx context.Context, fn func(q *sqlTx) error) error {
	if s.d == postgresDialect && s.tenant != nil {
		return s.tx(ctx, fn)
	}
	return fn(&sqlTx{s: s, db: s.db})
}

func (s *sqlStore) asTenant(ctx context.Context, t *sql.Tx) error {
	if s.d != postgresDialect || s.tenant == nil {
		return nil
	}
	if _, err := t.ExecContext(ctx, "SET LOCAL ROLE sandbox_gateway_tenant"); err != nil {
		return err
	}
	// Row-level security compares org_id with app.org: the id of the
	// tenant's organisation, or none ("") for a tenant not yet one, which
	// matches no row. orgID sets it once it makes the organisation.
	_, err := t.ExecContext(ctx, "SELECT set_config('app.org', COALESCE((SELECT id FROM orgs WHERE key = $1), ''), true)",
		orgKeyOf(*s.tenant))
	return err
}

// sqlTx is a statement runner with the store's dialect: ? placeholders
// rewritten for PostgreSQL, and its errors marked as the store's.
type sqlTx struct {
	s  *sqlStore
	tx *sql.Tx
	db *sql.DB
}

func (q *sqlTx) on() querier {
	if q.tx != nil {
		return q.tx
	}
	return q.db
}

func (q *sqlTx) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	r, err := q.on().ExecContext(ctx, q.s.rebind(query), args...)
	return r, storeErr(err)
}

func (q *sqlTx) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r, err := q.on().QueryContext(ctx, q.s.rebind(query), args...)
	return r, storeErr(err)
}

func (q *sqlTx) row(ctx context.Context, query string, args ...any) *sql.Row {
	return q.on().QueryRowContext(ctx, q.s.rebind(query), args...)
}

// forUpdate is the clause that locks the rows a read-modify-write reads, on
// PostgreSQL; SQLite's transaction already holds the database.
func (s *sqlStore) forUpdate() string {
	if s.d == postgresDialect {
		return " FOR UPDATE"
	}
	return ""
}

// forUpdateOf is forUpdate for a read that joins: it locks table's rows
// alone, not those it was joined to.
func (s *sqlStore) forUpdateOf(table string) string {
	if s.d == postgresDialect {
		return " FOR UPDATE OF " + table
	}
	return ""
}

// rebind rewrites ? placeholders as $1, $2… for PostgreSQL. No query here
// has a ? inside a string literal.
func (s *sqlStore) rebind(q string) string {
	if s.d != postgresDialect || !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	n := 0
	for _, c := range q {
		if c == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// --- values ------------------------------------------------------------------------

// t is a time as the dialect stores it, to the microsecond: PostgreSQL
// keeps no finer, and both dialects must give back what they were given.
func (s *sqlStore) t(v time.Time) any {
	v = v.UTC().Truncate(time.Microsecond)
	if s.d == sqliteDialect {
		return v.UnixNano()
	}
	return v
}

// tp is a time that may be absent.
func (s *sqlStore) tp(v *time.Time) any {
	if v == nil {
		return nil
	}
	return s.t(*v)
}

// dbTime scans a time stored by either dialect.
type dbTime struct {
	T     time.Time
	Valid bool
}

func (d *dbTime) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		d.Valid = false
	case int64:
		d.T, d.Valid = time.Unix(0, x).UTC(), true
	case time.Time:
		d.T, d.Valid = x.UTC(), true
	default:
		return fmt.Errorf("a time stored as %T", v)
	}
	return nil
}

func (d dbTime) ptr() *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.T
	return &t
}

// jsonText is a value stored as JSON text (TEXT, or jsonb).
func jsonText(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// jsonCol scans a JSON column into v.
type jsonCol struct{ v any }

func (j jsonCol) Scan(src any) error {
	var b []byte
	switch x := src.(type) {
	case nil:
		return nil
	case string:
		b = []byte(x)
	case []byte:
		b = x
	default:
		return fmt.Errorf("JSON stored as %T", src)
	}
	return json.Unmarshal(b, j.v)
}

// --- errors ------------------------------------------------------------------------

// storeErr marks a failure of the database as ErrStore, keeping the cause,
// so a handler answers it 503 rather than as a refusal.
func storeErr(err error) error {
	if err == nil || errors.Is(err, ErrStore) || errors.Is(err, sql.ErrNoRows) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &storeError{err}
}

type storeError struct{ err error }

func (e *storeError) Error() string        { return ErrStore.Error() + ": " + e.err.Error() }
func (e *storeError) Unwrap() []error      { return []error{ErrStore, e.err} }
func (e *storeError) Is(target error) bool { return target == ErrStore }

// isUnique reports whether err is a broken unique or primary-key constraint.
func isUnique(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		c := se.Code()
		return c == sqlite3.SQLITE_CONSTRAINT_UNIQUE || c == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// --- migrations ----------------------------------------------------------------------

// migrate applies, in order and each in its own transaction, the migrations
// the database has not had. Two gateways starting on one PostgreSQL wait for
// each other (an advisory lock). A database whose schema is newer than this
// gateway knows is refused rather than written to.
func (s *sqlStore) migrate(ctx context.Context) error {
	dir := "migrations/sqlite"
	if s.d == postgresDialect {
		dir = "migrations/postgres"
	}
	entries, err := fs.ReadDir(migrationFiles, dir)
	if err != nil {
		return err
	}
	type mig struct {
		v    int
		name string
	}
	var migs []mig
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue // a step a migration's Go part reads (0003_*.finish)
		}
		v, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: want NNNN_name.sql", e.Name())
		}
		migs = append(migs, mig{v, e.Name()})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].v < migs[j].v })
	known := 0
	if len(migs) > 0 {
		known = migs[len(migs)-1].v
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied `+s.timeType()+` NOT NULL)`); err != nil {
		return storeErr(err)
	}
	for _, m := range migs {
		body, err := fs.ReadFile(migrationFiles, dir+"/"+m.name)
		if err != nil {
			return err
		}
		if m.v == 3 {
			if err := s.beforeMigrate3(ctx); err != nil {
				return err
			}
		}
		err = s.tx(ctx, func(q *sqlTx) error {
			if s.d == postgresDialect {
				if _, err := q.exec(ctx, "SELECT pg_advisory_xact_lock(7316290412)"); err != nil {
					return err
				}
			}
			var newest sql.NullInt64
			if err := q.row(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&newest); err != nil {
				return storeErr(err)
			}
			if int(newest.Int64) > known {
				return fmt.Errorf("the database's schema is version %d, newer than this gateway's %d: upgrade the gateway", newest.Int64, known)
			}
			var done int
			if err := q.row(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", m.v).Scan(&done); err != nil {
				return storeErr(err)
			}
			if done > 0 {
				return nil
			}
			if _, err := q.on().ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("migration %s: %w", m.name, err)
			}
			if m.v == 3 {
				if err := s.migrate3(ctx, q, dir); err != nil {
					return fmt.Errorf("migration %s: %w", m.name, err)
				}
			}
			_, err := q.exec(ctx, "INSERT INTO schema_migrations (version, applied) VALUES (?, ?)", m.v, s.t(s.now()))
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// beforeMigrate3 makes sure there is a way back before rows move: a SQLite
// file is copied aside (gateway.db.before-0003, consistent: VACUUM INTO),
// and a PostgreSQL database that holds anything is migrated only when the
// operator says it is backed up (OpenOptions.MigrateData).
func (s *sqlStore) beforeMigrate3(ctx context.Context) error {
	var done int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = 3").Scan(&done); err != nil {
		return storeErr(err)
	}
	if done > 0 {
		return nil
	}
	has, err := s.v2HasData(ctx)
	if err != nil || !has {
		return err
	}
	if s.d == sqliteDialect {
		backup := s.path + ".before-0003"
		_ = os.Remove(backup)
		if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", backup); err != nil {
			return fmt.Errorf("copying the database aside before migrating it: %w", err)
		}
		_ = os.Chmod(backup, 0o600)
		return nil
	}
	if !s.migrateData {
		return errors.New("this gateway moves the database's rows into a new shape (organisations and users as tables, migration 3); back the database up first, then run it with --store-migrate:\n" +
			"  pg_dump -Fc -f gateway-before-0003.dump <the database>\n" +
			"  sandbox-gateway … --store postgres --store-dsn-file … --store-migrate store migrate")
	}
	return nil
}

func (s *sqlStore) timeType() string {
	if s.d == postgresDialect {
		return "TIMESTAMPTZ"
	}
	return "INTEGER"
}

// SchemaVersion is the newest migration st's database has had.
func SchemaVersion(ctx context.Context, st Store) (int, error) {
	s, ok := st.(*sqlStore)
	if !ok {
		return 0, errors.New("not a database")
	}
	return s.schemaVersion(ctx)
}

func (s *sqlStore) schemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&v)
	return int(v.Int64), storeErr(err)
}
