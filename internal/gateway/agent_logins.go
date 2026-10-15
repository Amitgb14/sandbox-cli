package gateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Agent logins are a user's own login to an agent — the OAuth files a
// subscription login writes in the sandbox user's home — kept by the gateway
// so that a sandbox started from Studio, or any client, does not ask for it
// again. It is what sandbox-cli does on one person's machine
// (agenthome.RestoreLogin, SaveLogin), moved to where many people's runs
// are. The rules:
//
//   - A login is one person's. It is kept per user (users.id, found from the
//     key that asks) and agent, never per organisation: a subscription is
//     not a team's, and an organisation's members do not share one. No
//     route returns its contents, and only the user's own keys reach it.
//   - It is sealed as a secret's value is (secrets.go), bound to the user
//     and the agent, so a row moved to another user does not open there.
//     Without --secrets-key-file there are no agent logins.
//   - It goes only into a sandbox the user may act on (resolve: their own),
//     and only when they ask (POST /v1/sandboxes/{ref}/agent-login). From
//     then on that sandbox is "kept from": the gateway reads the agent's
//     login files back every AgentLoginSaveInterval and once more as the
//     sandbox is deleted through it, and saves them when they changed — a
//     refreshed token, or the first login itself.
//   - What comes back is the guest's, and untrusted: only the agent's own
//     AuthPaths are read, each at most maxAgentLoginFile, filtered as the
//     CLI filters them (agents.FilterAuth: ~/.claude.json keeps its account
//     keys, never an MCP server's command), and restored only into the same
//     user's later sandboxes. A guest that rewrites them changes what its
//     own user's next sandbox starts with, and nothing of anyone else's.
//   - A sandbox's login is saved only when it changed since it was copied
//     in or last saved (agent_login_keeps.seen). Two sandboxes running one
//     agent at once each save only their own change, so one still holding
//     the login it started with does not put back a token the other has
//     since refreshed.

const (
	maxAgentLoginFile = 256 << 10
	// agentLoginTimeout bounds one sandbox's save or restore.
	agentLoginTimeout = 15 * time.Second
)

// ErrNoSuchAgentLogin is DeleteAgentLogin's answer when the user keeps no
// login for the agent and no sandbox is kept from.
var ErrNoSuchAgentLogin = errors.New("no such agent login")

// agentLoginFiles is a login: each of its files by its path relative to the
// sandbox user's home.
type agentLoginFiles map[string][]byte

// agentLoginAD binds a sealed login to its user and agent.
func agentLoginAD(userID, agent string) string {
	return "sandbox-gateway agent login v1\x00" + userID + "\x00" + agent
}

// digest says whether a login changed; "" for no files at all.
func (f agentLoginFiles) digest() string {
	if len(f) == 0 {
		return ""
	}
	names := make([]string, 0, len(f))
	for n := range f {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n + "\x00" + strconv.Itoa(len(f[n])) + "\x00"))
		h.Write(f[n])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// agentLoginKeep is a sandbox a user's login is kept from.
type agentLoginKeep struct {
	Sandbox, UserID, Agent string
	// Seen is the digest of the login last copied into or saved from it.
	Seen string
}

// agentLoginRow is a kept login as the store holds it.
type agentLoginRow struct {
	Agent   string
	Sealed  []byte
	Updated time.Time
}

// loginAgent is the agent a request names, if it keeps a login.
func loginAgent(name string) (agents.Descriptor, string) {
	d, ok := agents.LookupInteractive(name)
	if !ok {
		return d, "unknown agent " + strconv.Quote(name)
	}
	if len(d.AuthPaths) == 0 {
		return d, d.Name + " keeps no login files: it logs in with an API key each run"
	}
	return d, ""
}

// --- the store ---------------------------------------------------------------------

func (s *sqlStore) UserOfKey(ctx context.Context, keyID string) (string, bool, error) {
	var id string
	err := s.read(ctx, func(q *sqlTx) error {
		return storeErr(q.row(ctx, "SELECT user_id FROM api_keys WHERE id = ?", keyID).Scan(&id))
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

func (s *sqlStore) PutAgentLogin(ctx context.Context, userID, agent string, sealed []byte) error {
	now := s.stamp()
	return s.tx(ctx, func(q *sqlTx) error {
		_, err := q.exec(ctx, `INSERT INTO agent_logins (user_id, agent, sealed, updated) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id, agent) DO UPDATE SET sealed = excluded.sealed, updated = excluded.updated`,
			userID, agent, slices.Clone(sealed), s.t(now))
		return err
	})
}

func (s *sqlStore) AgentLogin(ctx context.Context, userID, agent string) ([]byte, bool, error) {
	var b []byte
	err := s.read(ctx, func(q *sqlTx) error {
		return storeErr(q.row(ctx, "SELECT sealed FROM agent_logins WHERE user_id = ? AND agent = ?", userID, agent).Scan(&b))
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return b, err == nil, err
}

func (s *sqlStore) AgentLoginsOf(ctx context.Context, userID string) ([]agentLoginRow, error) {
	var out []agentLoginRow
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT agent, sealed, updated FROM agent_logins WHERE user_id = ? ORDER BY agent", userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r agentLoginRow
			var updated dbTime
			if err := rows.Scan(&r.Agent, &r.Sealed, &updated); err != nil {
				return storeErr(err)
			}
			r.Updated = updated.T
			out = append(out, r)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

// DeleteAgentLogin forgets a user's login for agent and stops saving it
// from their sandboxes, which would otherwise put it back.
func (s *sqlStore) DeleteAgentLogin(ctx context.Context, userID, agent string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		r1, err := q.exec(ctx, "DELETE FROM agent_logins WHERE user_id = ? AND agent = ?", userID, agent)
		if err != nil {
			return err
		}
		r2, err := q.exec(ctx, "DELETE FROM agent_login_keeps WHERE user_id = ? AND agent = ?", userID, agent)
		if err != nil {
			return err
		}
		n1, _ := r1.RowsAffected()
		n2, _ := r2.RowsAffected()
		if n1+n2 == 0 {
			return ErrNoSuchAgentLogin
		}
		return nil
	})
}

func (s *sqlStore) KeepAgentLogin(ctx context.Context, k agentLoginKeep) error {
	now := s.stamp()
	return s.tx(ctx, func(q *sqlTx) error {
		_, err := q.exec(ctx, `INSERT INTO agent_login_keeps (sandbox_id, user_id, agent, seen, created) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (sandbox_id) DO UPDATE SET user_id = excluded.user_id, agent = excluded.agent, seen = excluded.seen`,
			k.Sandbox, k.UserID, k.Agent, k.Seen, s.t(now))
		return err
	})
}

func (s *sqlStore) AgentLoginKeeps(ctx context.Context) ([]agentLoginKeep, error) {
	var out []agentLoginKeep
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT sandbox_id, user_id, agent, seen FROM agent_login_keeps ORDER BY created, sandbox_id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k agentLoginKeep
			if err := rows.Scan(&k.Sandbox, &k.UserID, &k.Agent, &k.Seen); err != nil {
				return storeErr(err)
			}
			out = append(out, k)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

func (s *sqlStore) AgentLoginKeep(ctx context.Context, sandbox string) (agentLoginKeep, bool, error) {
	k := agentLoginKeep{Sandbox: sandbox}
	err := s.read(ctx, func(q *sqlTx) error {
		return storeErr(q.row(ctx, "SELECT user_id, agent, seen FROM agent_login_keeps WHERE sandbox_id = ?", sandbox).Scan(&k.UserID, &k.Agent, &k.Seen))
	})
	if errors.Is(err, sql.ErrNoRows) {
		return agentLoginKeep{}, false, nil
	}
	return k, err == nil, err
}

// SetAgentLoginSeen records what was last saved from a sandbox; a keep
// forgotten meanwhile stays forgotten.
func (s *sqlStore) SetAgentLoginSeen(ctx context.Context, sandbox, seen string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		_, err := q.exec(ctx, "UPDATE agent_login_keeps SET seen = ? WHERE sandbox_id = ?", seen, sandbox)
		return err
	})
}

func (s *sqlStore) DropAgentLoginKeep(ctx context.Context, sandbox string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		_, err := q.exec(ctx, "DELETE FROM agent_login_keeps WHERE sandbox_id = ?", sandbox)
		return err
	})
}

// --- the gateway ------------------------------------------------------------------

// loginUser is the user p's key belongs to, whose logins it reaches.
func (g *Gateway) loginUser(w http.ResponseWriter, r *http.Request, p Principal) (string, bool) {
	if g.sealer == nil {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this gateway keeps no agent logins: it was started without --secrets-key-file")
		return "", false
	}
	uid, ok, err := g.store.UserOfKey(r.Context(), p.KeyID)
	if err != nil {
		g.writeStoreErr(w, err)
		return "", false
	}
	if !ok {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "an agent login is kept for a user, and this credential is not a user's key")
		return "", false
	}
	return uid, true
}

// openAgentLogin is a user's kept login for agent: nil when there is none.
func (g *Gateway) openAgentLogin(ctx context.Context, userID, agent string) (agentLoginFiles, error) {
	sealed, ok, err := g.store.AgentLogin(ctx, userID, agent)
	if err != nil || !ok {
		return nil, err
	}
	plain, err := g.sealer.open(sealed, agentLoginAD(userID, agent))
	if err != nil {
		return nil, errors.New("the kept " + agent + " login does not open with this gateway's secrets key")
	}
	var f agentLoginFiles
	if err := json.Unmarshal(plain, &f); err != nil {
		return nil, errors.New("the kept " + agent + " login is unreadable")
	}
	return f, nil
}

// keepAgentLogin copies the caller's kept login into their sandbox, and
// keeps it from there from now on.
func (g *Gateway) keepAgentLogin(w http.ResponseWriter, r *http.Request, p Principal) {
	id, o, n, err := g.resolve(r.Context(), p, r.PathValue("ref"), ScopeCreate)
	if err != nil {
		writeRouteErr(w, err, ScopeCreate)
		return
	}
	var req api.KeepAgentLoginRequest
	if !decode(w, r, &req) {
		return
	}
	d, why := loginAgent(req.Agent)
	if why != "" {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, why)
		return
	}
	uid, ok := g.loginUser(w, r, p)
	if !ok {
		return
	}
	w, r, done := g.track(w, r, p, ScopeCreate, id, o.Node)
	defer done()
	ctx, cancel := context.WithTimeout(r.Context(), agentLoginTimeout)
	defer cancel()

	saved, err := g.openAgentLogin(ctx, uid, d.Name)
	if err != nil {
		if errors.Is(err, ErrStore) {
			g.writeStoreErr(w, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, err.Error()+"; forget it (DELETE /v1/agent-logins/"+d.Name+") and log in again")
		return
	}
	// Only the agent's own files, filtered again on the way in: a login
	// kept before a filter existed may carry what it now drops.
	in := agentLoginFiles{}
	for _, rel := range d.AuthPaths {
		data, ok := saved[rel]
		if !ok {
			continue
		}
		if data, ok = agents.FilterAuth(rel, data); !ok {
			continue
		}
		if err := n.client.WriteFile(ctx, id, agentHome+"/"+rel, data); err != nil {
			writeNodeErr(w, err)
			return
		}
		in[rel] = data
	}
	if err := g.store.KeepAgentLogin(ctx, agentLoginKeep{Sandbox: id, UserID: uid, Agent: d.Name, Seen: in.digest()}); err != nil {
		g.writeStoreErr(w, err)
		return
	}
	g.logf("agent login for %s kept from %s for key %s (restored: %t)", d.Name, id, p.KeyID, len(in) > 0)
	writeJSON(w, http.StatusOK, api.KeptAgentLogin{Restored: len(in) > 0})
}

// agentHome is the sandbox user's HOME in the base image
// (agenthome.GuestHome, which the gateway does not import).
const agentHome = "/sandbox/home"

// saveAgentLogin reads the login back from a sandbox it is kept from, and
// saves it if it changed. A sandbox that is gone is no longer kept from.
//
// Its reads are background (api.Background): the gateway's bookkeeping, not
// anyone using the sandbox. As activity they held every kept-from sandbox's
// idle timeout off, every two minutes, for good; and they must not resume
// one suspended for being idle, whose login was saved while it ran.
func (g *Gateway) saveAgentLogin(ctx context.Context, k agentLoginKeep) error {
	g.agentLoginMu.Lock()
	defer g.agentLoginMu.Unlock()
	ctx, cancel := context.WithTimeout(api.Background(ctx), agentLoginTimeout)
	defer cancel()
	// The keep may have been forgotten, or saved, since it was listed.
	k, ok, err := g.store.AgentLoginKeep(ctx, k.Sandbox)
	if err != nil || !ok {
		return err
	}
	o, ok, err := g.store.OwnerOf(ctx, k.Sandbox)
	if err != nil {
		return err
	}
	n := g.nodes.get(o.Node)
	if !ok || n == nil {
		return g.store.DropAgentLoginKeep(ctx, k.Sandbox)
	}
	if !n.isHealthy() {
		return nil // next time
	}
	sb, err := n.client.Sandbox(ctx, k.Sandbox)
	if isNotFound(err) || (err == nil && sb.State == api.StateTerminated) {
		return g.store.DropAgentLoginKeep(ctx, k.Sandbox)
	}
	if err != nil {
		return err
	}
	if sb.State != api.StateRunning {
		return nil // a suspended sandbox answers no file reads
	}
	d, why := loginAgent(k.Agent)
	if why != "" {
		return g.store.DropAgentLoginKeep(ctx, k.Sandbox)
	}
	out := agentLoginFiles{}
	for _, rel := range d.AuthPaths {
		data, err := n.client.ReadFileLimit(ctx, k.Sandbox, agentHome+"/"+rel, maxAgentLoginFile)
		if isNotFound(err) || errors.Is(err, api.ErrFileTooLarge) {
			continue
		}
		if err != nil {
			return err
		}
		if data, ok := agents.FilterAuth(rel, data); ok && len(data) > 0 {
			out[rel] = data
		}
	}
	// No files is "not logged in yet" (or logged out), never "forget": the
	// kept login stays until its user removes it.
	seen := out.digest()
	if seen == "" || seen == k.Seen {
		return nil
	}
	plain, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := g.store.PutAgentLogin(ctx, k.UserID, k.Agent, g.sealer.seal(plain, agentLoginAD(k.UserID, k.Agent))); err != nil {
		return err
	}
	if err := g.store.SetAgentLoginSeen(ctx, k.Sandbox, seen); err != nil {
		return err
	}
	g.logf("agent login for %s saved from %s", k.Agent, k.Sandbox)
	return nil
}

func isNotFound(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// saveAgentLogins saves every kept login that changed.
func (g *Gateway) saveAgentLogins(ctx context.Context) {
	if g.sealer == nil {
		return
	}
	keeps, err := g.store.AgentLoginKeeps(ctx)
	if err != nil {
		g.logf("agent logins: %v", err)
		return
	}
	for _, k := range keeps {
		if ctx.Err() != nil {
			return
		}
		if err := g.saveAgentLogin(ctx, k); err != nil {
			g.logf("agent login for %s from %s: %v", k.Agent, k.Sandbox, err)
		}
	}
}

// saveLoginBeforeDelete saves a sandbox's login, if it is kept from, before
// the delete goes on: the last chance to read it. A failure does not stop
// the delete; it is logged.
func (g *Gateway) saveLoginBeforeDelete(next handlerFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, p Principal) {
		if g.sealer != nil {
			if id, _, _, err := g.resolve(r.Context(), p, r.PathValue("ref"), ScopeDelete); err == nil {
				if k, ok, err := g.store.AgentLoginKeep(r.Context(), id); err == nil && ok {
					if err := g.saveAgentLogin(context.WithoutCancel(r.Context()), k); err != nil {
						g.logf("agent login for %s from %s, before its delete: %v", k.Agent, id, err)
					}
				}
			}
		}
		next(w, r, p)
	}
}

func (g *Gateway) listAgentLogins(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	uid, ok := g.loginUser(w, r, p)
	if !ok {
		return
	}
	rows, err := g.store.AgentLoginsOf(r.Context(), uid)
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	out := []api.AgentLoginInfo{}
	for _, row := range rows {
		li := api.AgentLoginInfo{Agent: row.Agent, Files: []string{}, Updated: row.Updated}
		// The names of its files, never their contents. One that does not
		// open is listed without them, so it can still be forgotten.
		if plain, err := g.sealer.open(row.Sealed, agentLoginAD(uid, row.Agent)); err == nil {
			var f agentLoginFiles
			if json.Unmarshal(plain, &f) == nil {
				for name := range f {
					li.Files = append(li.Files, name)
				}
				sort.Strings(li.Files)
			}
		}
		out = append(out, li)
	}
	writeJSON(w, http.StatusOK, api.AgentLoginList{Logins: out})
}

func (g *Gateway) forgetAgentLogin(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeCreate) {
		return
	}
	uid, ok := g.loginUser(w, r, p)
	if !ok {
		return
	}
	agent := r.PathValue("agent")
	g.agentLoginMu.Lock()
	err := g.store.DeleteAgentLogin(r.Context(), uid, agent)
	g.agentLoginMu.Unlock()
	if err != nil {
		if errors.Is(err, ErrNoSuchAgentLogin) {
			writeErr(w, http.StatusNotFound, api.CodeNotFound, "no "+agent+" login is kept for you")
			return
		}
		g.writeStoreErr(w, err)
		return
	}
	g.logf("agent login for %s forgotten by key %s", agent, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}
