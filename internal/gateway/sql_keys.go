package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// API keys, accounts and invites in the database: the same rules as the
// state file had (accounts.go explains accounts), each change one
// transaction.
//
// A key belongs to a user (api_keys.user_id), and its tenant is the user's
// home organisation. An account is a user with an address and a password,
// and the one live key marked account stands for it: Key.Email, Password
// and LastLogin are the user's, read through that key.

// keySelect reads keys as k, with their users (u) and home organisations
// (h). A where clause names k's columns as k.<column>.
const keySelect = `SELECT k.id, k.hash, u.name, h.key, k.scopes, k.created, k.revoked, k.expires, k.account,
	u.email, u.password, u.last_login, k.parent, k.restore_until
	FROM api_keys k JOIN users u ON u.id = k.user_id JOIN orgs h ON h.id = u.org_id `

func scanKey(sc interface{ Scan(...any) error }) (Key, error) {
	var k Key
	var created, expires, lastLogin, restore dbTime
	var email, password, parent sql.NullString
	var account bool
	err := sc.Scan(&k.ID, &k.Hash, &k.User, &k.Tenant, jsonCol{&k.Scopes}, &created, &k.Revoked, &expires, &account,
		&email, &password, &lastLogin, &parent, &restore)
	if err != nil {
		return Key{}, err
	}
	k.Created, k.Expires, k.Parent = created.T, expires.ptr(), parent.String
	if k.Revoked {
		k.RestoreUntil = restore.ptr()
	}
	if account {
		k.Email, k.Password, k.LastLogin = email.String, password.String, lastLogin.ptr()
	}
	return k, nil
}

func (q *sqlTx) keys(ctx context.Context, where string, args ...any) ([]Key, error) {
	rows, err := q.query(ctx, keySelect+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Key
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, storeErr(err)
		}
		out = append(out, k)
	}
	return out, storeErr(rows.Err())
}

func (q *sqlTx) key(ctx context.Context, where string, args ...any) (Key, bool, error) {
	k, err := scanKey(q.row(ctx, keySelect+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, false, nil
	}
	if err != nil {
		return Key{}, false, storeErr(err)
	}
	return k, true, nil
}

// insertKey adds k, under its user (made one, in an organisation of its
// tenant, if not yet). A key with an address is the user's account: the
// address and password go on the user, taken from any user whose account
// was revoked. A unique index refuses a second live account for an address
// or a user.
func (q *sqlTx) insertKey(ctx context.Context, k Key) error {
	scopes, err := jsonText(k.Scopes)
	if err != nil {
		return err
	}
	uid, _, err := q.userID(ctx, k.User, k.Tenant, true)
	if err != nil {
		return err
	}
	var parent any
	if k.Parent != "" {
		parent = k.Parent
	}
	account := k.Email != ""
	if account {
		if _, err := q.exec(ctx, `UPDATE users SET email = NULL, password = NULL WHERE email = ? AND id <> ?
			AND NOT EXISTS (SELECT 1 FROM api_keys a WHERE a.user_id = users.id AND a.account AND (NOT a.revoked OR a.restore_until > ?))`, k.Email, uid, q.s.t(q.s.stamp())); err != nil {
			return err
		}
		_, err := q.exec(ctx, "UPDATE users SET email = ?, password = ?, last_login = ? WHERE id = ?",
			k.Email, k.Password, q.s.tp(k.LastLogin), uid)
		if isUnique(err) {
			return ErrEmailTaken
		}
		if err != nil {
			return err
		}
	}
	_, err = q.exec(ctx, `INSERT INTO api_keys (id, hash, user_id, scopes, created, revoked, expires, account, parent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.Hash, uid, scopes, q.s.t(k.Created), k.Revoked, q.s.tp(k.Expires), account, parent)
	if isUnique(err) && account {
		return ErrEmailTaken
	}
	return err
}

func (s *sqlStore) CreateKey(ctx context.Context, user, tenant string, scopes []string) (string, Key, error) {
	return s.CreateKeyExpiring(ctx, user, tenant, scopes, nil)
}

func (s *sqlStore) CreateKeyExpiring(ctx context.Context, user, tenant string, scopes []string, expires *time.Time) (string, Key, error) {
	if !ValidUser(user) {
		return "", Key{}, fmt.Errorf("user %q: letters, digits and . _ @ + -, at most 64", user)
	}
	if tenant != "" && !ValidUser(tenant) {
		return "", Key{}, fmt.Errorf("tenant %q: letters, digits and . _ @ + -, at most 64", tenant)
	}
	if err := ValidateScopes(scopes); err != nil {
		return "", Key{}, err
	}
	secret := newSecret(keySecretPrefix)
	k := Key{ID: newID("key_"), User: user, Tenant: tenant, Scopes: slices.Clone(scopes),
		Hash: hashSecret(secret), Created: s.stamp()}
	if expires != nil {
		if !expires.After(k.Created) {
			return "", Key{}, errors.New("a key's expiry must be in the future")
		}
		t := expires.UTC().Truncate(time.Microsecond)
		k.Expires = &t
	}
	err := s.tx(ctx, func(q *sqlTx) error {
		// A key issued into an organisation's tenant is its member; an org's
		// tenant is its lowercased name.
		if o, ok, err := q.org(ctx, tenant); err != nil {
			return err
		} else if ok {
			k.Tenant = o.tenant()
		}
		if err := q.nameTakenInOrg(ctx, k.Tenant, user, k.Tenant); err != nil {
			return err
		}
		return q.insertKey(ctx, k)
	})
	if err != nil {
		return "", Key{}, err
	}
	return secret, k, nil
}

// KeyBySecret is an indexed lookup on the secret's hash: the SHA-256 of 256
// random bits, so how long a miss takes says nothing a guess could use.
func (s *sqlStore) KeyBySecret(ctx context.Context, secret string) (Key, bool, error) {
	if !strings.HasPrefix(secret, keySecretPrefix) {
		return Key{}, false, nil
	}
	var k Key
	var ok bool
	err := s.read(ctx, func(q *sqlTx) error {
		var err error
		k, ok, err = q.key(ctx, "WHERE k.hash = ?", hashSecret(secret))
		if err != nil || !ok {
			return err
		}
		now := s.now()
		if !k.Active(now) {
			ok = false
			return nil
		}
		// A sign-in's key is cut off with its account.
		if k.Parent != "" {
			a, found, err := q.key(ctx, "WHERE k.id = ?", k.Parent)
			if err != nil {
				return err
			}
			if !found || !a.Active(now) {
				ok = false
			}
		}
		return nil
	})
	if err != nil || !ok {
		return Key{}, false, err
	}
	return k, true, nil
}

func (s *sqlStore) RevokeKey(ctx context.Context, id string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		// For good, a suspension's too: nothing is left to restore.
		r, err := q.exec(ctx, "UPDATE api_keys SET revoked = ?, restore_until = NULL WHERE id = ?", true, id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNoSuchKey
		}
		_, err = q.exec(ctx, "UPDATE api_keys SET revoked = ? WHERE parent = ?", true, id)
		return err
	})
}

func (s *sqlStore) SetKeyExpiry(ctx context.Context, id string, at *time.Time) (Key, error) {
	var out Key
	err := s.tx(ctx, func(q *sqlTx) error {
		k, ok, err := q.key(ctx, "WHERE k.id = ?"+s.forUpdateOf("k"), id)
		if err != nil {
			return err
		}
		switch {
		case !ok:
			return ErrNoSuchKey
		case k.Revoked:
			return ErrKeyRevoked
		}
		if at != nil {
			t := at.UTC().Truncate(time.Microsecond)
			at = &t
		}
		if _, err := q.exec(ctx, "UPDATE api_keys SET expires = ? WHERE id = ?", s.tp(at), id); err != nil {
			return err
		}
		// An account's sign-ins end no later than it does; a renewal does not
		// lengthen them.
		if at != nil {
			if _, err := q.exec(ctx, "UPDATE api_keys SET expires = ? WHERE parent = ? AND (expires IS NULL OR expires > ?)",
				s.t(*at), id, s.t(*at)); err != nil {
				return err
			}
		}
		k.Expires = at
		out = k
		return nil
	})
	return out, err
}

func (s *sqlStore) Keys(ctx context.Context) ([]Key, error) {
	var out []Key
	err := s.read(ctx, func(q *sqlTx) error {
		var err error
		out, err = q.keys(ctx, "ORDER BY k.created, k.id")
		return err
	})
	return out, err
}

func (s *sqlStore) KeysOfUser(ctx context.Context, user string) ([]Key, error) {
	var out []Key
	err := s.read(ctx, func(q *sqlTx) error {
		var err error
		out, err = q.keys(ctx, "WHERE u.name = ? ORDER BY k.created, k.id", user)
		return err
	})
	return out, err
}

// --- accounts --------------------------------------------------------------------

func (s *sqlStore) Account(ctx context.Context, email string) (Key, bool, error) {
	var k Key
	var ok bool
	err := s.read(ctx, func(q *sqlTx) error {
		var err error
		k, ok, err = q.key(ctx, "WHERE u.email = ? AND k.account AND NOT k.revoked", strings.ToLower(email))
		return err
	})
	return k, ok, err
}

func (s *sqlStore) AccountOf(ctx context.Context, keyID string) (Key, bool, error) {
	var out Key
	var ok bool
	err := s.read(ctx, func(q *sqlTx) error {
		k, found, err := q.key(ctx, "WHERE k.id = ?", keyID)
		if err != nil || !found {
			return err
		}
		if k.Parent != "" {
			if k, found, err = q.key(ctx, "WHERE k.id = ?", k.Parent); err != nil || !found {
				return err
			}
		}
		out, ok = k, k.Email != ""
		return nil
	})
	return out, ok, err
}

func (s *sqlStore) SignIn(ctx context.Context, account string, ttl time.Duration) (string, Key, error) {
	now := s.stamp()
	secret := newSecret(keySecretPrefix)
	var out Key
	err := s.tx(ctx, func(q *sqlTx) error {
		acct, ok, err := q.key(ctx, "WHERE k.id = ?"+s.forUpdateOf("k"), account)
		if err != nil {
			return err
		}
		if !ok || acct.Email == "" {
			return ErrNotAccount
		}
		if !acct.Active(now) {
			return ErrKeyRevoked
		}
		end := now.Add(ttl)
		if acct.Expires != nil && acct.Expires.Before(end) {
			end = *acct.Expires
		}
		if _, err := q.exec(ctx, "UPDATE users SET last_login = ? WHERE id = (SELECT user_id FROM api_keys WHERE id = ?)", s.t(now), acct.ID); err != nil {
			return err
		}
		// Sign-ins that ended are forgotten, so they do not pile up.
		if _, err := q.exec(ctx, "DELETE FROM api_keys WHERE parent IS NOT NULL AND (revoked OR expires <= ?)", s.t(now)); err != nil {
			return err
		}
		out = Key{ID: newID("key_"), User: acct.User, Tenant: acct.Tenant, Scopes: slices.Clone(acct.Scopes),
			Hash: hashSecret(secret), Created: now, Expires: &end, Parent: acct.ID}
		return q.insertKey(ctx, out)
	})
	return secret, out, err
}

func (s *sqlStore) SignOut(ctx context.Context, id string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		r, err := q.exec(ctx, "DELETE FROM api_keys WHERE id = ? AND parent IS NOT NULL", id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNotAccount
		}
		return nil
	})
}

func (s *sqlStore) SetPassword(ctx context.Context, account, passwordHash, keep string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		r, err := q.exec(ctx, "UPDATE users SET password = ? WHERE id = (SELECT user_id FROM api_keys WHERE id = ? AND account)",
			passwordHash, account)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNotAccount
		}
		_, err = q.exec(ctx, "DELETE FROM api_keys WHERE parent = ? AND id <> ?", account, keep)
		return err
	})
}

// --- invites ---------------------------------------------------------------------

// inviteSelect reads invites as i, with the organisation each makes its
// account in (o).
const inviteSelect = `SELECT i.id, i.hash, i.email, i.user_name, o.key, i.scopes, i.policy, i.expires_in_secs, i.account_key,
	i.created, i.expires FROM invites i JOIN orgs o ON o.id = i.org_id `

func scanInvite(sc interface{ Scan(...any) error }) (inviteRecord, error) {
	var iv inviteRecord
	var created, expires dbTime
	var key sql.NullString
	err := sc.Scan(&iv.ID, &iv.Hash, &iv.Email, &iv.User, &iv.Tenant, jsonCol{&iv.Scopes}, &iv.Policy,
		&iv.ExpiresInSecs, &key, &created, &expires)
	iv.Created, iv.Expires, iv.Key = created.T, expires.T, key.String
	return iv, err
}

func (q *sqlTx) insertInvite(ctx context.Context, iv inviteRecord) error {
	scopes, err := jsonText(iv.Scopes)
	if err != nil {
		return err
	}
	var key any
	if iv.Key != "" {
		key = iv.Key
	}
	org, _, err := q.orgID(ctx, iv.Tenant, true)
	if err != nil {
		return err
	}
	_, err = q.exec(ctx, `INSERT INTO invites (id, hash, email, org_id, user_name, scopes, policy, expires_in_secs, account_key,
		created, expires) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		iv.ID, iv.Hash, iv.Email, org, iv.User, scopes, iv.Policy, iv.ExpiresInSecs, key,
		q.s.t(iv.Created), q.s.t(iv.Expires))
	return err
}

func (q *sqlTx) pruneInvites(ctx context.Context, now time.Time) error {
	_, err := q.exec(ctx, "DELETE FROM invites WHERE expires <= ?", q.s.t(now))
	return err
}

// accountFree refuses a new account whose address, or whose user, one has
// already, or whose name an organisation holds for someone else.
func (q *sqlTx) accountFree(ctx context.Context, email, user, tenant string) error {
	var n int
	if err := q.row(ctx, "SELECT COUNT(*) FROM api_keys k JOIN users u ON u.id = k.user_id WHERE u.email = ? AND k.account AND (NOT k.revoked OR k.restore_until > ?)",
		email, q.s.t(q.s.stamp())).Scan(&n); err != nil {
		return storeErr(err)
	}
	if n > 0 {
		return ErrEmailTaken
	}
	if err := q.row(ctx, `SELECT COUNT(*) FROM api_keys k JOIN users u ON u.id = k.user_id JOIN orgs h ON h.id = u.org_id
		WHERE u.name = ? AND h.key = ? AND k.account AND (NOT k.revoked OR k.restore_until > ?)`, user, orgKeyOf(tenant), q.s.t(q.s.stamp())).Scan(&n); err != nil {
		return storeErr(err)
	}
	if n > 0 {
		return ErrUserHasAccount
	}
	return q.nameTakenInOrg(ctx, tenant, user, tenant)
}

// ownTenant is the organisation a sign-up for user gets: the name
// lowercased, each run of what an organisation name cannot hold (". _ @ +")
// a dash, at most 30 characters, so john.smith is john-smith. What is left
// may still be no organisation name (a digit first, "admin"), which
// CheckOrgName says.
func ownTenant(user string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(user) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	t := strings.Trim(b.String(), "-")
	if len(t) > 30 {
		t = strings.TrimRight(t[:30], "-")
	}
	return t
}

// tenantFree refuses tenant as a new account's own: something is stored in
// it, it is a team organisation, or a pending invite for another address
// names it.
func (q *sqlTx) tenantFree(ctx context.Context, tenant, email string) error {
	if used, err := q.tenantInUse(ctx, tenant); err != nil {
		return err
	} else if used {
		return fmt.Errorf("%s: %w", tenant, ErrTenantTaken)
	}
	var n int
	if err := q.row(ctx, `SELECT COUNT(*) FROM invites i JOIN orgs o ON o.id = i.org_id
		WHERE lower(o.key) = ? AND i.account_key IS NULL AND i.email <> ?`, strings.ToLower(tenant), email).Scan(&n); err != nil {
		return storeErr(err)
	}
	if n > 0 {
		return fmt.Errorf("%s: %w", tenant, ErrTenantTaken)
	}
	return nil
}

func (q *sqlTx) policyExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := q.row(ctx, "SELECT COUNT(*) FROM policies WHERE name = ?", name).Scan(&n)
	return n > 0, storeErr(err)
}

func (s *sqlStore) CreateInvite(ctx context.Context, req api.CreateInviteRequest) (string, inviteRecord, error) {
	email, iv, err := newInvite(req, s.stamp())
	if err != nil {
		return "", inviteRecord{}, err
	}
	// An invite naming no tenant is a sign-up: the account gets an
	// organisation of its own, named after its user, that nobody else is in.
	// It is never a tenant already holding someone's work, or one another
	// pending invite names, since the new account would share its secrets
	// and services; putting an account in an existing tenant is said by
	// naming it, "default" for the default tenant.
	own := false
	switch iv.Tenant {
	case "":
		iv.Tenant, own = ownTenant(iv.User), true
		if err := CheckOrgName(iv.Tenant); err != nil {
			return "", inviteRecord{}, fmt.Errorf("user %q cannot name an organization of its own (%v); give the invite a tenant", iv.User, err)
		}
	case api.DefaultOrg:
		iv.Tenant = ""
	}
	token := newSecret(inviteSecretPrefix)
	iv.Hash = hashSecret(token)
	err = s.tx(ctx, func(q *sqlTx) error {
		if err := q.pruneInvites(ctx, iv.Created); err != nil {
			return err
		}
		if own {
			if err := q.tenantFree(ctx, iv.Tenant, email); err != nil {
				return err
			}
		}
		if o, ok, err := q.org(ctx, iv.Tenant); err != nil {
			return err
		} else if ok {
			iv.Tenant = o.tenant()
		}
		if err := q.accountFree(ctx, email, iv.User, iv.Tenant); err != nil {
			return err
		}
		if iv.Policy != "" {
			if ok, err := q.policyExists(ctx, iv.Policy); err != nil {
				return err
			} else if !ok {
				return ErrNoSuchPolicy
			}
		}
		// Only the newest invite for an address works.
		if _, err := q.exec(ctx, "DELETE FROM invites WHERE email = ? AND account_key IS NULL", email); err != nil {
			return err
		}
		return q.insertInvite(ctx, iv)
	})
	if err != nil {
		return "", inviteRecord{}, err
	}
	return token, iv, nil
}

// newInvite checks an invite request and makes its record, without a hash.
func newInvite(req api.CreateInviteRequest, now time.Time) (string, inviteRecord, error) {
	email, ok := NormalEmail(req.Email)
	switch {
	case !ok:
		return "", inviteRecord{}, fmt.Errorf("email %q: not an address", req.Email)
	case !ValidUser(req.User):
		return "", inviteRecord{}, fmt.Errorf("user %q: letters, digits and . _ @ + -, at most 64", req.User)
	case req.Tenant != "" && !ValidUser(req.Tenant):
		return "", inviteRecord{}, fmt.Errorf("tenant %q: letters, digits and . _ @ + -, at most 64", req.Tenant)
	case req.ExpiresInSecs < 0:
		return "", inviteRecord{}, errors.New("expires_in_secs must not be negative")
	case req.ValidSecs < 0 || time.Duration(req.ValidSecs)*time.Second > inviteMaxValid:
		return "", inviteRecord{}, fmt.Errorf("valid_secs: at most %d", int(inviteMaxValid/time.Second))
	}
	if err := ValidateScopes(req.Scopes); err != nil {
		return "", inviteRecord{}, err
	}
	valid := inviteValid
	if req.ValidSecs > 0 {
		valid = time.Duration(req.ValidSecs) * time.Second
	}
	return email, inviteRecord{ID: newID("inv_"), Email: email, User: req.User, Tenant: req.Tenant,
		Scopes: slices.Clone(req.Scopes), Policy: req.Policy, ExpiresInSecs: req.ExpiresInSecs,
		Created: now, Expires: now.Add(valid)}, nil
}

func (s *sqlStore) CreateResetInvite(ctx context.Context, id string, valid time.Duration) (string, inviteRecord, error) {
	now := s.stamp()
	token := newSecret(inviteSecretPrefix)
	var iv inviteRecord
	err := s.tx(ctx, func(q *sqlTx) error {
		k, ok, err := q.key(ctx, "WHERE k.id = ?", id)
		switch {
		case err != nil:
			return err
		case !ok:
			return ErrNoSuchKey
		case k.Email == "":
			return ErrNotAccount
		case k.Revoked:
			return ErrKeyRevoked
		}
		iv = inviteRecord{ID: newID("inv_"), Hash: hashSecret(token), Email: k.Email, User: k.User, Tenant: k.Tenant,
			Scopes: slices.Clone(k.Scopes), Key: k.ID, Created: now, Expires: now.Add(valid)}
		if err := q.pruneInvites(ctx, now); err != nil {
			return err
		}
		if _, err := q.exec(ctx, "DELETE FROM invites WHERE account_key = ?", id); err != nil {
			return err
		}
		return q.insertInvite(ctx, iv)
	})
	if err != nil {
		return "", inviteRecord{}, err
	}
	return token, iv, nil
}

func (s *sqlStore) RenewInvite(ctx context.Context, id string, valid time.Duration) (string, inviteRecord, error) {
	now := s.stamp()
	token := newSecret(inviteSecretPrefix)
	var iv inviteRecord
	err := s.tx(ctx, func(q *sqlTx) error {
		if err := q.pruneInvites(ctx, now); err != nil {
			return err
		}
		var err error
		iv, err = scanInvite(q.row(ctx, inviteSelect+"WHERE i.id = ?", id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoSuchInvite
		}
		if err != nil {
			return storeErr(err)
		}
		iv.Hash, iv.Expires = hashSecret(token), now.Add(valid)
		_, err = q.exec(ctx, "UPDATE invites SET hash = ?, expires = ? WHERE id = ?", iv.Hash, s.t(iv.Expires), id)
		return err
	})
	if err != nil {
		return "", inviteRecord{}, err
	}
	return token, iv, nil
}

func (s *sqlStore) Invites(ctx context.Context) ([]inviteRecord, error) {
	var out []inviteRecord
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, inviteSelect+"WHERE i.expires > ? ORDER BY i.created, i.id", s.t(s.now()))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			iv, err := scanInvite(rows)
			if err != nil {
				return storeErr(err)
			}
			out = append(out, iv)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

func (s *sqlStore) RevokeInvite(ctx context.Context, id string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		r, err := q.exec(ctx, "DELETE FROM invites WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNoSuchInvite
		}
		return nil
	})
}

func (q *sqlTx) inviteByToken(ctx context.Context, token string, now time.Time) (inviteRecord, bool, error) {
	if !strings.HasPrefix(token, inviteSecretPrefix) {
		return inviteRecord{}, false, nil
	}
	iv, err := scanInvite(q.row(ctx, inviteSelect+"WHERE i.hash = ? AND i.expires > ?",
		hashSecret(token), q.s.t(now)))
	if errors.Is(err, sql.ErrNoRows) {
		return inviteRecord{}, false, nil
	}
	if err != nil {
		return inviteRecord{}, false, storeErr(err)
	}
	return iv, true, nil
}

func (s *sqlStore) InviteByToken(ctx context.Context, token string) (inviteRecord, bool, error) {
	var iv inviteRecord
	var ok bool
	err := s.read(ctx, func(q *sqlTx) error {
		var err error
		iv, ok, err = q.inviteByToken(ctx, token, s.now())
		return err
	})
	return iv, ok, err
}

func (s *sqlStore) RedeemInvite(ctx context.Context, token, passwordHash string) (Key, error) {
	now := s.stamp()
	var out Key
	err := s.tx(ctx, func(q *sqlTx) error {
		iv, ok, err := q.inviteByToken(ctx, token, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoSuchInvite
		}
		if iv.Key != "" {
			a, ok, err := q.key(ctx, "WHERE k.id = ?", iv.Key)
			if err != nil {
				return err
			}
			if !ok || a.Revoked || a.Email != iv.Email {
				return ErrNoSuchInvite
			}
			if _, err := q.exec(ctx, "UPDATE users SET password = ? WHERE id = (SELECT user_id FROM api_keys WHERE id = ?)",
				passwordHash, a.ID); err != nil {
				return err
			}
			// A new password ends every sign-in.
			if _, err := q.exec(ctx, "DELETE FROM api_keys WHERE parent = ?", a.ID); err != nil {
				return err
			}
			a.Password = passwordHash
			out = a
		} else {
			if err := q.accountFree(ctx, iv.Email, iv.User, iv.Tenant); err != nil {
				return err
			}
			k := Key{ID: newID("key_"), User: iv.User, Tenant: iv.Tenant, Scopes: slices.Clone(iv.Scopes),
				// A secret nobody holds: the account signs in by password.
				Hash: hashSecret(newSecret(keySecretPrefix)), Created: now, Email: iv.Email, Password: passwordHash}
			if iv.ExpiresInSecs > 0 {
				t := now.Add(time.Duration(iv.ExpiresInSecs) * time.Second)
				k.Expires = &t
			}
			if iv.Policy != "" {
				// Removed since the invite was made: the account would act
				// without the limits it was invited under.
				if ok, err := q.policyExists(ctx, iv.Policy); err != nil {
					return err
				} else if !ok {
					return ErrNoSuchPolicy
				}
				if err := q.assignPolicy(ctx, iv.User, iv.Tenant, iv.Policy); err != nil {
					return err
				}
			}
			if err := q.insertKey(ctx, k); err != nil {
				return err
			}
			out = k
		}
		_, err = q.exec(ctx, "DELETE FROM invites WHERE id = ?", iv.ID)
		return err
	})
	return out, err
}
