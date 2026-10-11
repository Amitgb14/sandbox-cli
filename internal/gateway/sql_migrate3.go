package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// Migration 3: from tenants and user names in text columns to organisations
// and users as tables, every record tied to them by foreign key.
//
// The new tables (0003_orgs_users.sql) are made beside the old ones, as
// *_v3, so no name of an index or a constraint clashes. Then, in the same
// transaction: the old tables become *_v2 and the new ones take their names;
// every row is copied across, each tenant resolved to its organisation and
// each user name to its user (sql_ids.go, making them as they are met); the
// counts are checked, so a row that did not come across rolls the whole
// migration back; the old tables are dropped; and the indexes, grants and
// row-level security of 0003_orgs_users.finish are made.

// migratedTables are the tables migration 3 replaces, children before their
// parents for the drop.
var migratedTables = []string{"job_runs", "user_policies", "org_members", "invites", "ssh_tokens", "ssh_keys",
	"sandboxes", "volumes", "snapshots", "secrets", "services", "jobs", "api_keys", "orgs"}

// newTables are the tables migration 3 makes (users is new).
var newTables = append([]string{"users"}, migratedTables...)

// v2HasData reports whether the version-2 tables hold anything a copy would
// have to carry: what decides whether the migration needs a backup first.
func (s *sqlStore) v2HasData(ctx context.Context) (bool, error) {
	for _, t := range []string{"api_keys", "orgs", "sandboxes", "jobs", "secrets", "services", "invites"} {
		var n int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n); err != nil {
			return false, storeErr(err)
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}

// migrate3 runs after 0003_orgs_users.sql, in its transaction.
func (s *sqlStore) migrate3(ctx context.Context, q *sqlTx, dir string) error {
	for _, t := range migratedTables {
		if _, err := q.exec(ctx, "ALTER TABLE "+t+" RENAME TO "+t+"_v2"); err != nil {
			return fmt.Errorf("moving %s aside: %w", t, err)
		}
	}
	for _, t := range newTables {
		if _, err := q.exec(ctx, "ALTER TABLE "+t+"_v3 RENAME TO "+t); err != nil {
			return fmt.Errorf("naming %s: %w", t, err)
		}
	}
	if err := s.copyV2(ctx, q); err != nil {
		return err
	}
	for _, t := range migratedTables {
		if _, err := q.exec(ctx, "DROP TABLE "+t+"_v2"); err != nil {
			return fmt.Errorf("dropping the old %s: %w", t, err)
		}
	}
	finish, err := fs.ReadFile(migrationFiles, dir+"/0003_orgs_users.finish")
	if err != nil {
		return err
	}
	if _, err := q.on().ExecContext(ctx, string(finish)); err != nil {
		return fmt.Errorf("indexing: %w", err)
	}
	return nil
}

// copyV2 copies every version-2 row into the new tables.
func (s *sqlStore) copyV2(ctx context.Context, q *sqlTx) error {
	// Every tenant is an organisation: the default one first, then those
	// made as organisations (team), then the rest as they are met.
	if _, _, err := q.orgID(ctx, "", true); err != nil {
		return err
	}
	type orgV2 struct {
		key, name, by, byTenant string
		created                 dbTime
	}
	var orgs []orgV2
	if err := eachRow(ctx, q, "SELECT name_key, display_name, created, created_by, created_by_tenant FROM orgs_v2", func(r *sql.Rows) error {
		var o orgV2
		if err := r.Scan(&o.key, &o.name, &o.created, &o.by, &o.byTenant); err != nil {
			return err
		}
		orgs = append(orgs, o)
		return nil
	}); err != nil {
		return err
	}
	for _, o := range orgs {
		if _, err := q.exec(ctx, "INSERT INTO orgs (id, key, display_name, kind, created) VALUES (?, ?, ?, ?, ?)",
			newID("org_"), strings.ToLower(o.key), o.name, orgTeam, s.t(o.created.T)); err != nil {
			return fmt.Errorf("organisation %s: %w", o.key, err)
		}
	}

	// Users, from their keys: an account's email and password move onto
	// its user. A key's parent goes in before it.
	type keyV2 struct {
		id, hash, user, tenant, scopes string
		created, expires, lastLogin    dbTime
		revoked                        bool
		email, password, parent        sql.NullString
	}
	var keys []keyV2
	if err := eachRow(ctx, q, `SELECT id, hash, user_name, tenant, scopes, created, revoked, expires, email, password, last_login, parent
		FROM api_keys_v2 ORDER BY (parent IS NOT NULL), created, id`, func(r *sql.Rows) error {
		var k keyV2
		var scopes []string
		if err := r.Scan(&k.id, &k.hash, &k.user, &k.tenant, jsonCol{&scopes}, &k.created, &k.revoked, &k.expires,
			&k.email, &k.password, &k.lastLogin, &k.parent); err != nil {
			return err
		}
		b, err := jsonText(scopes)
		k.scopes = b
		keys = append(keys, k)
		return err
	}); err != nil {
		return err
	}
	for _, k := range keys {
		uid, _, err := q.userID(ctx, k.user, k.tenant, true)
		if err != nil {
			return fmt.Errorf("the user of key %s: %w", k.id, err)
		}
		account := k.email.Valid && k.email.String != "" && !k.parent.Valid
		var parent any
		if k.parent.Valid {
			parent = k.parent.String
		}
		if _, err := q.exec(ctx, `INSERT INTO api_keys (id, hash, user_id, scopes, created, revoked, expires, account, parent)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, k.id, k.hash, uid, k.scopes, s.t(k.created.T), k.revoked,
			s.tp(k.expires.ptr()), account, parent); err != nil {
			return fmt.Errorf("key %s: %w", k.id, err)
		}
		if account {
			email := strings.ToLower(k.email.String)
			// One user holds an address: a live account takes it from a
			// revoked one of another user's, and a revoked one does not take
			// it from anybody.
			cond := " AND NOT EXISTS (SELECT 1 FROM users o WHERE o.email = ? AND o.id <> ?)"
			if !k.revoked {
				if _, err := q.exec(ctx, "UPDATE users SET email = NULL, password = NULL WHERE email = ? AND id <> ?", email, uid); err != nil {
					return err
				}
			}
			if _, err := q.exec(ctx, "UPDATE users SET email = ?, password = ?, last_login = ? WHERE id = ?"+cond,
				email, k.password.String, s.tp(k.lastLogin.ptr()), uid, email, uid); err != nil {
				return err
			}
		}
	}
	// Who made each organisation, now a user.
	for _, o := range orgs {
		if o.by == "" {
			continue
		}
		uid, _, err := q.userID(ctx, o.by, o.byTenant, true)
		if err != nil {
			return err
		}
		if _, err := q.exec(ctx, "UPDATE orgs SET created_by = ? WHERE key = ?", uid, strings.ToLower(o.key)); err != nil {
			return err
		}
	}

	type member struct {
		org, user, tenant, role string
		added                   dbTime
	}
	var members []member
	if err := eachRow(ctx, q, "SELECT org, user_name, user_tenant, role, added FROM org_members_v2", func(r *sql.Rows) error {
		var m member
		if err := r.Scan(&m.org, &m.user, &m.tenant, &m.role, &m.added); err != nil {
			return err
		}
		members = append(members, m)
		return nil
	}); err != nil {
		return err
	}
	for _, m := range members {
		oid, _, err := q.orgID(ctx, m.org, true)
		if err != nil {
			return err
		}
		uid, _, err := q.userID(ctx, m.user, m.tenant, true)
		if err != nil {
			return err
		}
		if _, err := q.exec(ctx, "INSERT INTO org_members (org_id, user_id, role, added) VALUES (?, ?, ?, ?)",
			oid, uid, m.role, s.t(m.added.T)); err != nil {
			return fmt.Errorf("member %s of %s: %w", m.user, m.org, err)
		}
	}

	// Records, each to its organisation and its user.
	copyOwned := func(table, cols, insert string, extra func(r *sql.Rows) ([]any, Owner, error)) error {
		var rows [][]any
		var owners []Owner
		if err := eachRow(ctx, q, "SELECT "+cols+" FROM "+table+"_v2", func(r *sql.Rows) error {
			vals, o, err := extra(r)
			rows, owners = append(rows, vals), append(owners, o)
			return err
		}); err != nil {
			return err
		}
		for i, vals := range rows {
			oid, uid, _, err := q.ownerIDs(ctx, owners[i], true)
			if err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}
			args := append([]any{oid, uid}, vals...)
			if _, err := q.exec(ctx, insert, args...); err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}
		}
		return nil
	}
	if err := copyOwned("sandboxes", `id, tenant, user_name, node, cpus, memory_mb, disk_mb, recorded, name, image, state, labels, network,
		lifetime_secs, expires_at, idle_timeout_secs, created_at, details_updated, terminated_at, terminated_reason`,
		`INSERT INTO sandboxes (org_id, user_id, id, node, cpus, memory_mb, disk_mb, recorded, name, image, state, labels, network,
		lifetime_secs, expires_at, idle_timeout_secs, created_at, details_updated, terminated_at, terminated_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		func(r *sql.Rows) ([]any, Owner, error) {
			var o Owner
			var id, node, name, image, state, reason string
			var labels, network jsonRaw
			var cpus float64
			var mem, disk, life, idle int
			var recorded, expiresAt, createdAt, detailsUpdated, terminatedAt dbTime
			err := r.Scan(&id, &o.Tenant, &o.User, &node, &cpus, &mem, &disk, &recorded, &name, &image, &state, &labels, &network,
				&life, &expiresAt, &idle, &createdAt, &detailsUpdated, &terminatedAt, &reason)
			return []any{id, node, cpus, mem, disk, s.t(recorded.T), name, image, state, string(labels), string(network),
				life, s.tp(expiresAt.ptr()), idle, s.tp(createdAt.ptr()), s.tp(detailsUpdated.ptr()), s.tp(terminatedAt.ptr()), reason}, o, err
		}); err != nil {
		return err
	}
	for _, t := range []string{"volumes", "snapshots"} {
		key := "name"
		if t == "snapshots" {
			key = "id"
		}
		if err := copyOwned(t, key+", tenant, user_name, node", "INSERT INTO "+t+" (org_id, user_id, "+key+", node) VALUES (?, ?, ?, ?)",
			func(r *sql.Rows) ([]any, Owner, error) {
				var o Owner
				var k, node string
				err := r.Scan(&k, &o.Tenant, &o.User, &node)
				return []any{k, node}, o, err
			}); err != nil {
			return err
		}
	}
	if err := copyOwned("jobs", "id, tenant, user_name, spec, env_names, env_sealed, state, error, cancelled, created, finished",
		`INSERT INTO jobs (org_id, user_id, id, spec, env_names, env_sealed, state, error, cancelled, created, finished)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		func(r *sql.Rows) ([]any, Owner, error) {
			var o Owner
			var id, state, errText string
			var spec, names jsonRaw
			var sealed []byte
			var cancelled bool
			var created, finished dbTime
			err := r.Scan(&id, &o.Tenant, &o.User, &spec, &names, &sealed, &state, &errText, &cancelled, &created, &finished)
			return []any{id, string(spec), string(names), sealed, state, errText, cancelled, s.t(created.T), s.tp(finished.ptr())}, o, err
		}); err != nil {
		return err
	}
	if _, err := q.exec(ctx, `INSERT INTO job_runs (org_id, job, n, run) SELECT j.org_id, r.job, r.n, r.run
		FROM job_runs_v2 r JOIN jobs j ON j.id = r.job`); err != nil {
		return fmt.Errorf("job runs: %w", err)
	}
	if err := copyOwned("services", "name, tenant, user_name, record", "INSERT INTO services (org_id, user_id, name, record) VALUES (?, ?, ?, ?)",
		func(r *sql.Rows) ([]any, Owner, error) {
			var o Owner
			var name string
			var rec jsonRaw
			err := r.Scan(&name, &o.Tenant, &o.User, &rec)
			return []any{name, string(rec)}, o, err
		}); err != nil {
		return err
	}

	// What belongs to an organisation, or to a user, alone.
	type secret struct {
		tenant, name string
		sealed       []byte
		updated      dbTime
	}
	var secrets []secret
	if err := eachRow(ctx, q, "SELECT tenant, name, sealed, updated FROM secrets_v2", func(r *sql.Rows) error {
		var x secret
		err := r.Scan(&x.tenant, &x.name, &x.sealed, &x.updated)
		secrets = append(secrets, x)
		return err
	}); err != nil {
		return err
	}
	for _, x := range secrets {
		oid, _, err := q.orgID(ctx, x.tenant, true)
		if err != nil {
			return err
		}
		if _, err := q.exec(ctx, "INSERT INTO secrets (org_id, name, sealed, updated) VALUES (?, ?, ?, ?)", oid, x.name, x.sealed, s.t(x.updated.T)); err != nil {
			return fmt.Errorf("secret %s: %w", x.name, err)
		}
	}
	type sshKey struct {
		id, user, tenant, fp, line, sandbox string
		created                             dbTime
	}
	var sshKeys []sshKey
	if err := eachRow(ctx, q, "SELECT id, user_name, tenant, fingerprint, authorized_key, sandbox, created FROM ssh_keys_v2", func(r *sql.Rows) error {
		var k sshKey
		err := r.Scan(&k.id, &k.user, &k.tenant, &k.fp, &k.line, &k.sandbox, &k.created)
		sshKeys = append(sshKeys, k)
		return err
	}); err != nil {
		return err
	}
	for _, k := range sshKeys {
		uid, _, err := q.userID(ctx, k.user, k.tenant, true)
		if err != nil {
			return err
		}
		if _, err := q.exec(ctx, "INSERT INTO ssh_keys (id, user_id, fingerprint, authorized_key, sandbox, created) VALUES (?, ?, ?, ?, ?, ?)",
			k.id, uid, k.fp, k.line, k.sandbox, s.t(k.created.T)); err != nil {
			return err
		}
	}
	type token struct {
		hash, user, tenant, sandbox string
		expires                     dbTime
	}
	var tokens []token
	if err := eachRow(ctx, q, "SELECT hash, user_name, tenant, sandbox, expires FROM ssh_tokens_v2", func(r *sql.Rows) error {
		var x token
		err := r.Scan(&x.hash, &x.user, &x.tenant, &x.sandbox, &x.expires)
		tokens = append(tokens, x)
		return err
	}); err != nil {
		return err
	}
	for _, x := range tokens {
		if !x.expires.T.After(time.Now()) {
			continue // a token past its expiry would be swept anyway
		}
		oid, uid, _, err := q.ownerIDs(ctx, Owner{User: x.user, Tenant: x.tenant}, true)
		if err != nil {
			return err
		}
		if _, err := q.exec(ctx, "INSERT INTO ssh_tokens (hash, user_id, org_id, sandbox, expires) VALUES (?, ?, ?, ?, ?)",
			x.hash, uid, oid, x.sandbox, s.t(x.expires.T)); err != nil {
			return err
		}
	}
	type userPolicy struct{ user, tenant, policy string }
	var ups []userPolicy
	if err := eachRow(ctx, q, "SELECT user_name, tenant, policy FROM user_policies_v2", func(r *sql.Rows) error {
		var x userPolicy
		err := r.Scan(&x.user, &x.tenant, &x.policy)
		ups = append(ups, x)
		return err
	}); err != nil {
		return err
	}
	for _, x := range ups {
		uid, _, err := q.userID(ctx, x.user, x.tenant, true)
		if err != nil {
			return err
		}
		if _, err := q.exec(ctx, "INSERT INTO user_policies (user_id, policy) VALUES (?, ?)", uid, x.policy); err != nil {
			return err
		}
	}
	type invite struct {
		id, hash, email, user, tenant, scopes, policy string
		expiresIn                                     int
		key                                           sql.NullString
		created, expires                              dbTime
	}
	var invites []invite
	if err := eachRow(ctx, q, `SELECT id, hash, email, user_name, tenant, scopes, policy, expires_in_secs, account_key, created, expires
		FROM invites_v2`, func(r *sql.Rows) error {
		var x invite
		var scopes jsonRaw
		err := r.Scan(&x.id, &x.hash, &x.email, &x.user, &x.tenant, &scopes, &x.policy, &x.expiresIn, &x.key, &x.created, &x.expires)
		x.scopes = string(scopes)
		invites = append(invites, x)
		return err
	}); err != nil {
		return err
	}
	for _, x := range invites {
		oid, _, err := q.orgID(ctx, x.tenant, true)
		if err != nil {
			return err
		}
		var key any
		if x.key.Valid {
			key = x.key.String
		}
		if _, err := q.exec(ctx, `INSERT INTO invites (id, hash, email, org_id, user_name, scopes, policy, expires_in_secs, account_key, created, expires)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, x.id, x.hash, x.email, oid, x.user, x.scopes, x.policy, x.expiresIn, key,
			s.t(x.created.T), s.t(x.expires.T)); err != nil {
			return err
		}
	}

	// Nothing left behind: every row of every table came across (an expired
	// SSH token excepted), or the migration is not done.
	for _, t := range []string{"api_keys", "org_members", "sandboxes", "volumes", "snapshots", "jobs", "job_runs", "services",
		"secrets", "ssh_keys", "user_policies", "invites"} {
		var old, now int
		if err := q.row(ctx, "SELECT COUNT(*) FROM "+t+"_v2").Scan(&old); err != nil {
			return storeErr(err)
		}
		if err := q.row(ctx, "SELECT COUNT(*) FROM "+t).Scan(&now); err != nil {
			return storeErr(err)
		}
		if old != now {
			return fmt.Errorf("migration 3: %s has %d rows, of %d before; nothing was changed", t, now, old)
		}
	}
	return nil
}

// eachRow runs fn on every row of query, then closes the rows: the next
// statement may need the connection (SQLite has one, in a transaction).
func eachRow(ctx context.Context, q *sqlTx, query string, fn func(*sql.Rows) error) error {
	rows, err := q.query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return storeErr(rows.Err())
}

// jsonRaw scans a JSON column as its text, to write it back unchanged.
type jsonRaw []byte

func (j *jsonRaw) Scan(src any) error {
	switch x := src.(type) {
	case nil:
		*j = []byte("null")
	case string:
		*j = []byte(x)
	case []byte:
		*j = append([]byte(nil), x...)
	default:
		return fmt.Errorf("JSON stored as %T", src)
	}
	return nil
}
