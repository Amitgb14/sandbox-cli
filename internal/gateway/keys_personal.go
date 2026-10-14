package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Personal keys: API keys a user makes for themselves, for a program to
// act as them — a CI job, a script, the CLI on another machine — without
// asking an admin, and without handing over their password.
//
// A personal key can only be less than what made it:
//
//   - It is made under a root: the account its maker signed in to, or the
//     plain key they were acting with (Key.Parent). Like a sign-in's key it
//     is cut off with that root — revoked, suspended, expired, or shortened
//     to end sooner — so a user an admin stops cannot carry on through a
//     key they made. A suspension revokes it for good; a restored account
//     makes its keys again.
//   - It acts in one organisation, the one it was made in (Key.Bound), and
//     only while its user may act there: a leaked one exposes that
//     organisation's work and no other of its user's. It is listed there.
//   - Its scopes are some of the root's, never admin: an admin's keys are
//     issued through /v1/admin/keys, where the issuing is an admin's act.
//     An admin's own account holds every other scope, so a super admin
//     makes personal keys for their own work like anyone else.
//   - It always ends, by maxPersonalKeyLife at the latest and never after
//     its root.
//   - It manages no keys, and is not its account (AccountOf): a leaked one
//     cannot change the password, make another key that outlives it, or
//     revoke its user's other keys.
//
// Its user's policy applies to it as to every key of theirs: a policy is
// the user's, not a key's.
//
// Unlike a sign-in's key it outlasts signing out and a new password: it was
// made on purpose, for a program, and is revoked one by one.

const (
	maxPersonalKeys    = 20 // live ones under one root
	minPersonalKeyLife = time.Hour
	maxPersonalKeyLife = 366 * 24 * time.Hour
)

// Errors of personal keys.
var (
	ErrPersonalKey  = errors.New("a personal key cannot manage keys; use the sign-in or key it was made with")
	ErrTooManyKeys  = fmt.Errorf("at most %d personal keys may be live at once; revoke one first", maxPersonalKeys)
	ErrKeyOutlives  = errors.New("a personal key cannot outlive the account or key it is made under")
	ErrScopeNotHeld = errors.New("a personal key can only have scopes you hold")
	ErrNotInOrg     = errors.New("a personal key is made in an organization you belong to; this one you are not in")
)

// personalRoot is what personal keys made with key by hang from: by's
// account for a sign-in, by itself for a plain key. A personal key has no
// root; it makes and manages nothing.
func (q *sqlTx) personalRoot(ctx context.Context, by string, lock bool) (caller, root Key, err error) {
	caller, ok, err := q.key(ctx, "WHERE k.id = ?", by)
	switch {
	case err != nil:
		return Key{}, Key{}, err
	case !ok:
		return Key{}, Key{}, ErrNoSuchKey
	case caller.Personal:
		return Key{}, Key{}, ErrPersonalKey
	}
	lockClause := ""
	if lock {
		lockClause = q.s.forUpdateOf("k")
	}
	id := caller.ID
	if caller.Parent != "" {
		id = caller.Parent
	}
	root, ok, err = q.key(ctx, "WHERE k.id = ?"+lockClause, id)
	switch {
	case err != nil:
		return Key{}, Key{}, err
	case !ok:
		return Key{}, Key{}, ErrNoSuchKey
	case !root.Active(q.s.stamp()):
		return Key{}, Key{}, ErrKeyRevoked
	}
	return caller, root, nil
}

// holds reports whether k may give scope to a personal key: one it was
// issued, or any but admin for an admin's.
func holds(k Key, scope string) bool {
	return slices.Contains(k.Scopes, scope) || (scope != ScopeAdmin && slices.Contains(k.Scopes, ScopeAdmin))
}

func (s *sqlStore) CreatePersonalKey(ctx context.Context, by, org, name string, scopes []string, expires time.Time) (string, Key, error) {
	if err := ValidateScopes(scopes); err != nil {
		return "", Key{}, badKeyRequest{err}
	}
	if slices.Contains(scopes, ScopeAdmin) {
		return "", Key{}, badKeyRequest{errors.New("a personal key cannot have the admin scope; an admin issues admin keys")}
	}
	secret := newSecret(keySecretPrefix)
	now := s.stamp()
	expires = expires.UTC().Truncate(time.Microsecond)
	if !expires.After(now) {
		return "", Key{}, badKeyRequest{errors.New("a key's expiry must be in the future")}
	}
	var out Key
	err := s.tx(ctx, func(q *sqlTx) error {
		// The root is locked, so two creates at once count each other.
		caller, root, err := q.personalRoot(ctx, by, true)
		if err != nil {
			return err
		}
		for _, sc := range scopes {
			if !holds(root, sc) || !holds(caller, sc) {
				return fmt.Errorf("%w: not %s", ErrScopeNotHeld, sc)
			}
		}
		// Its user's own tenant, or an organisation they are in. An admin
		// may select any organisation, but its key acts as the user, not as
		// an admin, so it needs a membership like anyone's.
		if !strings.EqualFold(org, root.Tenant) {
			_, id, found, err := q.teamOrg(ctx, org)
			if err != nil {
				return err
			}
			member := false
			if found {
				if _, _, member, err = q.memberRole(ctx, id, root.User, root.Tenant); err != nil {
					return err
				}
			}
			if !member {
				return ErrNotInOrg
			}
			org = strings.ToLower(org)
		} else {
			org = root.Tenant
		}
		if root.Expires != nil && expires.After(*root.Expires) {
			return badKeyRequest{fmt.Errorf("%w (it ends %s)", ErrKeyOutlives, root.Expires.Format(time.RFC3339))}
		}
		var n int
		if err := q.row(ctx, "SELECT COUNT(*) FROM api_keys WHERE parent = ? AND personal AND NOT revoked AND (expires IS NULL OR expires > ?)",
			root.ID, s.t(now)).Scan(&n); err != nil {
			return storeErr(err)
		}
		if n >= maxPersonalKeys {
			return ErrTooManyKeys
		}
		out = Key{ID: newID("key_"), User: root.User, Tenant: root.Tenant, Scopes: slices.Clone(scopes),
			Hash: hashSecret(secret), Created: now, Expires: &expires, Parent: root.ID, Personal: true, Name: name,
			Bound: true, Org: org}
		return q.insertKey(ctx, out)
	})
	if err != nil {
		return "", Key{}, err
	}
	return secret, out, nil
}

func (s *sqlStore) PersonalKeys(ctx context.Context, by, org string) ([]Key, error) {
	var out []Key
	err := s.read(ctx, func(q *sqlTx) error {
		_, root, err := q.personalRoot(ctx, by, false)
		if err != nil {
			return err
		}
		if !strings.EqualFold(org, root.Tenant) {
			org = strings.ToLower(org)
		} else {
			org = root.Tenant
		}
		out, err = q.keys(ctx, "WHERE k.parent = ? AND k.personal AND b.key = ? ORDER BY k.created, k.id", root.ID, orgKeyOf(org))
		return err
	})
	return out, err
}

func (s *sqlStore) RevokePersonalKey(ctx context.Context, by, id string) (Key, error) {
	var out Key
	err := s.tx(ctx, func(q *sqlTx) error {
		_, root, err := q.personalRoot(ctx, by, false)
		if err != nil {
			return err
		}
		// Only one of the caller's own: another user's key is "no such key".
		k, ok, err := q.key(ctx, "WHERE k.id = ? AND k.parent = ? AND k.personal", id, root.ID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoSuchKey
		}
		if _, err := q.exec(ctx, "UPDATE api_keys SET revoked = ?, restore_until = NULL WHERE id = ?", true, id); err != nil {
			return err
		}
		k.Revoked = true
		out = k
		return nil
	})
	return out, err
}

// badKeyRequest is a refusal of what a create asked for, said back to the
// caller; any other error is the store's, and is not.
type badKeyRequest struct{ error }

func (e badKeyRequest) Unwrap() error { return e.error }

// checkKeyName refuses a name that is empty, long, or would print as
// something else: it is shown back in Studio and the CLI.
func checkKeyName(name string) error {
	if name == "" || len(name) > 64 {
		return errors.New("name: 1 to 64 characters")
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return errors.New("name: printable characters only")
		}
	}
	return nil
}

// --- handlers -------------------------------------------------------------------

// personalCaller refuses a principal that is not a key of its own: an SSH
// token, confined to one sandbox, makes no keys.
func personalCaller(w http.ResponseWriter, p Principal) bool {
	if p.Sandbox != "" || p.KeyID == "" {
		writeErr(w, http.StatusForbidden, api.CodeRefused, "only an API key or a sign-in manages personal keys")
		return false
	}
	return true
}

// writePersonalErr answers for the refusals of a personal key's store calls.
func (g *Gateway) writePersonalErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrPersonalKey), errors.Is(err, ErrNotInOrg):
		writeErr(w, http.StatusForbidden, api.CodeRefused, err.Error())
	case errors.Is(err, ErrNoSuchKey):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such key")
	case errors.Is(err, ErrKeyRevoked):
		writeErr(w, http.StatusForbidden, api.CodeRefused, "the account or key this would be made under is not active")
	case errors.Is(err, ErrTooManyKeys):
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
	case errors.As(err, new(badKeyRequest)):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
	default:
		g.writeStoreErr(w, err)
	}
}

// createPersonalKey is POST /v1/keys.
func (g *Gateway) createPersonalKey(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !personalCaller(w, p) {
		return
	}
	var req api.CreatePersonalKeyRequest
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if err := checkKeyName(req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	life := time.Duration(req.ExpiresInSecs) * time.Second
	if req.ExpiresInSecs <= 0 || life < minPersonalKeyLife || life > maxPersonalKeyLife {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest,
			fmt.Sprintf("expires_in_secs: a personal key always ends, from %d (an hour) to %d (366 days)", int(minPersonalKeyLife/time.Second), int(maxPersonalKeyLife/time.Second)))
		return
	}
	for _, sc := range req.Scopes {
		if slices.Contains(AllScopes, sc) && !p.Can(sc) {
			writeErr(w, http.StatusForbidden, api.CodeRefused, ErrScopeNotHeld.Error()+": not "+sc)
			return
		}
	}
	secret, k, err := g.store.CreatePersonalKey(ctx, p.KeyID, p.Tenant, req.Name, req.Scopes, time.Now().Add(life))
	if err != nil {
		if errors.Is(err, ErrScopeNotHeld) {
			writeErr(w, http.StatusForbidden, api.CodeRefused, err.Error())
			return
		}
		g.writePersonalErr(w, err)
		return
	}
	g.logf("personal key %s made by %s for %s in %s", k.ID, p.KeyID, k.User, wireOrg(k.Org))
	g.writeKey(w, ctx, http.StatusCreated, k, secret)
}

// listPersonalKeys is GET /v1/keys.
func (g *Gateway) listPersonalKeys(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !personalCaller(w, p) {
		return
	}
	keys, err := g.store.PersonalKeys(ctx, p.KeyID, p.Tenant)
	if err != nil {
		g.writePersonalErr(w, err)
		return
	}
	out := api.KeyList{Keys: []api.KeyInfo{}}
	for _, k := range keys {
		ki, err := g.keyInfo(ctx, k)
		if err != nil {
			g.writeStoreErr(w, err)
			return
		}
		out.Keys = append(out.Keys, ki)
	}
	writeJSON(w, http.StatusOK, out)
}

// revokePersonalKey is DELETE /v1/keys/{id}.
func (g *Gateway) revokePersonalKey(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !personalCaller(w, p) {
		return
	}
	id := r.PathValue("id")
	if _, err := g.store.RevokePersonalKey(ctx, p.KeyID, id); err != nil {
		g.writePersonalErr(w, err)
		return
	}
	g.logf("personal key %s revoked by %s", id, p.KeyID)
	// What the key has open — a stream, an SSH session — is ended as for
	// any revocation.
	g.accessChanged()
	w.WriteHeader(http.StatusNoContent)
}
