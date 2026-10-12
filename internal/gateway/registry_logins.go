package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/image"
)

// Registry logins are an organization's user name and token for a
// registry, so its sandboxes, jobs and services start from its private
// images. The rules:
//
//   - A login is the organization's, as its secrets are: kept for one
//     tenant, set and removed with secrets:write, listed (without the
//     token) to anyone in it, and added only to that tenant's creates. No
//     login is kept anywhere else — not on a node, not for the gateway —
//     so no one starts from a private image without a login of their own
//     for it.
//   - The token is sealed as a secret's value is (secrets.go), bound to the
//     tenant, the registry and the user name, and is never returned,
//     logged or put in an error.
//   - It leaves the gateway only in a create of an image on its registry
//     (CreateSandboxRequest.RegistryLogin), to a node that can pull with it
//     (capability registry_login). The node uses it for that pull and keeps
//     what it fetched apart from pulls without it (internal/image,
//     logins.go), so the image is not every tenant's once one has pulled it.
//   - A client cannot send a login of its own through the gateway: the
//     field is the gateway's to set.

const maxRegistryLogins = 50 // per tenant

// ErrNoSuchRegistryLogin is DeleteRegistryLogin's answer for a registry
// the tenant holds no login for.
var ErrNoSuchRegistryLogin = errors.New("no such registry login")

var registryRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

// canonRegistry is the name a login is kept under. Docker Hub has three
// names an image can reach it by; a login for one is for all of them.
func canonRegistry(host string) string {
	host = strings.ToLower(host)
	switch host {
	case "docker.io", "index.docker.io", "registry-1.docker.io":
		return "docker.io"
	}
	return host
}

func checkRegistry(host string) error {
	if len(host) > 255 || !registryRE.MatchString(host) {
		return fmt.Errorf("registry %q: a registry's host[:port], e.g. ghcr.io", host)
	}
	return nil
}

// checkRegistryLoginRequest is what is wrong with req, never quoting the
// password. The node holds a login to the same rules (server.go).
func checkRegistryLoginRequest(req api.RegistryLoginRequest) error {
	switch {
	case req.Username == "" || len(req.Username) > 256 || strings.ContainsAny(req.Username, ":\r\n\x00"):
		return errors.New("username: 1 to 256 bytes, without a colon or a line break")
	case req.Password == "" || len(req.Password) > 8<<10 || strings.ContainsAny(req.Password, "\r\n\x00"):
		return errors.New("password: 1 to 8192 bytes, without a line break")
	}
	return nil
}

// registryLoginAD binds a sealed token to its tenant, registry and user
// name, so one moved to another row does not open there.
func registryLoginAD(tenant, registry, username string) string {
	return "registry-login\x00" + tenant + "\x00" + registry + "\x00" + username
}

// --- the store ---------------------------------------------------------------------

func (s *sqlStore) PutRegistryLogin(ctx context.Context, tenant, registry, username string, sealed []byte) error {
	now := s.stamp()
	return s.tx(ctx, func(q *sqlTx) error {
		org, _, err := q.orgID(ctx, tenant, true)
		if err != nil {
			return err
		}
		var exists, n int
		if err := q.row(ctx, "SELECT COUNT(*) FROM registry_logins WHERE org_id = ? AND registry = ?", org, registry).Scan(&exists); err != nil {
			return storeErr(err)
		}
		if exists == 0 {
			if err := q.row(ctx, "SELECT COUNT(*) FROM registry_logins WHERE org_id = ?", org).Scan(&n); err != nil {
				return storeErr(err)
			}
			if n >= maxRegistryLogins {
				return fmt.Errorf("an organization holds at most %d registry logins", maxRegistryLogins)
			}
		}
		_, err = q.exec(ctx, `INSERT INTO registry_logins (org_id, registry, username, sealed, updated) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (org_id, registry) DO UPDATE SET username = excluded.username, sealed = excluded.sealed, updated = excluded.updated`,
			org, registry, username, slices.Clone(sealed), s.t(now))
		return err
	})
}

func (s *sqlStore) RegistryLogin(ctx context.Context, tenant, registry string) (string, []byte, bool, error) {
	var user string
	var b []byte
	err := s.read(ctx, func(q *sqlTx) error {
		return storeErr(q.row(ctx, "SELECT x.username, x.sealed FROM registry_logins x JOIN orgs o ON o.id = x.org_id WHERE o.key = ? AND x.registry = ?",
			orgKeyOf(tenant), registry).Scan(&user, &b))
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, nil
	}
	return user, b, err == nil, err
}

func (s *sqlStore) RegistryLoginsOf(ctx context.Context, tenant string) ([]api.RegistryLoginInfo, error) {
	out := []api.RegistryLoginInfo{}
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT x.registry, x.username, x.updated FROM registry_logins x JOIN orgs o ON o.id = x.org_id WHERE o.key = ? ORDER BY x.registry",
			orgKeyOf(tenant))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var li api.RegistryLoginInfo
			var updated dbTime
			if err := rows.Scan(&li.Registry, &li.Username, &updated); err != nil {
				return storeErr(err)
			}
			li.Updated = updated.T
			out = append(out, li)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

func (s *sqlStore) DeleteRegistryLogin(ctx context.Context, tenant, registry string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		r, err := q.exec(ctx, "DELETE FROM registry_logins WHERE org_id = (SELECT id FROM orgs WHERE key = ?) AND registry = ?", orgKeyOf(tenant), registry)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNoSuchRegistryLogin
		}
		return nil
	})
}

// --- the gateway ------------------------------------------------------------------

// addRegistryLogin sets req.RegistryLogin to the tenant's login for the
// registry of req's image, if it holds one, and asks for a node that can
// pull with it. A login the client sent is refused.
func (g *Gateway) addRegistryLogin(ctx context.Context, p Principal, req *api.CreateSandboxRequest, want *Want) *createFail {
	if req.RegistryLogin != nil {
		return failWith(http.StatusBadRequest, api.CodeInvalidRequest, "registry_login is the gateway's to set, from your organization's registry logins (PUT /v1/registry-logins/{registry})")
	}
	if req.Image == "" || g.sealer == nil {
		return nil
	}
	ref, err := image.ParseRef(req.Image)
	if err != nil {
		return nil // the node refuses the image in its own words
	}
	registry := canonRegistry(ref.Registry)
	user, sealed, ok, err := g.storeFor(p).RegistryLogin(ctx, p.Tenant, registry)
	if err != nil {
		g.logf("store: %v", err)
		return transientFail(http.StatusServiceUnavailable, api.CodeUnavailable, ErrStore.Error())
	}
	if !ok {
		return nil
	}
	pw, err := g.sealer.open(sealed, registryLoginAD(p.Tenant, registry, user))
	if err != nil {
		return failWith(http.StatusInternalServerError, api.CodeInternal, "the registry login for "+registry+" does not open with this gateway's secrets key; set it again")
	}
	req.RegistryLogin = &api.RegistryLogin{Registry: ref.Registry, Username: user, Password: string(pw)}
	want.Caps = append(want.Caps, api.CapRegistryLogin)
	return nil
}

func (g *Gateway) putRegistryLogin(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeSecretsWrite) {
		return
	}
	if g.sealer == nil {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this gateway keeps no registry logins: it was started without --secrets-key-file")
		return
	}
	registry := canonRegistry(r.PathValue("registry"))
	if err := checkRegistry(registry); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	var req api.RegistryLoginRequest
	if !decode(w, r, &req) {
		return
	}
	if err := checkRegistryLoginRequest(req); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	sealed := g.sealer.seal([]byte(req.Password), registryLoginAD(p.Tenant, registry, req.Username))
	if err := g.storeFor(p).PutRegistryLogin(r.Context(), p.Tenant, registry, req.Username, sealed); err != nil {
		if errors.Is(err, ErrStore) {
			g.writeStoreErr(w, err)
			return
		}
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
		return
	}
	g.logf("registry login for %s (user %s) set for tenant %q by key %s", registry, req.Username, p.Tenant, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) listRegistryLogins(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	list, err := g.storeFor(p).RegistryLoginsOf(r.Context(), p.Tenant)
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.RegistryLoginList{Logins: list})
}

func (g *Gateway) deleteRegistryLogin(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeSecretsWrite) {
		return
	}
	registry := canonRegistry(r.PathValue("registry"))
	if err := g.storeFor(p).DeleteRegistryLogin(r.Context(), p.Tenant, registry); err != nil {
		if errors.Is(err, ErrNoSuchRegistryLogin) {
			writeErr(w, http.StatusNotFound, api.CodeNotFound, "no registry login for "+registry)
			return
		}
		g.writeStoreErr(w, err)
		return
	}
	g.logf("registry login for %s removed for tenant %q by key %s", registry, p.Tenant, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}
