package gateway

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// When a node stops answering, the gateway cannot know what became of its
// sandboxes: the machine may be rebooting, cut off, or gone for good. So it
// does not guess.
//
//   - While the node is down, a call on one of its sandboxes is 503
//     unavailable, "the node holding this sandbox is not answering". The
//     sandbox is not reported terminated, nor given a state the API does
//     not have: a client that reads "lost" would have to learn a fifth
//     state, and one that reads "terminated" might make a second sandbox
//     with the same name and work beside the first when the node returns.
//   - Once the node has been down longer than the grace period
//     (Config.NodeLostAfter, 5 minutes by default), its recorded sandboxes
//     are listed for the operator at GET /v1/admin/lost and no longer count
//     against their tenants' quotas, so a dead machine does not keep its
//     users from working elsewhere. Their calls are still 503. Their owner
//     records are kept.
//   - When the node answers again, its sandboxes are reconciled from its own
//     listing at once: those it still runs are back as they were (and count
//     again), and those it no longer has are forgotten.
//
// A sandbox recorded on a node the gateway no longer has configured is lost
// the same way, since nothing will ever answer for it.

// lostSandboxes is every recorded sandbox on a node past the grace period,
// with the details the store last had for it: its node cannot be asked.
func (g *Gateway) lostSandboxes(ctx context.Context) ([]api.LostSandbox, error) {
	rows, err := g.store.LiveSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	return g.lostAmong(rows), nil
}

// lostAmong is lostSandboxes over rows already read.
func (g *Gateway) lostAmong(rows []SandboxRow) []api.LostSandbox {
	lost := g.lostNodes(rows)
	if len(lost) == 0 {
		return nil
	}
	var out []api.LostSandbox
	for _, rec := range rows {
		since, ok := lost[rec.Owner.Node]
		if !ok {
			continue
		}
		ls := api.LostSandbox{ID: rec.ID, User: rec.Owner.User, Tenant: rec.Owner.Tenant, Node: rec.Owner.Node,
			CPUs: rec.CPUs, MemoryMB: rec.MemoryMB, NodeDownSince: since}
		if d := rec.Details; d != nil {
			ls.Name, ls.Image, ls.State = d.Name, d.Image, d.State
		}
		out = append(out, ls)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// lostNodes maps each node whose sandboxes are lost to when it went down:
// those unhealthy past the grace period, and every node a sandbox is
// recorded on that is not configured at all (with a zero time).
func (g *Gateway) lostNodes(rows []SandboxRow) map[string]time.Time {
	now := time.Now()
	out := map[string]time.Time{}
	for _, n := range g.nodes.list() {
		if since, down := n.downFor(); down && now.Sub(since) > g.cfg.NodeLostAfter {
			out[n.cfg.Name] = since.UTC()
		}
	}
	for _, rec := range rows {
		if _, ok := out[rec.Owner.Node]; !ok && g.nodes.get(rec.Owner.Node) == nil {
			out[rec.Owner.Node] = time.Time{}
		}
	}
	return out
}

// usageOf is a tenant's recorded usage less what is on lost nodes: the
// figure its quota is checked against.
func (g *Gateway) usageOf(ctx context.Context, tenant string) (Usage, error) {
	rows, err := g.store.SandboxHistory(ctx, SandboxFilter{Tenant: tenant, Live: true})
	if err != nil {
		return Usage{}, err
	}
	lost := g.lostNodes(rows)
	var u Usage
	for _, r := range rows {
		if _, isLost := lost[r.Owner.Node]; !isLost {
			u.Sandboxes++
			u.CPUs += r.CPUs
			u.MemoryMB += r.MemoryMB
		}
	}
	return u, nil
}

// downFor reports whether the node is unhealthy and since when.
func (n *node) downFor() (time.Time, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.downSince, !n.healthy
}

// adminLost is GET /v1/admin/lost.
func (g *Gateway) adminLost(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	out, err := g.lostSandboxes(r.Context())
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	if out == nil {
		out = []api.LostSandbox{}
	}
	writeJSON(w, http.StatusOK, api.LostList{Sandboxes: out})
}

// nodeChanged is told of every node's health changing, from the poll. A node
// that answers again is reconciled at once, rather than at the next
// reconcile tick, so its sandboxes stop being reported lost and the ones it
// no longer has stop counting against a quota.
func (g *Gateway) nodeChanged(n *node, healthy bool) {
	if !healthy {
		return
	}
	g.goBackground(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		g.reconcileNode(ctx, n)
	})
}
