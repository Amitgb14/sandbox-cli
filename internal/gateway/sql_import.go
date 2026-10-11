package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Moving a gateway from its state file to a database, once
// (sandbox-gateway store import), and back out of one (store export).
//
// Everything is copied as it was: keys keep their ids and hashes, so every
// secret handed out goes on working; organisations, members, policies,
// invites, ownership, sealed secrets, jobs and services keep theirs. The
// import is one transaction into a database that holds no gateway state
// yet: a second import, or one into a live database, is refused rather than
// merged.

// ErrNotEmpty refuses an import into a database that already holds state.
var ErrNotEmpty = errors.New("the database already holds a gateway's state; import only into an empty one")

// ImportState copies the state file at path into st. The file is opened as
// the gateway did, so a file others can read, or one held by a running
// gateway, is refused the same way.
func ImportState(ctx context.Context, path string, st Store) (ImportCounts, error) {
	if _, err := os.Stat(path); err != nil {
		return ImportCounts{}, err
	}
	fs, err := OpenFileStore(path)
	if err != nil {
		return ImportCounts{}, err
	}
	defer fs.Close()
	s, ok := st.(*sqlStore)
	if !ok {
		return ImportCounts{}, errors.New("the store cannot be imported into")
	}
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return s.importState(ctx, &fs.st)
}

// ImportCounts says what an import copied.
type ImportCounts struct {
	Keys, SSHKeys, Sandboxes, Volumes, Snapshots, Nodes, Orgs, Members, Policies, Secrets, Jobs, Services, Invites int
}

func (c ImportCounts) String() string {
	return fmt.Sprintf("%d keys, %d SSH keys, %d organisations (%d members), %d policies, %d invites, %d sandboxes, %d volumes, %d snapshots, %d nodes, %d secrets, %d jobs, %d services",
		c.Keys, c.SSHKeys, c.Orgs, c.Members, c.Policies, c.Invites, c.Sandboxes, c.Volumes, c.Snapshots, c.Nodes, c.Secrets, c.Jobs, c.Services)
}

func (s *sqlStore) importState(ctx context.Context, st *fileState) (ImportCounts, error) {
	var c ImportCounts
	err := s.tx(ctx, func(q *sqlTx) error {
		// Not orgs: every database has the default organisation.
		for _, t := range []string{"api_keys", "users", "sandboxes", "jobs", "services", "policies", "nodes", "secrets", "invites"} {
			var n int
			if err := q.row(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n); err != nil {
				return storeErr(err)
			}
			if n > 0 {
				return ErrNotEmpty
			}
		}
		// Organisations first, so a key in one's tenant is a user of it.
		orgIDs := map[string]string{}
		for _, o := range st.Orgs {
			id := newID("org_")
			if _, err := q.exec(ctx, "INSERT INTO orgs (id, key, display_name, kind, created) VALUES (?, ?, ?, ?, ?)",
				id, o.tenant(), o.Name, orgTeam, s.t(o.Created)); err != nil {
				return fmt.Errorf("organisation %s: %w", o.Name, err)
			}
			orgIDs[o.tenant()] = id
			c.Orgs++
		}
		// Parents before their sign-ins: a sign-in's key names its account.
		// A revoked account before a live one, which takes its address.
		keys := append([]Key(nil), st.Keys...)
		sort.SliceStable(keys, func(i, j int) bool { return keys[i].Revoked && !keys[j].Revoked })
		for pass := 0; pass < 2; pass++ {
			for _, k := range keys {
				if (k.Parent == "") != (pass == 0) {
					continue
				}
				k.Email = strings.ToLower(k.Email)
				if err := q.insertKey(ctx, k); err != nil {
					return fmt.Errorf("key %s: %w", k.ID, err)
				}
				c.Keys++
			}
		}
		for _, o := range st.Orgs {
			id := orgIDs[o.tenant()]
			if o.CreatedBy != "" {
				uid, _, err := q.userID(ctx, o.CreatedBy, o.CreatedByTenant, true)
				if err != nil {
					return err
				}
				if _, err := q.exec(ctx, "UPDATE orgs SET created_by = ? WHERE id = ?", uid, id); err != nil {
					return err
				}
			}
			for _, m := range o.Members {
				uid, _, err := q.userID(ctx, m.User, m.Tenant, true)
				if err != nil {
					return err
				}
				if _, err := q.exec(ctx, "INSERT INTO org_members (org_id, user_id, role, added) VALUES (?, ?, ?, ?)",
					id, uid, m.Role, s.t(m.Added)); err != nil {
					return fmt.Errorf("organisation %s, member %s: %w", o.Name, m.User, err)
				}
				c.Members++
			}
		}
		for _, k := range st.SSHKeys {
			uid, _, err := q.userID(ctx, k.User, k.Tenant, true)
			if err != nil {
				return err
			}
			if _, err := q.exec(ctx, "INSERT INTO ssh_keys (id, user_id, fingerprint, authorized_key, sandbox, created) VALUES (?, ?, ?, ?, ?, ?)",
				k.ID, uid, k.Fingerprint, k.AuthorizedKey, k.Sandbox, s.t(k.Created)); err != nil {
				return fmt.Errorf("SSH key %s: %w", k.ID, err)
			}
			c.SSHKeys++
		}
		for _, t := range st.Tokens {
			org, uid, _, err := q.ownerIDs(ctx, Owner{User: t.User, Tenant: t.Tenant}, true)
			if err != nil {
				return err
			}
			if _, err := q.exec(ctx, "INSERT INTO ssh_tokens (hash, user_id, org_id, sandbox, expires) VALUES (?, ?, ?, ?, ?)",
				t.Hash, uid, org, t.Sandbox, s.t(t.Expires)); err != nil {
				return err
			}
		}
		for id, r := range st.Sandboxes {
			org, uid, _, err := q.ownerIDs(ctx, r.Owner, true)
			if err != nil {
				return err
			}
			if _, err := q.exec(ctx, `INSERT INTO sandboxes (id, org_id, user_id, node, cpus, memory_mb, disk_mb, recorded)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, org, uid, r.Node, r.CPUs, r.MemoryMB, r.DiskMB, s.t(r.Recorded)); err != nil {
				return fmt.Errorf("sandbox %s: %w", id, err)
			}
			c.Sandboxes++
		}
		for name, o := range st.Volumes {
			org, uid, _, err := q.ownerIDs(ctx, o, true)
			if err != nil {
				return err
			}
			if _, err := q.exec(ctx, "INSERT INTO volumes (name, org_id, user_id, node) VALUES (?, ?, ?, ?)", name, org, uid, o.Node); err != nil {
				return err
			}
			c.Volumes++
		}
		for id, o := range st.Snapshots {
			org, uid, _, err := q.ownerIDs(ctx, o, true)
			if err != nil {
				return err
			}
			if _, err := q.exec(ctx, "INSERT INTO snapshots (id, org_id, user_id, node) VALUES (?, ?, ?, ?)", id, org, uid, o.Node); err != nil {
				return err
			}
			c.Snapshots++
		}
		for _, n := range st.Nodes {
			if _, err := q.exec(ctx, "INSERT INTO nodes (name, endpoint, token_file, ca_file, cert_file, key_file) VALUES (?, ?, ?, ?, ?, ?)",
				n.Name, n.Endpoint, n.TokenFile, n.CAFile, n.CertFile, n.KeyFile); err != nil {
				return err
			}
			c.Nodes++
		}
		for _, sec := range st.Secrets {
			// Sealed with the secrets key as they were: the database holds
			// the same ciphertext the file did.
			org, _, err := q.orgID(ctx, sec.Tenant, true)
			if err != nil {
				return err
			}
			if _, err := q.exec(ctx, "INSERT INTO secrets (org_id, name, sealed, updated) VALUES (?, ?, ?, ?)",
				org, sec.Name, sec.Sealed, s.t(sec.Updated)); err != nil {
				return err
			}
			c.Secrets++
		}
		for _, j := range st.Jobs {
			if err := q.writeJob(ctx, j, true); err != nil {
				return fmt.Errorf("job %s: %w", j.ID, err)
			}
			c.Jobs++
		}
		if st.Services != nil {
			for _, r := range st.Services.Records {
				b, err := jsonText(r)
				if err != nil {
					return err
				}
				org, uid, _, err := q.ownerIDs(ctx, Owner{User: r.User, Tenant: r.Tenant}, true)
				if err != nil {
					return err
				}
				if _, err := q.exec(ctx, "INSERT INTO services (org_id, name, user_id, record) VALUES (?, ?, ?, ?)", org, r.Name, uid, b); err != nil {
					return err
				}
				c.Services++
			}
			if err := q.retire(ctx, st.Services.Retiring); err != nil {
				return err
			}
		}
		for _, p := range st.Policies {
			if _, err := q.exec(ctx, "INSERT INTO policies (name, max_lifetime_secs) VALUES (?, ?)", p.Name, p.MaxLifetimeSecs); err != nil {
				return err
			}
			c.Policies++
		}
		for _, u := range st.UserPolicies {
			if err := q.assignPolicy(ctx, u.User, u.Tenant, u.Policy); err != nil {
				return err
			}
		}
		for _, iv := range st.Invites {
			if err := q.insertInvite(ctx, iv); err != nil {
				return err
			}
			c.Invites++
		}
		return nil
	})
	return c, err
}
