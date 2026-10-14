package gateway

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Orphaned sandboxes: ones still running whose owner can no longer act
// where they are. A user suspended, revoked or expired, or removed from the
// organisation a sandbox is in, cannot reach it to end it, and nobody else
// may (mayAct): it would run until its lifetime, if it has one, taking a
// node's room.
//
// The super admin sees no one's sandboxes but these, which nobody can act
// on any more, listed with what it takes to end them (GET /v1/admin/orphans):
// the id, the owner and why they cannot. Ending one is the admin's
// terminate by id (mayEnd), as for any.
//
// "Can no longer act" is the gateway's own test, userActive: the one that
// cancels the user's jobs and stops their services' new replicas, so a
// sandbox is listed here exactly when the rest of the owner's work has
// stopped too.

// orphanedSandboxes is every live sandbox whose owner may no longer act in
// its tenant, with why.
func (g *Gateway) orphanedSandboxes(ctx context.Context) ([]api.OrphanSandbox, error) {
	rows, err := g.store.LiveSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	type who struct{ user, tenant string }
	active := map[who]bool{}
	anyKey := map[string]bool{}
	out := []api.OrphanSandbox{}
	for _, rec := range rows {
		k := who{rec.Owner.User, rec.Owner.Tenant}
		ok, seen := active[k]
		if !seen {
			if ok, err = userActive(ctx, g.store, k.user, k.tenant); err != nil {
				return nil, err
			}
			active[k] = ok
		}
		if ok {
			continue
		}
		has, seen := anyKey[k.user]
		if !seen {
			keys, err := g.store.KeysOfUser(ctx, k.user)
			if err != nil {
				return nil, err
			}
			for _, key := range keys {
				has = has || key.Active(time.Now())
			}
			anyKey[k.user] = has
		}
		why := "its owner's access ended: suspended, revoked or expired"
		if has {
			why = "its owner is no longer a member of " + wireOrg(rec.Owner.Tenant)
		}
		o := api.OrphanSandbox{ID: rec.ID, User: rec.Owner.User, Org: wireOrg(rec.Owner.Tenant), Node: rec.Owner.Node,
			CPUs: rec.CPUs, MemoryMB: rec.MemoryMB, DiskMB: rec.DiskMB, Recorded: rec.Recorded, Reason: why}
		if d := rec.Details; d != nil {
			o.Name, o.Image, o.State, o.ExpiresAt = d.Name, d.Image, string(d.State), d.ExpiresAt
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Org != out[j].Org {
			return out[i].Org < out[j].Org
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// adminOrphans is GET /v1/admin/orphans.
func (g *Gateway) adminOrphans(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	out, err := g.orphanedSandboxes(r.Context())
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.OrphanList{Sandboxes: out})
}
