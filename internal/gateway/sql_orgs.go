package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Organisations and policies in the database. An organisation's tenant is
// its name lowercased (name_key): "Acme-Labs" and "acme-labs" are one
// organisation, whose records, labels and router host all say acme-labs,
// and whose name is shown as it was typed.

func (q *sqlTx) org(ctx context.Context, name string) (orgRecord, bool, error) {
	if name == "" {
		return orgRecord{}, false, nil
	}
	var o orgRecord
	var key string
	var created dbTime
	err := q.row(ctx, "SELECT name_key, display_name, created, created_by, created_by_tenant FROM orgs WHERE name_key = ?",
		strings.ToLower(name)).Scan(&key, &o.Name, &created, &o.CreatedBy, &o.CreatedByTenant)
	if errors.Is(err, sql.ErrNoRows) {
		return orgRecord{}, false, nil
	}
	if err != nil {
		return orgRecord{}, false, storeErr(err)
	}
	o.Created = created.T
	o.Members, err = q.members(ctx, key)
	return o, err == nil, err
}

func (q *sqlTx) members(ctx context.Context, key string) ([]memberRecord, error) {
	rows, err := q.query(ctx, "SELECT user_name, user_tenant, role, added FROM org_members WHERE org = ? ORDER BY added, user_name", key)
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

// tenantInUse reports whether name, without regard to case, is a tenant
// anything stored belongs to: an organisation may not take over a tenant
// that already holds someone's keys or work.
func (q *sqlTx) tenantInUse(ctx context.Context, name string) (bool, error) {
	key := strings.ToLower(name)
	for _, t := range []string{"api_keys", "ssh_keys", "ssh_tokens", "sandboxes", "volumes", "snapshots", "secrets", "jobs", "services"} {
		var n int
		if err := q.row(ctx, "SELECT COUNT(*) FROM "+t+" WHERE tenant <> '' AND lower(tenant) = ?", key).Scan(&n); err != nil {
			return false, storeErr(err)
		}
		if n > 0 {
			return true, nil
		}
	}
	var n int
	err := q.row(ctx, "SELECT COUNT(*) FROM orgs WHERE name_key = ?", key).Scan(&n)
	return n > 0, storeErr(err)
}

// nameTakenInOrg refuses user of userTenant joining organisation org when
// another user of the same name is in it already: a member from another
// tenant, or a key whose own tenant is org.
func (q *sqlTx) nameTakenInOrg(ctx context.Context, org, user, userTenant string) error {
	if org == "" {
		return nil
	}
	org = strings.ToLower(org)
	var n int
	if err := q.row(ctx, "SELECT COUNT(*) FROM org_members WHERE org = ? AND user_name = ? AND user_tenant <> ?",
		org, user, userTenant).Scan(&n); err != nil {
		return storeErr(err)
	}
	if n > 0 {
		return ErrMemberName
	}
	if userTenant != org {
		if err := q.row(ctx, "SELECT COUNT(*) FROM api_keys WHERE lower(tenant) = ? AND user_name = ?", org, user).Scan(&n); err != nil {
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
		if maxOwned > 0 {
			var n int
			// Made counts as well as owns, so handing an organisation to
			// someone else does not free a place for another.
			if err := q.row(ctx, `SELECT COUNT(*) FROM orgs o WHERE (o.created_by = ? AND o.created_by_tenant = ?)
				OR EXISTS (SELECT 1 FROM org_members m WHERE m.org = o.name_key AND m.user_name = ? AND m.user_tenant = ? AND m.role = ?)`,
				user, userTenant, user, userTenant, api.RoleOwner).Scan(&n); err != nil {
				return storeErr(err)
			}
			if n >= maxOwned {
				return ErrOrgCap
			}
		}
		key := rec.tenant()
		if _, err := q.exec(ctx, "INSERT INTO orgs (name_key, display_name, created, created_by, created_by_tenant) VALUES (?, ?, ?, ?, ?)",
			key, name, s.t(now), user, userTenant); err != nil {
			if isUnique(err) {
				return ErrOrgExists
			}
			return err
		}
		_, err := q.exec(ctx, "INSERT INTO org_members (org, user_name, user_tenant, role, added) VALUES (?, ?, ?, ?, ?)",
			key, user, userTenant, api.RoleOwner, s.t(now))
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
		rows, err := q.query(ctx, "SELECT name_key FROM orgs ORDER BY name_key")
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
	err := s.read(ctx, func(q *sqlTx) error {
		return storeErr(q.row(ctx, "SELECT role FROM org_members WHERE org = ? AND user_name = ? AND user_tenant = ?",
			strings.ToLower(org), user, userTenant).Scan(&role))
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return role, err == nil, err
}

func (s *sqlStore) MembershipsOf(ctx context.Context, user, userTenant string) ([]orgMembership, error) {
	var out []orgMembership
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, `SELECT o.name_key, o.display_name, m.role, o.created FROM org_members m JOIN orgs o ON o.name_key = m.org
			WHERE m.user_name = ? AND m.user_tenant = ? ORDER BY o.name_key`, user, userTenant)
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
func (q *sqlTx) lockOrg(ctx context.Context, key string) error {
	var k string
	return storeErr(q.row(ctx, "SELECT name_key FROM orgs WHERE name_key = ?"+q.s.forUpdate(), key).Scan(&k))
}

func (q *sqlTx) owners(ctx context.Context, org string) (int, error) {
	var n int
	err := q.row(ctx, "SELECT COUNT(*) FROM org_members WHERE org = ? AND role = ?", org, api.RoleOwner).Scan(&n)
	return n, storeErr(err)
}

func (s *sqlStore) SetMember(ctx context.Context, org, user, userTenant, role string) (string, error) {
	prev := ""
	err := s.tx(ctx, func(q *sqlTx) error {
		o, ok, err := q.org(ctx, org)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoSuchOrg
		}
		key := o.tenant()
		if err := q.lockOrg(ctx, key); err != nil {
			return err
		}
		err = q.row(ctx, "SELECT role FROM org_members WHERE org = ? AND user_name = ? AND user_tenant = ?", key, user, userTenant).Scan(&prev)
		if err == nil {
			if prev == api.RoleOwner && role != api.RoleOwner {
				n, err := q.owners(ctx, key)
				if err != nil {
					return err
				}
				if n == 1 {
					return ErrLastOwner
				}
			}
			_, err = q.exec(ctx, "UPDATE org_members SET role = ? WHERE org = ? AND user_name = ? AND user_tenant = ?", role, key, user, userTenant)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return storeErr(err)
		}
		if err := q.nameTakenInOrg(ctx, key, user, userTenant); err != nil {
			return err
		}
		_, err = q.exec(ctx, "INSERT INTO org_members (org, user_name, user_tenant, role, added) VALUES (?, ?, ?, ?, ?)",
			key, user, userTenant, role, s.t(s.stamp()))
		if isUnique(err) {
			return ErrMemberName
		}
		return err
	})
	return prev, err
}

func (s *sqlStore) RemoveMember(ctx context.Context, org, user, userTenant string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		key := strings.ToLower(org)
		var exists int
		if err := q.row(ctx, "SELECT COUNT(*) FROM orgs WHERE name_key = ?", key).Scan(&exists); err != nil {
			return storeErr(err)
		}
		if exists == 0 {
			return ErrNoSuchOrg
		}
		if err := q.lockOrg(ctx, key); err != nil {
			return err
		}
		var role string
		err := q.row(ctx, "SELECT role FROM org_members WHERE org = ? AND user_name = ? AND user_tenant = ?", key, user, userTenant).Scan(&role)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoSuchMember
		}
		if err != nil {
			return storeErr(err)
		}
		if role == api.RoleOwner {
			n, err := q.owners(ctx, key)
			if err != nil {
				return err
			}
			if n == 1 {
				return ErrLastOwner
			}
		}
		_, err = q.exec(ctx, "DELETE FROM org_members WHERE org = ? AND user_name = ? AND user_tenant = ?", key, user, userTenant)
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

func (q *sqlTx) assignPolicy(ctx context.Context, user, tenant, name string) error {
	if _, err := q.exec(ctx, "DELETE FROM user_policies WHERE user_name = ? AND tenant = ?", user, tenant); err != nil {
		return err
	}
	if name == "" {
		return nil
	}
	_, err := q.exec(ctx, "INSERT INTO user_policies (user_name, tenant, policy) VALUES (?, ?, ?)", user, tenant, name)
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
	err := q.row(ctx, `SELECT p.name, p.max_lifetime_secs FROM user_policies u JOIN policies p ON p.name = u.policy
		WHERE u.user_name = ? AND u.tenant = ?`, user, keyTenant).Scan(&p.Name, &p.MaxLifetimeSecs)
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
		if err := q.row(ctx, "SELECT COUNT(*) FROM api_keys WHERE user_name = ? AND tenant = ?", user, tenant).Scan(&n); err != nil {
			return storeErr(err)
		}
		home := tenant
		if n == 0 {
			var mt string
			err := q.row(ctx, "SELECT user_tenant FROM org_members WHERE org = ? AND user_name = ?", strings.ToLower(tenant), user).Scan(&mt)
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
