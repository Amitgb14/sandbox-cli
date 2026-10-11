package gateway

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Sandboxes, volumes, snapshots, nodes and SSH in the database.
//
// A sandbox's row is who owns it, where it runs and what it was given —
// what isolation and quotas are decided by — and its details as its node
// last answered: name, image, state, labels, network, lifetime, end and
// idle timeout. The node stays the authority on those; the gateway keeps
// them in step (UpdateSandboxDetails, from every change it forwards and from
// reconcile) so they can be asked for when the node cannot be, and kept as
// history once it is gone. Processes, events, metrics and files stay on the
// node.
//
// A sandbox, a volume and a snapshot belong to an organisation and a user
// (org_id, user_id); reads join them back to the tenant and the user name
// an Owner holds (ownerJoin).

// sandboxSelect reads sandboxes as r. A where clause names its columns as
// r.<column>.
const sandboxSelect = `SELECT r.id, ro.key, ru.name, r.node, r.cpus, r.memory_mb, r.disk_mb, r.recorded, r.name, r.image, r.state,
	r.labels, r.network, r.lifetime_secs, r.expires_at, r.idle_timeout_secs, r.created_at, r.details_updated, r.terminated_at,
	r.terminated_reason FROM sandboxes r` + ownerJoin

func scanSandbox(sc interface{ Scan(...any) error }) (SandboxRow, error) {
	var r SandboxRow
	var recorded, expiresAt, createdAt, detailsUpdated, terminatedAt dbTime
	var d api.Sandbox
	err := sc.Scan(&r.ID, &r.Owner.Tenant, &r.Owner.User, &r.Owner.Node, &r.CPUs, &r.MemoryMB, &r.DiskMB, &recorded,
		&d.Name, &d.Image, &d.State, jsonCol{&d.Labels}, jsonCol{&d.Network}, &d.LifetimeSecs, &expiresAt,
		&d.IdleTimeoutSecs, &createdAt, &detailsUpdated, &terminatedAt, &r.TerminatedReason)
	if err != nil {
		return SandboxRow{}, err
	}
	r.Recorded, r.TerminatedAt = recorded.T, terminatedAt.ptr()
	if detailsUpdated.Valid {
		d.ID, d.CPUs, d.MemoryMB, d.DiskMB = r.ID, r.CPUs, r.MemoryMB, r.DiskMB
		d.ExpiresAt, d.CreatedAt = expiresAt.ptr(), createdAt.T
		r.Details = &d
	}
	return r, nil
}

// detailsArgs are the detail columns' values for sb, nil for none.
func (s *sqlStore) detailsArgs(sb *api.Sandbox) ([]any, error) {
	if sb == nil {
		return []any{"", "", "", "{}", "{}", 0, nil, 0, nil, nil}, nil
	}
	labels, err := jsonText(sb.Labels)
	if err != nil {
		return nil, err
	}
	if sb.Labels == nil {
		labels = "{}"
	}
	network, err := jsonText(sb.Network)
	if err != nil {
		return nil, err
	}
	var created any
	if !sb.CreatedAt.IsZero() {
		created = s.t(sb.CreatedAt)
	}
	return []any{sb.Name, sb.Image, sb.State, labels, network, sb.LifetimeSecs, s.tp(sb.ExpiresAt),
		sb.IdleTimeoutSecs, created, s.t(s.stamp())}, nil
}

func (s *sqlStore) RecordSandbox(ctx context.Context, id string, o Owner, size api.NodeResources, sb *api.Sandbox) error {
	if id == "" || o.User == "" || o.Node == "" {
		return errors.New("an owner needs a sandbox, a user and a node")
	}
	details, err := s.detailsArgs(sb)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *sqlTx) error {
		org, user, _, err := q.ownerIDs(ctx, o, true)
		if err != nil {
			return err
		}
		args := append([]any{id, org, user, o.Node, size.CPUs, size.MemoryMB, size.DiskMB, s.t(s.stamp())}, details...)
		_, err = q.exec(ctx, `INSERT INTO sandboxes (id, org_id, user_id, node, cpus, memory_mb, disk_mb, recorded,
			name, image, state, labels, network, lifetime_secs, expires_at, idle_timeout_secs, created_at, details_updated)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET org_id = excluded.org_id, user_id = excluded.user_id, node = excluded.node,
				cpus = excluded.cpus, memory_mb = excluded.memory_mb, disk_mb = excluded.disk_mb, recorded = excluded.recorded,
				name = excluded.name, image = excluded.image, state = excluded.state, labels = excluded.labels,
				network = excluded.network, lifetime_secs = excluded.lifetime_secs, expires_at = excluded.expires_at,
				idle_timeout_secs = excluded.idle_timeout_secs, created_at = excluded.created_at,
				details_updated = excluded.details_updated, terminated_at = NULL, terminated_reason = ''`, args...)
		return err
	})
}

// UpdateSandboxDetails writes many in one transaction: reconcile brings a
// whole node's up to date each minute.
func (s *sqlStore) UpdateSandboxDetails(ctx context.Context, sbs ...api.Sandbox) error {
	if len(sbs) == 0 {
		return nil
	}
	return s.tx(ctx, func(q *sqlTx) error {
		for i := range sbs {
			sb := &sbs[i]
			details, err := s.detailsArgs(sb)
			if err != nil {
				return err
			}
			if _, err := q.exec(ctx, `UPDATE sandboxes SET name = ?, image = ?, state = ?, labels = ?, network = ?, lifetime_secs = ?,
				expires_at = ?, idle_timeout_secs = ?, created_at = ?, details_updated = ? WHERE id = ? AND terminated_at IS NULL`,
				append(details, sb.ID)...); err != nil {
				return err
			}
			if sb.State == api.StateTerminated {
				if err := q.terminate(ctx, []string{sb.ID}, "terminated"); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// terminate marks sandboxes gone, keeping their rows as history.
func (q *sqlTx) terminate(ctx context.Context, ids []string, reason string) error {
	now := q.s.t(q.s.stamp())
	for _, id := range ids {
		if _, err := q.exec(ctx, "UPDATE sandboxes SET terminated_at = ?, terminated_reason = ?, state = ? WHERE id = ? AND terminated_at IS NULL",
			now, reason, api.StateTerminated, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqlStore) sandbox(ctx context.Context, id string) (SandboxRow, bool, error) {
	var r SandboxRow
	var ok bool
	err := s.read(ctx, func(q *sqlTx) error {
		var err error
		r, err = scanSandbox(q.row(ctx, sandboxSelect+"WHERE r.id = ? AND r.terminated_at IS NULL", id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return storeErr(err)
		}
		ok = true
		return nil
	})
	return r, ok, err
}

func (s *sqlStore) OwnerOf(ctx context.Context, sandbox string) (Owner, bool, error) {
	r, ok, err := s.sandbox(ctx, sandbox)
	return r.Owner, ok, err
}

func (s *sqlStore) SizeOf(ctx context.Context, sandbox string) (api.NodeResources, bool, error) {
	r, ok, err := s.sandbox(ctx, sandbox)
	return api.NodeResources{CPUs: r.CPUs, MemoryMB: r.MemoryMB, DiskMB: r.DiskMB}, ok, err
}

func (s *sqlStore) ForgetSandbox(ctx context.Context, sandbox string) error {
	return s.ForgetSandboxes(ctx, []string{sandbox}, "terminated")
}

func (s *sqlStore) ForgetSandboxes(ctx context.Context, ids []string, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.tx(ctx, func(q *sqlTx) error { return q.terminate(ctx, ids, reason) })
}

func (s *sqlStore) UsageOf(ctx context.Context, tenant string) (Usage, error) {
	var u Usage
	err := s.read(ctx, func(q *sqlTx) error {
		return storeErr(q.row(ctx, `SELECT COUNT(*), COALESCE(SUM(r.cpus), 0), COALESCE(SUM(r.memory_mb), 0)
			FROM sandboxes r JOIN orgs ro ON ro.id = r.org_id WHERE ro.key = ? AND r.terminated_at IS NULL`,
			orgKeyOf(tenant)).Scan(&u.Sandboxes, &u.CPUs, &u.MemoryMB))
	})
	return u, err
}

func (s *sqlStore) sandboxRows(ctx context.Context, where string, args ...any) ([]SandboxRow, error) {
	var out []SandboxRow
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, sandboxSelect+where, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanSandbox(rows)
			if err != nil {
				return storeErr(err)
			}
			out = append(out, r)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

func (s *sqlStore) LiveSandboxes(ctx context.Context) ([]SandboxRow, error) {
	return s.sandboxRows(ctx, "WHERE r.terminated_at IS NULL ORDER BY r.id")
}

func (s *sqlStore) SandboxesOn(ctx context.Context, node string) (map[string]time.Time, error) {
	rows, err := s.sandboxRows(ctx, "WHERE r.node = ? AND r.terminated_at IS NULL", node)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Recorded
	}
	return out, nil
}

func (s *sqlStore) SandboxHistory(ctx context.Context, f SandboxFilter) ([]SandboxRow, error) {
	var where []string
	var args []any
	if !f.AllTenant {
		where, args = append(where, "ro.key = ?"), append(args, orgKeyOf(f.Tenant))
	}
	if f.User != "" {
		where, args = append(where, "ru.name = ?"), append(args, f.User)
	}
	if f.Live {
		where = append(where, "r.terminated_at IS NULL")
	}
	q := ""
	if len(where) > 0 {
		q = "WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY r.recorded DESC, r.id"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	return s.sandboxRows(ctx, q, args...)
}

func (s *sqlStore) PruneSandboxHistory(ctx context.Context, before time.Time) (int, error) {
	var n int64
	err := s.tx(ctx, func(q *sqlTx) error {
		r, err := q.exec(ctx, "DELETE FROM sandboxes WHERE terminated_at IS NOT NULL AND terminated_at < ?", s.t(before))
		if err != nil {
			return err
		}
		n, _ = r.RowsAffected()
		return nil
	})
	return int(n), err
}

// --- volumes and snapshots -------------------------------------------------------

func (s *sqlStore) SetVolume(ctx context.Context, name string, o Owner) error {
	return s.tx(ctx, func(q *sqlTx) error {
		org, user, _, err := q.ownerIDs(ctx, o, true)
		if err != nil {
			return err
		}
		_, err = q.exec(ctx, `INSERT INTO volumes (name, org_id, user_id, node) VALUES (?, ?, ?, ?)
			ON CONFLICT (name) DO UPDATE SET org_id = excluded.org_id, user_id = excluded.user_id, node = excluded.node`,
			name, org, user, o.Node)
		return err
	})
}

func (s *sqlStore) ownerRow(ctx context.Context, query string, arg string) (Owner, bool, error) {
	var o Owner
	err := s.read(ctx, func(q *sqlTx) error {
		return storeErr(q.row(ctx, query, arg).Scan(&o.Tenant, &o.User, &o.Node))
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Owner{}, false, nil
	}
	return o, err == nil, err
}

func (s *sqlStore) VolumeOwner(ctx context.Context, name string) (Owner, bool, error) {
	return s.ownerRow(ctx, "SELECT ro.key, ru.name, r.node FROM volumes r"+ownerJoin+"WHERE r.name = ?", name)
}

func (s *sqlStore) ForgetVolume(ctx context.Context, name string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		_, err := q.exec(ctx, "DELETE FROM volumes WHERE name = ?", name)
		return err
	})
}

func (s *sqlStore) VolumesOf(ctx context.Context, user, tenant string) (map[string]string, error) {
	out := map[string]string{}
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT r.name, r.node FROM volumes r"+ownerJoin+"WHERE ru.name = ? AND ro.key = ?", user, orgKeyOf(tenant))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name, node string
			if err := rows.Scan(&name, &node); err != nil {
				return storeErr(err)
			}
			out[name] = node
		}
		return storeErr(rows.Err())
	})
	return out, err
}

func (s *sqlStore) SetSnapshot(ctx context.Context, id string, o Owner) error {
	return s.tx(ctx, func(q *sqlTx) error {
		org, user, _, err := q.ownerIDs(ctx, o, true)
		if err != nil {
			return err
		}
		_, err = q.exec(ctx, `INSERT INTO snapshots (id, org_id, user_id, node) VALUES (?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET org_id = excluded.org_id, user_id = excluded.user_id, node = excluded.node`,
			id, org, user, o.Node)
		return err
	})
}

func (s *sqlStore) SnapshotOwner(ctx context.Context, id string) (Owner, bool, error) {
	return s.ownerRow(ctx, "SELECT ro.key, ru.name, r.node FROM snapshots r"+ownerJoin+"WHERE r.id = ?", id)
}

func (s *sqlStore) ForgetSnapshotsGoneFrom(ctx context.Context, node string, holds map[string]bool) error {
	return s.tx(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT id FROM snapshots WHERE node = ?", node)
		if err != nil {
			return err
		}
		var gone []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return storeErr(err)
			}
			if !holds[id] {
				gone = append(gone, id)
			}
		}
		rows.Close()
		for _, id := range gone {
			if _, err := q.exec(ctx, "DELETE FROM snapshots WHERE id = ?", id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *sqlStore) ForgetSnapshot(ctx context.Context, id string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		_, err := q.exec(ctx, "DELETE FROM snapshots WHERE id = ?", id)
		return err
	})
}

// --- nodes -----------------------------------------------------------------------

func (s *sqlStore) PutNode(ctx context.Context, n NodeConfig) error {
	if !api.ValidNodeID(n.Name) {
		return fmt.Errorf("node name %q: lowercase letters, digits and dashes, at most 31", n.Name)
	}
	return s.tx(ctx, func(q *sqlTx) error {
		_, err := q.exec(ctx, `INSERT INTO nodes (name, endpoint, token_file, ca_file, cert_file, key_file) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (name) DO UPDATE SET endpoint = excluded.endpoint, token_file = excluded.token_file,
				ca_file = excluded.ca_file, cert_file = excluded.cert_file, key_file = excluded.key_file`,
			n.Name, n.Endpoint, n.TokenFile, n.CAFile, n.CertFile, n.KeyFile)
		return err
	})
}

func (s *sqlStore) RemoveNode(ctx context.Context, name string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		r, err := q.exec(ctx, "DELETE FROM nodes WHERE name = ?", name)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNoSuchNode
		}
		return nil
	})
}

func (s *sqlStore) Nodes(ctx context.Context) ([]NodeConfig, error) {
	var out []NodeConfig
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT name, endpoint, token_file, ca_file, cert_file, key_file FROM nodes ORDER BY name")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n NodeConfig
			if err := rows.Scan(&n.Name, &n.Endpoint, &n.TokenFile, &n.CAFile, &n.CertFile, &n.KeyFile); err != nil {
				return storeErr(err)
			}
			out = append(out, n)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

// --- SSH -------------------------------------------------------------------------

// sshKeySelect reads SSH keys as k, with their users (u) and the users'
// home organisations (h).
const sshKeySelect = `SELECT k.id, u.name, h.key, k.fingerprint, k.authorized_key, k.sandbox, k.created
	FROM ssh_keys k JOIN users u ON u.id = k.user_id JOIN orgs h ON h.id = u.org_id `

func scanSSHKey(sc interface{ Scan(...any) error }) (SSHKey, error) {
	var k SSHKey
	var created dbTime
	err := sc.Scan(&k.ID, &k.User, &k.Tenant, &k.Fingerprint, &k.AuthorizedKey, &k.Sandbox, &created)
	k.Created = created.T
	return k, err
}

func (s *sqlStore) sshKeys(ctx context.Context, where string, args ...any) ([]SSHKey, error) {
	var out []SSHKey
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, sshKeySelect+where, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			k, err := scanSSHKey(rows)
			if err != nil {
				return storeErr(err)
			}
			out = append(out, k)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

func (s *sqlStore) AddSSHKey(ctx context.Context, user, tenant, sandbox, authorizedKey string) (SSHKey, error) {
	pk, err := ParseAuthorizedKey(authorizedKey)
	if err != nil {
		return SSHKey{}, err
	}
	k := SSHKey{ID: newID("ssh_"), User: user, Tenant: tenant, Fingerprint: pk.Fingerprint,
		AuthorizedKey: pk.Line(), Sandbox: sandbox, Created: s.stamp()}
	err = s.tx(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, `SELECT u.name, h.key, k.sandbox FROM ssh_keys k JOIN users u ON u.id = k.user_id
			JOIN orgs h ON h.id = u.org_id WHERE k.fingerprint = ?`, k.Fingerprint)
		if err != nil {
			return err
		}
		for rows.Next() {
			var u, t, sb string
			if err := rows.Scan(&u, &t, &sb); err != nil {
				rows.Close()
				return storeErr(err)
			}
			if u != user || t != orgKeyOf(tenant) {
				rows.Close()
				return ErrKeyTaken
			}
			if sb == sandbox {
				rows.Close()
				return ErrKeyRegistered
			}
		}
		rows.Close()
		uid, _, err := q.userID(ctx, user, tenant, true)
		if err != nil {
			return err
		}
		_, err = q.exec(ctx, "INSERT INTO ssh_keys (id, user_id, fingerprint, authorized_key, sandbox, created) VALUES (?, ?, ?, ?, ?, ?)",
			k.ID, uid, k.Fingerprint, k.AuthorizedKey, k.Sandbox, s.t(k.Created))
		return err
	})
	if err != nil {
		return SSHKey{}, err
	}
	return k, nil
}

func (s *sqlStore) RemoveSSHKey(ctx context.Context, id string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		r, err := q.exec(ctx, "DELETE FROM ssh_keys WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNoSuchKey
		}
		return nil
	})
}

func (s *sqlStore) SSHKeysByFingerprint(ctx context.Context, fingerprint string) ([]SSHKey, error) {
	keys, err := s.sshKeys(ctx, "WHERE k.fingerprint = ? ORDER BY k.created, k.id", fingerprint)
	// The index found them; compare again in constant time, as the file
	// store did, so a near match costs as long as a far one.
	out := keys[:0]
	for _, k := range keys {
		if subtle.ConstantTimeCompare([]byte(k.Fingerprint), []byte(fingerprint)) == 1 {
			out = append(out, k)
		}
	}
	return out, err
}

func (s *sqlStore) SSHKeysFor(ctx context.Context, user string) ([]SSHKey, error) {
	return s.sshKeys(ctx, "WHERE u.name = ? ORDER BY k.created, k.id", user)
}

func (s *sqlStore) NewSSHToken(ctx context.Context, user, tenant, sandbox string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		return "", time.Time{}, errors.New("a token needs a positive lifetime")
	}
	if sandbox == "" {
		return "", time.Time{}, errors.New("a token names one sandbox")
	}
	tok := newSecret(tokenSecretPrefix)
	now := s.stamp()
	exp := now.Add(ttl)
	err := s.tx(ctx, func(q *sqlTx) error {
		if _, err := q.exec(ctx, "DELETE FROM ssh_tokens WHERE expires <= ?", s.t(now)); err != nil {
			return err
		}
		org, uid, _, err := q.ownerIDs(ctx, Owner{User: user, Tenant: tenant}, true)
		if err != nil {
			return err
		}
		_, err = q.exec(ctx, "INSERT INTO ssh_tokens (hash, user_id, org_id, sandbox, expires) VALUES (?, ?, ?, ?, ?)",
			hashSecret(tok), uid, org, sandbox, s.t(exp))
		return err
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

func (s *sqlStore) RedeemSSHToken(ctx context.Context, token string) (SSHToken, bool, error) {
	if !strings.HasPrefix(token, tokenSecretPrefix) {
		return SSHToken{}, false, nil
	}
	var t SSHToken
	var exp dbTime
	err := s.read(ctx, func(q *sqlTx) error {
		return storeErr(q.row(ctx, `SELECT u.name, o.key, t.sandbox, t.expires FROM ssh_tokens t
			JOIN users u ON u.id = t.user_id JOIN orgs o ON o.id = t.org_id WHERE t.hash = ? AND t.expires > ?`,
			hashSecret(token), s.t(s.now())).Scan(&t.User, &t.Tenant, &t.Sandbox, &exp))
	})
	if errors.Is(err, sql.ErrNoRows) {
		return SSHToken{}, false, nil
	}
	if err != nil {
		return SSHToken{}, false, err
	}
	t.Expires = exp.T
	return t, true, nil
}
