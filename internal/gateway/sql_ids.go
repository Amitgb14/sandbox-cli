package gateway

import (
	"context"
	"database/sql"
	"errors"
)

// Organisations and users by id (migrations/*/0003_orgs_users.sql).
//
// The gateway names a tenant and a user as it always has — the tenant a key
// is in, "" for the default one, and a user name — and the store keeps them
// as rows: every tenant an organisation (default, personal or team), every
// user a name in a home organisation. These turn one into the other.
//
// An organisation's key is its tenant exactly as the gateway names it: a
// team organisation's is its name lowercased (orgRecord.tenant), a personal
// one's the tenant its first key or invite named, and the default
// organisation's "". Not "default": that is a tenant anyone may be given,
// and it is not the default tenant.

const defaultOrgKey = ""

// Kinds of organisation.
const (
	orgDefault  = "default"  // the default tenant, ""
	orgPersonal = "personal" // a tenant a key or an invite named, not made as an organisation
	orgTeam     = "team"     // made through the API (CreateOrg), with members
)

// orgKeyOf is a tenant's organisation key. Tenants that differ in case are
// different tenants, as they always were: making one into the other's
// organisation would hand one tenant's records to the other.
func orgKeyOf(tenant string) string { return tenant }

// orgID is the id of tenant's organisation. With create, a tenant not yet
// one is made one: the default organisation for "", a personal one
// otherwise. ok is false only without create.
func (q *sqlTx) orgID(ctx context.Context, tenant string, create bool) (string, bool, error) {
	key := orgKeyOf(tenant)
	var id string
	err := q.row(ctx, "SELECT id FROM orgs WHERE key = ?", key).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, storeErr(err)
	}
	if !create {
		return "", false, nil
	}
	kind, display := orgPersonal, tenant
	if key == defaultOrgKey {
		kind, display = orgDefault, "default"
	}
	id = newID("org_")
	if _, err := q.exec(ctx, "INSERT INTO orgs (id, key, display_name, kind, created) VALUES (?, ?, ?, ?, ?)",
		id, key, display, kind, q.s.t(q.s.stamp())); err != nil {
		return "", false, err
	}
	// Made as the tenant the store acts for: row-level security now knows
	// its id (asTenant found none).
	if t := q.s.tenant; t != nil && q.s.d == postgresDialect && orgKeyOf(*t) == key {
		if _, err := q.exec(ctx, "SELECT set_config('app.org', ?, true)", id); err != nil {
			return "", false, err
		}
	}
	return id, true, nil
}

// userID is the id of user in their home tenant. With create, one not yet
// a user is made one (and their tenant an organisation).
func (q *sqlTx) userID(ctx context.Context, name, homeTenant string, create bool) (string, bool, error) {
	org, ok, err := q.orgID(ctx, homeTenant, create)
	if err != nil || !ok {
		return "", false, err
	}
	var id string
	err = q.row(ctx, "SELECT id FROM users WHERE org_id = ? AND name = ?", org, name).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, storeErr(err)
	}
	if !create {
		return "", false, nil
	}
	id = newID("usr_")
	if _, err := q.exec(ctx, "INSERT INTO users (id, org_id, name, created) VALUES (?, ?, ?, ?)",
		id, org, name, q.s.t(q.s.stamp())); err != nil {
		return "", false, err
	}
	return id, true, nil
}

// ownerIDs are the organisation and the user a record with owner o belongs
// to. The user is the one of that name whose home is the organisation, or
// else its member of that name: one user of a name per organisation
// (nameTakenInOrg), so the name and the organisation are enough. With
// create, an owner who is neither is made a user of the organisation.
func (q *sqlTx) ownerIDs(ctx context.Context, o Owner, create bool) (org, user string, ok bool, err error) {
	org, ok, err = q.orgID(ctx, o.Tenant, create)
	if err != nil || !ok {
		return "", "", false, err
	}
	err = q.row(ctx, `SELECT id FROM users WHERE org_id = ? AND name = ?
		UNION ALL SELECT m.user_id FROM org_members m JOIN users u ON u.id = m.user_id WHERE m.org_id = ? AND u.name = ?
		LIMIT 1`, org, o.User, org, o.User).Scan(&user)
	if err == nil {
		return org, user, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", false, storeErr(err)
	}
	if !create {
		return org, "", false, nil
	}
	user, _, err = q.userID(ctx, o.User, o.Tenant, true)
	return org, user, err == nil, err
}

// ownerJoin is the columns and joins that name a record's owner, for a table
// aliased r with org_id and user_id: the record's organisation, and its user.
const ownerJoin = " JOIN orgs ro ON ro.id = r.org_id JOIN users ru ON ru.id = r.user_id "
