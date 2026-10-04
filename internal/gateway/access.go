package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The gateway's own endpoints: who the caller is, SSH, and the operator's
// keys and nodes.

func (g *Gateway) whoami(w http.ResponseWriter, r *http.Request, p Principal) {
	writeJSON(w, http.StatusOK, api.Whoami{User: p.User, Tenant: p.Tenant, KeyID: p.KeyID, Scopes: p.Scopes})
}

// --- SSH ----------------------------------------------------------------------

func (g *Gateway) sshEndpoint(w http.ResponseWriter, r *http.Request, p Principal) {
	info, ok := g.sshInfo()
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeUnsupported, "this gateway does not serve SSH")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func sshKeyInfo(k SSHKey) api.SSHKeyInfo {
	return api.SSHKeyInfo{ID: k.ID, Fingerprint: k.Fingerprint, Key: k.AuthorizedKey, Sandbox: k.Sandbox, Created: k.Created}
}

func (g *Gateway) addSSHKey(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeSSH) {
		return
	}
	var req api.SSHKeyRequest
	if !decode(w, r, &req) {
		return
	}
	sandbox := ""
	if req.Sandbox != "" {
		// Limited to a sandbox the caller may reach, by id, so a name later
		// reused does not inherit the key.
		id, _, _, err := g.resolve(r.Context(), p, req.Sandbox, ScopeSSH)
		if err != nil {
			writeRouteErr(w, err, ScopeSSH)
			return
		}
		sandbox = id
	}
	k, err := g.store.AddSSHKey(p.User, p.Tenant, sandbox, req.Key)
	switch {
	case errors.Is(err, ErrKeyTaken), errors.Is(err, ErrKeyRegistered):
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "key: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sshKeyInfo(k))
}

func (g *Gateway) listSSHKeys(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeSSH) {
		return
	}
	out := api.SSHKeyList{Keys: []api.SSHKeyInfo{}}
	for _, k := range g.store.SSHKeysFor(p.User) {
		if k.Tenant == p.Tenant {
			out.Keys = append(out.Keys, sshKeyInfo(k))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) removeSSHKey(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeSSH) {
		return
	}
	id := r.PathValue("id")
	for _, k := range g.store.SSHKeysFor(p.User) {
		if k.ID == id && k.Tenant == p.Tenant {
			if err := g.store.RemoveSSHKey(id); err != nil && !errors.Is(err, ErrNoSuchKey) {
				writeErr(w, http.StatusInternalServerError, api.CodeInternal, "removing the key failed")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such key")
}

// sshAccess issues a short-lived token for one sandbox. The token is the
// SSH username and the whole credential, so it is returned once, in this
// body, and stored only as its hash.
func (g *Gateway) sshAccess(w http.ResponseWriter, r *http.Request, p Principal) {
	var req api.SSHAccessRequest
	if !decode(w, r, &req) {
		return
	}
	id, _, _, err := g.resolve(r.Context(), p, r.PathValue("ref"), ScopeSSH)
	if err != nil {
		writeRouteErr(w, err, ScopeSSH)
		return
	}
	info, ok := g.sshInfo()
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeUnsupported, "this gateway does not serve SSH")
		return
	}
	ttl := g.cfg.SSHAccessTTL
	switch {
	case req.TTLSecs < 0:
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "ttl_secs must not be negative")
		return
	// Compared in seconds: a large ttl_secs times a second overflows.
	case req.TTLSecs > int(g.cfg.SSHAccessMaxTTL/time.Second):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, fmt.Sprintf("ttl_secs: at most %d", int(g.cfg.SSHAccessMaxTTL/time.Second)))
		return
	case req.TTLSecs > 0:
		ttl = time.Duration(req.TTLSecs) * time.Second
	}
	tok, exp, err := g.store.NewSSHToken(p.User, p.Tenant, id, ttl)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "issuing the token failed")
		return
	}
	writeJSON(w, http.StatusCreated, api.SSHAccess{
		User: tok, Host: info.Host, Port: info.Port, ExpiresAt: exp,
		Command: fmt.Sprintf("ssh -p %d %s@%s", info.Port, tok, info.Host),
	})
}

// --- admin: keys --------------------------------------------------------------

func keyInfo(k Key) api.KeyInfo {
	return api.KeyInfo{ID: k.ID, User: k.User, Tenant: k.Tenant, Scopes: k.Scopes, Created: k.Created, Revoked: k.Revoked}
}

func (g *Gateway) adminCreateKey(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	var req api.CreateKeyRequest
	if !decode(w, r, &req) {
		return
	}
	secret, k, err := g.store.CreateKey(req.User, req.Tenant, req.Scopes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	g.logf("key %s issued for %s by %s", k.ID, k.User, p.KeyID)
	writeJSON(w, http.StatusCreated, api.CreatedKey{KeyInfo: keyInfo(k), Secret: secret})
}

func (g *Gateway) adminListKeys(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	out := api.KeyList{Keys: []api.KeyInfo{}}
	for _, k := range g.store.Keys() {
		out.Keys = append(out.Keys, keyInfo(k))
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) adminRevokeKey(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	id := r.PathValue("id")
	if err := g.store.RevokeKey(id); err != nil {
		if errors.Is(err, ErrNoSuchKey) {
			writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such key")
			return
		}
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "revoking the key failed")
		return
	}
	g.logf("key %s revoked by %s", id, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}

// adminListSSHKeys lists one user's SSH keys (?user=, required), so an
// operator can see what a person can still log in with.
func (g *Gateway) adminListSSHKeys(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	user := r.URL.Query().Get("user")
	if user == "" {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "user is required (?user=)")
		return
	}
	out := api.SSHKeyList{Keys: []api.SSHKeyInfo{}}
	for _, k := range g.store.SSHKeysFor(user) {
		out.Keys = append(out.Keys, sshKeyInfo(k))
	}
	writeJSON(w, http.StatusOK, out)
}

// adminRemoveSSHKey removes any user's SSH key by id.
func (g *Gateway) adminRemoveSSHKey(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	id := r.PathValue("id")
	if err := g.store.RemoveSSHKey(id); err != nil {
		if errors.Is(err, ErrNoSuchKey) {
			writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such key")
			return
		}
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "removing the key failed")
		return
	}
	g.logf("ssh key %s removed by %s", id, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}

// --- admin: nodes -------------------------------------------------------------

func (g *Gateway) adminListNodes(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	out := api.NodeList{Nodes: []api.NodeInfo{}}
	for _, n := range g.nodes.list() {
		out.Nodes = append(out.Nodes, n.info())
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) adminAddNode(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	var spec api.NodeSpec
	if !decode(w, r, &spec) {
		return
	}
	cfg := NodeConfig{Name: spec.Name, Endpoint: spec.Endpoint, TokenFile: spec.TokenFile,
		CAFile: spec.CAFile, CertFile: spec.CertFile, KeyFile: spec.KeyFile}
	if err := CheckNodeConfig(cfg); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	if err := g.checkNodeFiles(cfg); err != nil {
		writeErr(w, http.StatusForbidden, api.CodeRefused, err.Error())
		return
	}
	if o := g.nodes.get(cfg.Name); o != nil && o.static {
		writeErr(w, http.StatusConflict, api.CodeConflict, "node "+cfg.Name+" is defined in the node config file")
		return
	}
	n, err := g.nodes.add(cfg, false)
	if err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	if err := g.store.PutNode(cfg); err != nil {
		g.nodes.remove(cfg.Name)
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "recording the node failed")
		return
	}
	g.logf("node %s (%s) added by %s", cfg.Name, cfg.Endpoint, p.KeyID)
	g.nodes.poll(r.Context(), n)
	writeJSON(w, http.StatusCreated, n.info())
}

// checkNodeFiles confines the files a node added through the API may name
// to NodeFilesDir. The gateway reads a token file and sends what it holds to
// the endpoint beside it; a path anywhere would let an admin key read any
// file the gateway can, by pointing a node at a server of its own.
func (g *Gateway) checkNodeFiles(cfg NodeConfig) error {
	for _, f := range []string{cfg.TokenFile, cfg.CAFile, cfg.CertFile, cfg.KeyFile} {
		if f == "" {
			continue
		}
		if g.cfg.NodeFilesDir == "" {
			return errors.New("this gateway takes no node files through the API; add the node with sandbox-gateway nodes add")
		}
		dir, err := filepath.EvalSymlinks(g.cfg.NodeFilesDir)
		if err != nil {
			return fmt.Errorf("the node files directory: %w", err)
		}
		if !filepath.IsAbs(f) {
			return fmt.Errorf("%s: give an absolute path under %s", f, g.cfg.NodeFilesDir)
		}
		real, err := filepath.EvalSymlinks(f)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		rel, err := filepath.Rel(dir, real)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("%s is not under %s, the only directory node files may come from", f, g.cfg.NodeFilesDir)
		}
	}
	return nil
}

func (g *Gateway) adminRemoveNode(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	name := r.PathValue("name")
	n := g.nodes.get(name)
	if n == nil {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such node")
		return
	}
	if n.static {
		writeErr(w, http.StatusConflict, api.CodeConflict, "node "+name+" is defined in the node config file")
		return
	}
	if err := g.store.RemoveNode(name); err != nil && !errors.Is(err, ErrNoSuchNode) {
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "removing the node failed")
		return
	}
	g.nodes.remove(name)
	g.logf("node %s removed by %s", name, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}

// adminCordon forwards a cordon to the node itself, which is where it is
// kept: a cordoned node says so in its status, so a second gateway — or
// this one after a restart — sees it too.
func (g *Gateway) adminCordon(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	var req api.CordonRequest
	if !decode(w, r, &req) {
		return
	}
	n := g.nodes.get(r.PathValue("name"))
	if n == nil {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such node")
		return
	}
	body, _ := json.Marshal(req)
	resp, err := n.do(r.Context(), http.MethodPost, "/v1/node/cordon", nil, bytes.NewReader(body), "application/json")
	if err != nil {
		writeUnreachable(w, n)
		return
	}
	if resp.StatusCode >= 300 {
		err := readAPIError(resp)
		resp.Body.Close()
		writeRouteErr(w, err, ScopeAdmin)
		return
	}
	resp.Body.Close()
	g.nodes.poll(r.Context(), n)
	g.logf("node %s cordoned=%v by %s", n.cfg.Name, req.Cordoned, p.KeyID)
	writeJSON(w, http.StatusOK, n.info())
}
