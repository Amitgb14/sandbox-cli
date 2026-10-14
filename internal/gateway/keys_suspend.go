package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Suspending a key.
//
// A revocation is for good: a key revoked by mistake is a new invite, a new
// password and the user's work started again. A suspension is a revocation
// that can be taken back for a while:
//
//   - It is a revocation from the moment it is made. The key is marked
//     revoked, so every check that refuses a revoked key — a request, an
//     open stream, an SSH login, a job's run, a service's next replica,
//     a sign-in — refuses it, with nothing new to forget; its sign-ins'
//     keys are revoked with it, and accessChanged ends what it has open.
//   - RestoreUntil says until when it may be restored, suspendGrace after.
//     A restore puts back the key alone: its sign-ins stay ended, its jobs
//     stay cancelled, and its services make replicas again, as they do for
//     any owner whose key is active.
//   - Past RestoreUntil it is a revoked key like any other; nothing runs at
//     that moment, since nothing changes but what a restore is allowed.
//   - Until then its address and user are still its own: an invite for
//     either is refused, as for a live account, so a restore cannot find
//     them taken.
//   - An admin cannot suspend the key they are acting with, or the account
//     it signs in to: nobody would be left to restore it.

// suspendGrace is how long a suspended key may be restored.
const suspendGrace = time.Hour

// Errors of suspension.
var (
	ErrNotSuspended = errors.New("the key is not suspended")
	ErrSignInKey    = errors.New("this is a sign-in's key; suspend the account it signs in to")
	// A personal key is revoked, or its account suspended: restoring one
	// alone would bring back a key its user may have revoked meanwhile.
	ErrPersonalSuspend = errors.New("this is a personal key; revoke it, or suspend the account it was made under")
)

// SuspendKey revokes key id and its sign-ins' keys, restorable until until.
func (s *sqlStore) SuspendKey(ctx context.Context, id string, until time.Time) (Key, error) {
	var out Key
	err := s.tx(ctx, func(q *sqlTx) error {
		k, ok, err := q.key(ctx, "WHERE k.id = ?"+s.forUpdateOf("k"), id)
		switch {
		case err != nil:
			return err
		case !ok:
			return ErrNoSuchKey
		case k.Revoked:
			return ErrKeyRevoked
		case k.Personal:
			return ErrPersonalSuspend
		case k.Parent != "":
			return ErrSignInKey
		}
		until = until.UTC().Truncate(time.Microsecond)
		if _, err := q.exec(ctx, "UPDATE api_keys SET revoked = ?, restore_until = ? WHERE id = ?", true, s.t(until), id); err != nil {
			return err
		}
		if _, err := q.exec(ctx, "UPDATE api_keys SET revoked = ? WHERE parent = ?", true, id); err != nil {
			return err
		}
		k.Revoked, k.RestoreUntil = true, &until
		out = k
		return nil
	})
	return out, err
}

// RestoreKey takes back a suspension that has not run out.
func (s *sqlStore) RestoreKey(ctx context.Context, id string) (Key, error) {
	var out Key
	err := s.tx(ctx, func(q *sqlTx) error {
		k, ok, err := q.key(ctx, "WHERE k.id = ?"+s.forUpdateOf("k"), id)
		switch {
		case err != nil:
			return err
		case !ok:
			return ErrNoSuchKey
		case !k.Revoked:
			return ErrNotSuspended
		case !k.Suspended(s.stamp()):
			return ErrKeyRevoked
		}
		if _, err := q.exec(ctx, "UPDATE api_keys SET revoked = ?, restore_until = NULL WHERE id = ?", false, id); err != nil {
			return err
		}
		k.Revoked, k.RestoreUntil = false, nil
		out = k
		return nil
	})
	return out, err
}

// adminSuspendKey is POST /v1/admin/keys/{id}/suspend.
func (g *Gateway) adminSuspendKey(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeAdmin) {
		return
	}
	id := r.PathValue("id")
	if id == p.KeyID {
		writeErr(w, http.StatusConflict, api.CodeConflict, "this is the key you are using; suspend it with another admin's")
		return
	}
	if acct, ok, err := g.store.AccountOf(ctx, p.KeyID); err != nil {
		g.writeStoreErr(w, err)
		return
	} else if ok && acct.ID == id {
		writeErr(w, http.StatusConflict, api.CodeConflict, "this is the account you are signed in to; suspend it with another admin's")
		return
	}
	k, err := g.store.SuspendKey(ctx, id, time.Now().Add(suspendGrace))
	switch {
	case errors.Is(err, ErrNoSuchKey):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such key")
		return
	case errors.Is(err, ErrKeyRevoked):
		writeErr(w, http.StatusConflict, api.CodeConflict, "the key is revoked already")
		return
	case errors.Is(err, ErrSignInKey), errors.Is(err, ErrPersonalSuspend):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	case err != nil:
		g.writeStoreErr(w, err)
		return
	}
	g.logf("key %s suspended by %s, revoked for good at %s", id, p.KeyID, k.RestoreUntil.Format(time.RFC3339))
	g.accessChanged()
	g.writeKey(w, ctx, http.StatusOK, k, "")
}

// adminRestoreKey is POST /v1/admin/keys/{id}/restore.
func (g *Gateway) adminRestoreKey(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeAdmin) {
		return
	}
	id := r.PathValue("id")
	k, err := g.store.RestoreKey(ctx, id)
	switch {
	case errors.Is(err, ErrNoSuchKey):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such key")
		return
	case errors.Is(err, ErrNotSuspended):
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
		return
	case errors.Is(err, ErrKeyRevoked):
		writeErr(w, http.StatusConflict, api.CodeConflict, "the key's suspension ran out; it is revoked for good")
		return
	case err != nil:
		g.writeStoreErr(w, err)
		return
	}
	g.logf("key %s restored by %s", id, p.KeyID)
	// What was refused while it was suspended is allowed again: forget the
	// answers held for it.
	g.accessChanged()
	g.writeKey(w, ctx, http.StatusOK, k, "")
}
