package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Organisations and policies in the database.
//
// Every tenant is an organisation (sql_ids.go); the ones made through the
// API, with members, are of kind team, and they are what Org, Orgs and
// MembershipsOf answer for. A team organisation's tenant is its name
// lowercased (its key): "Acme-Labs" and "acme-labs" are one organisation,
// whose records, labels and router host all say acme-labs, and whose name
// is shown as it was typed.

// teamOrg is the team organisation named name, without regard to case, and
// its id.
func (q *sqlTx) teamOrg(ctx context.Context, name string) (orgRecord, string, bool, error) {
	if name == "" {
		return orgRecord{}, "", false, nil
	}
	var o orgRecord
	var id string
	var created dbTime
	var by, byTenant sql.NullString
	err := q.row(ctx, `SELECT o.id, o.display_name, o.created, cu.name, ch.key FROM orgs o
		LEFT JOIN users cu ON cu.id = o.created_by LEFT JOIN orgs ch ON ch.id = cu.org_id
		WHERE o.key = ? AND o.kind = ?`, strings.ToLower(name), orgTeam).Scan(&id, &o.Name, &created, &by, &byTenant)
	if errors.Is(err, sql.ErrNoRows) {
		return orgRecord{}, "", false, nil
	}
	if err != nil {
		return orgRecord{}, "", false, storeErr(err)
	}
	o.Created, o.CreatedBy, o.CreatedByTenant = created.T, by.String, byTenant.String
	o.Members, err = q.members(ctx, id)
	return o, id, err == nil, err
}

func (q *sqlTx) org(ctx context.Context, name string) (orgRecord, bool, error) {
	o, _, ok, err := q.teamOrg(ctx, name)
	return o, ok, err
}

// members are an organisation's members, each named with their home
// organisation's tenant.
func (q *sqlTx) members(ctx context.Context, orgID string) ([]memberRecord, error) {
	rows, err := q.query(ctx, `SELECT u.name, h.key, m.role, m.added FROM org_members m
		JOIN users u ON u.id = m.user_id JOIN orgs h ON h.id = u.org_id
		WHERE m.org_id = ? ORDER BY m.added, u.name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []memberRecord
	for rows.Next() {
		var m memberRecord
		var added dbTime
		if err := rows.Scan(&m.User, &m.Tenant, &m.Role, &added); err != nil {
			return nil, storeErr(err)
		}
		m.Added = added.T
		out = append(out, m)
	}
	return out, storeErr(rows.Err())
}

// memberRole is the user id of user of userTenant, and their role in
// organisation orgID; ok is false when they are not its member.
func (q *sqlTx) memberRole(ctx context.Context, orgID, user, userTenant string) (string, string, bool, error) {
	var uid, role string
	err := q.row(ctx, `SELECT u.id, m.role FROM org_members m JOIN users u ON u.id = m.user_id JOIN orgs h ON h.id = u.org_id
		WHERE m.org_id = ? AND u.name = ? AND h.key = ?`, orgID, user, orgKeyOf(userTenant)).Scan(&uid, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, storeErr(err)
	}
	return uid, role, true, nil
}

// orgUsed is the SQL that holds when organisation o (a table alias) holds
// anyone or anything: a user at home in it, or a record of it.
const orgUsed = `(EXISTS (SELECT 1 FROM users x WHERE x.org_id = o.id)
	OR EXISTS (SELECT 1 FROM sandboxes x WHERE x.org_id = o.id)
	OR EXISTS (SELECT 1 FROM volumes x WHERE x.org_id = o.id)
	OR EXISTS (SELECT 1 FROM snapshots x WHERE x.org_id = o.id)
	OR EXISTS (SELECT 1 FROM secrets x WHERE x.org_id = o.id)
	OR EXISTS (SELECT 1 FROM jobs x WHERE x.org_id = o.id)
	OR EXISTS (SELECT 1 FROM services x WHERE x.org_id = o.id)
	OR EXISTS (SELECT 1 FROM ssh_tokens x WHERE x.org_id = o.id))`

// tenantInUse reports whether name, without regard to case, is a tenant
// anything stored belongs to: an organisation may not take over a tenant
// that already holds someone's keys or work. A personal organisation
// nothing is in yet (one an invite named) does not count.
func (q *sqlTx) tenantInUse(ctx context.Context, name string) (bool, error) {
	var n int
	err := q.row(ctx, "SELECT COUNT(*) FROM orgs o WHERE o.key <> '' AND lower(o.key) = ? AND (o.kind = ? OR "+orgUsed+")",
		strings.ToLower(name), orgTeam).Scan(&n)
	return n > 0, storeErr(err)
}

// nameTakenInOrg refuses user of userTenant joining organisation org when
// another user of the same name is in it already: a member from another
// tenant, or a user with keys whose own tenant is org.
func (q *sqlTx) nameTakenInOrg(ctx context.Context, org, user, userTenant string) error {
	if org == "" {
		return nil
	}
	org = strings.ToLower(org)
	var n int
	if err := q.row(ctx, `SELECT COUNT(*) FROM org_members m JOIN orgs o ON o.id = m.org_id
		JOIN users u ON u.id = m.user_id JOIN orgs h ON h.id = u.org_id
		WHERE o.key = ? AND o.kind = ? AND u.name = ? AND h.key <> ?`, org, orgTeam, user, orgKeyOf(userTenant)).Scan(&n); err != nil {
		return storeErr(err)
	}
	if n > 0 {
		return ErrMemberName
	}
	if userTenant != org {
		if err := q.row(ctx, `SELECT COUNT(*) FROM users u JOIN orgs h ON h.id = u.org_id
			WHERE lower(h.key) = ? AND u.name = ? AND EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id = u.id)`,
			org, user).Scan(&n); err != nil {
			return storeErr(err)
		}
		if n > 0 {
			return ErrMemberName
		}
	}
	return nil
}

func (s *sqlStore) CreateOrg(ctx context.Context, name, user, userTenant string, maxOwned int) (orgRecord, error) {
	if err := CheckOrgName(name); err != nil {
		return orgRecord{}, err
	}
	now := s.stamp()
	rec := orgRecord{Name: name, Created: now, CreatedBy: user, CreatedByTenant: userTenant,
		Members: []memberRecord{{User: user, Tenant: userTenant, Role: api.RoleOwner, Added: now}}}
	err := s.tx(ctx, func(q *sqlTx) error {
		if used, err := q.tenantInUse(ctx, name); err != nil {
			return err
		} else if used {
			return ErrOrgExists
		}
		uid, _, err := q.userID(ctx, user, userTenant, true)
		if err != nil {
			return err
		}
		if maxOwned > 0 {
			var n int
			// Made counts as well as owns, so handing an organisation to
			// someone else does not free a place for another.
			if err := q.row(ctx, `SELECT COUNT(*) FROM orgs o WHERE o.kind = ? AND (o.created_by = ?
				OR EXISTS (SELECT 1 FROM org_members m WHERE m.org_id = o.id AND m.user_id = ? AND m.role = ?))`,
				orgTeam, uid, uid, api.RoleOwner).Scan(&n); err != nil {
				return storeErr(err)
			}
			if n >= maxOwned {
				return ErrOrgCap
			}
		}
		key := rec.tenant()
		// A personal organisation of that key that nothing is in yet
		// (tenantInUse) becomes this one, with the invites that name it.
		r, err := q.exec(ctx, "UPDATE orgs SET kind = ?, display_name = ?, created = ?, created_by = ? WHERE key = ? AND kind = ?",
			orgTeam, name, s.t(now), uid, key, orgPersonal)
		if err != nil {
			return err
		}
		id := ""
		if n, _ := r.RowsAffected(); n > 0 {
			if err := q.row(ctx, "SELECT id FROM orgs WHERE key = ?", key).Scan(&id); err != nil {
				return storeErr(err)
			}
		} else {
			id = newID("org_")
			if _, err := q.exec(ctx, "INSERT INTO orgs (id, key, display_name, kind, created, created_by) VALUES (?, ?, ?, ?, ?, ?)",
				id, key, name, orgTeam, s.t(now), uid); err != nil {
				if isUnique(err) {
					return ErrOrgExists
				}
				return err
			}
		}
		_, err = q.exec(ctx, "INSERT INTO org_members (org_id, user_id, role, added) VALUES (?, ?, ?, ?)",
			id, uid, api.RoleOwner, s.t(now))
		return err
	})
	if err != nil {
		return orgRecord{}, err
	}
	return rec, nil
}

func (s *sqlStore) Org(ctx context.Context, name string) (orgRecord, bool, error) {
	var o orgRecord
	var ok bool
	err := s.read(ctx, func(q *sqlTx) error {
		var err error
		o, ok, err = q.org(ctx, name)
		return err
	})
	return o, ok, err
}

func (s *sqlStore) Orgs(ctx context.Context) ([]orgRecord, error) {
	var out []orgRecord
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT key FROM orgs WHERE kind = ? ORDER BY key", orgTeam)
		if err != nil {
			return err
		}
		var keys []string
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return storeErr(err)
			}
			keys = append(keys, k)
		}
		rows.Close()
		for _, k := range keys {
			o, ok, err := q.org(ctx, k)
			if err != nil {
				return err
			}
			if ok {
				out = append(out, o)
			}
		}
		return nil
	})
	return out, err
}

func (s *sqlStore) MemberRole(ctx context.Context, org, user, userTenant string) (string, bool, error) {
	if org == "" || user == "" {
		return "", false, nil
	}
	var role string
	var ok bool
	err := s.read(ctx, func(q *sqlTx) error {
		_, id, found, err := q.teamOrg(ctx, org)
		if err != nil || !found {
			return err
		}
		_, role, ok, err = q.memberRole(ctx, id, user, userTenant)
		return err
	})
	return role, ok, err
}

func (s *sqlStore) MembershipsOf(ctx context.Context, user, userTenant string) ([]orgMembership, error) {
	var out []orgMembership
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, `SELECT o.key, o.display_name, m.role, o.created FROM org_members m
			JOIN orgs o ON o.id = m.org_id JOIN users u ON u.id = m.user_id JOIN orgs h ON h.id = u.org_id
			WHERE o.kind = ? AND u.name = ? AND h.key = ? ORDER BY o.key`, orgTeam, user, orgKeyOf(userTenant))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m orgMembership
			var created dbTime
			if err := rows.Scan(&m.Org, &m.Name, &m.Role, &created); err != nil {
				return storeErr(err)
			}
			m.Created = created.T
			out = append(out, m)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

// lockOrg holds an organisation's row for the rest of the transaction, so
// "keeps at least one owner" is read and written in one step. SQLite's
// transaction already holds the whole database.
func (q *sqlTx) lockOrg(ctx context.Context, id string) error {
	var k string
	return storeErr(q.row(ctx, "SELECT id FROM orgs WHERE id = ?"+q.s.forUpdate(), id).Scan(&k))
}

func (q *sqlTx) owners(ctx context.Context, orgID string) (int, error) {
	var n int
	err := q.row(ctx, "SELECT COUNT(*) FROM org_members WHERE org_id = ? AND role = ?", orgID, api.RoleOwner).Scan(&n)
	return n, storeErr(err)
}

func (s *sqlStore) SetMember(ctx context.Context, org, user, userTenant, role string) (string, error) {
	prev := ""
	err := s.tx(ctx, func(q *sqlTx) error {
		o, id, ok, err := q.teamOrg(ctx, org)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoSuchOrg
		}
		if err := q.lockOrg(ctx, id); err != nil {
			return err
		}
		uid, was, ok, err := q.memberRole(ctx, id, user, userTenant)
		if err != nil {
			return err
		}
		if ok {
			prev = was
			if prev == api.RoleOwner && role != api.RoleOwner {
				n, err := q.owners(ctx, id)
				if err != nil {
					return err
				}
				if n == 1 {
					return ErrLastOwner
				}
			}
			_, err = q.exec(ctx, "UPDATE org_members SET role = ? WHERE org_id = ? AND user_id = ?", role, id, uid)
			return err
		}
		if err := q.nameTakenInOrg(ctx, o.tenant(), user, userTenant); err != nil {
			return err
		}
		if uid, _, err = q.userID(ctx, user, userTenant, true); err != nil {
			return err
		}
		_, err = q.exec(ctx, "INSERT INTO org_members (org_id, user_id, role, added) VALUES (?, ?, ?, ?)",
			id, uid, role, s.t(s.stamp()))
		if isUnique(err) {
			return ErrMemberName
		}
		return err
	})
	return prev, err
}

func (s *sqlStore) RemoveMember(ctx context.Context, org, user, userTenant string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		_, id, ok, err := q.teamOrg(ctx, org)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoSuchOrg
		}
		if err := q.lockOrg(ctx, id); err != nil {
			return err
		}
		uid, role, ok, err := q.memberRole(ctx, id, user, userTenant)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoSuchMember
		}
		if role == api.RoleOwner {
			n, err := q.owners(ctx, id)
			if err != nil {
				return err
			}
			if n == 1 {
				return ErrLastOwner
			}
		}
		_, err = q.exec(ctx, "DELETE FROM org_members WHERE org_id = ? AND user_id = ?", id, uid)
		return err
	})
}

// --- policies --------------------------------------------------------------------

func (s *sqlStore) PutPolicy(ctx context.Context, p api.UserPolicy) error {
	if err := checkPolicy(p); err != nil {
		return err
	}
	return s.tx(ctx, func(q *sqlTx) error {
		_, err := q.exec(ctx, `INSERT INTO policies (name, max_lifetime_secs) VALUES (?, ?)
			ON CONFLICT (name) DO UPDATE SET max_lifetime_secs = excluded.max_lifetime_secs`, p.Name, p.MaxLifetimeSecs)
		return err
	})
}

func (s *sqlStore) DeletePolicy(ctx context.Context, name string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		if ok, err := q.policyExists(ctx, name); err != nil {
			return err
		} else if !ok {
			return ErrNoSuchPolicy
		}
		var n int
		if err := q.row(ctx, "SELECT COUNT(*) FROM user_policies WHERE policy = ?", name).Scan(&n); err != nil {
			return storeErr(err)
		}
		if n > 0 {
			return ErrPolicyInUse
		}
		_, err := q.exec(ctx, "DELETE FROM policies WHERE name = ?", name)
		return err
	})
}

func (s *sqlStore) Policies(ctx context.Context) ([]api.UserPolicy, error) {
	var out []api.UserPolicy
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT name, max_lifetime_secs FROM policies ORDER BY name")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p api.UserPolicy
			if err := rows.Scan(&p.Name, &p.MaxLifetimeSecs); err != nil {
				return storeErr(err)
			}
			out = append(out, p)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

// assignPolicy holds user, of tenant, to policy name, or to none for "". A
// policy may be given before the user has a key: they are made a user.
func (q *sqlTx) assignPolicy(ctx context.Context, user, tenant, name string) error {
	uid, _, err := q.userID(ctx, user, tenant, name != "")
	if err != nil || uid == "" {
		return err
	}
	if _, err := q.exec(ctx, "DELETE FROM user_policies WHERE user_id = ?", uid); err != nil {
		return err
	}
	if name == "" {
		return nil
	}
	_, err = q.exec(ctx, "INSERT INTO user_policies (user_id, policy) VALUES (?, ?)", uid, name)
	return err
}

func (s *sqlStore) AssignPolicy(ctx context.Context, user, tenant, name string) error {
	if !ValidUser(user) {
		return fmt.Errorf("user %q: letters, digits and . _ @ + -, at most 64", user)
	}
	if tenant != "" && !ValidUser(tenant) {
		return fmt.Errorf("tenant %q: letters, digits and . _ @ + -, at most 64", tenant)
	}
	return s.tx(ctx, func(q *sqlTx) error {
		if name != "" {
			if ok, err := q.policyExists(ctx, name); err != nil {
				return err
			} else if !ok {
				return ErrNoSuchPolicy
			}
		}
		return q.assignPolicy(ctx, user, tenant, name)
	})
}

func (q *sqlTx) policyOf(ctx context.Context, user, keyTenant string) (api.UserPolicy, bool, error) {
	var p api.UserPolicy
	err := q.row(ctx, `SELECT p.name, p.max_lifetime_secs FROM user_policies up JOIN policies p ON p.name = up.policy
		JOIN users u ON u.id = up.user_id JOIN orgs h ON h.id = u.org_id
		WHERE u.name = ? AND h.key = ?`, user, orgKeyOf(keyTenant)).Scan(&p.Name, &p.MaxLifetimeSecs)
	if errors.Is(err, sql.ErrNoRows) {
		return api.UserPolicy{}, false, nil
	}
	if err != nil {
		return api.UserPolicy{}, false, storeErr(err)
	}
	return p, true, nil
}

func (s *sqlStore) PolicyOf(ctx context.Context, user, keyTenant string) (api.UserPolicy, bool, error) {
	var p api.UserPolicy
	var ok bool
	err := s.read(ctx, func(q *sqlTx) error {
		var err error
		p, ok, err = q.policyOf(ctx, user, keyTenant)
		return err
	})
	return p, ok, err
}

// PolicyActingIn is the policy of user acting in tenant, for a principal
// made from a record rather than a key (a job's, a service's): the user
// whose keys are in tenant or, in an organisation, its member of that name.
func (s *sqlStore) PolicyActingIn(ctx context.Context, user, tenant string) (api.UserPolicy, bool, error) {
	var p api.UserPolicy
	var ok bool
	err := s.read(ctx, func(q *sqlTx) error {
		var n int
		if err := q.row(ctx, `SELECT COUNT(*) FROM api_keys k JOIN users u ON u.id = k.user_id JOIN orgs h ON h.id = u.org_id
			WHERE u.name = ? AND h.key = ?`, user, orgKeyOf(tenant)).Scan(&n); err != nil {
			return storeErr(err)
		}
		home := tenant
		if n == 0 {
			var mt string
			err := q.row(ctx, `SELECT h.key FROM org_members m JOIN orgs o ON o.id = m.org_id
				JOIN users u ON u.id = m.user_id JOIN orgs h ON h.id = u.org_id
				WHERE o.key = ? AND o.kind = ? AND u.name = ?`, strings.ToLower(tenant), orgTeam, user).Scan(&mt)
			if err == nil {
				home = mt
			} else if !errors.Is(err, sql.ErrNoRows) {
				return storeErr(err)
			}
		}
		var err error
		p, ok, err = q.policyOf(ctx, user, home)
		return err
	})
	return p, ok, err
}
