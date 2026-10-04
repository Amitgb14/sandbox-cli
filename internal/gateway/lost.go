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

// lostSandboxes is every recorded sandbox on a node past the grace period.
func (g *Gateway) lostSandboxes() []api.LostSandbox {
	lost := g.lostNodes()
	if len(lost) == 0 {
		return nil
	}
	g.store.mu.RLock()
	defer g.store.mu.RUnlock()
	var out []api.LostSandbox
	for id, rec := range g.store.st.Sandboxes {
		since, ok := lost[rec.Node]
		if !ok {
			continue
		}
		out = append(out, api.LostSandbox{ID: id, User: rec.User, Tenant: rec.Tenant, Node: rec.Node,
			CPUs: rec.CPUs, MemoryMB: rec.MemoryMB, NodeDownSince: since})
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
func (g *Gateway) lostNodes() map[string]time.Time {
	now := time.Now()
	out := map[string]time.Time{}
	for _, n := range g.nodes.list() {
		if since, down := n.downFor(); down && now.Sub(since) > g.cfg.NodeLostAfter {
			out[n.cfg.Name] = since.UTC()
		}
	}
	g.store.mu.RLock()
	for _, rec := range g.store.st.Sandboxes {
		if _, ok := out[rec.Node]; !ok && g.nodes.get(rec.Node) == nil {
			out[rec.Node] = time.Time{}
		}
	}
	g.store.mu.RUnlock()
	return out
}

// usageOf is a tenant's recorded usage less what is on lost nodes: the
// figure its quota is checked against.
func (g *Gateway) usageOf(tenant string) Usage {
	lost := g.lostNodes()
	if len(lost) == 0 {
		return g.store.UsageOf(tenant)
	}
	g.store.mu.RLock()
	defer g.store.mu.RUnlock()
	var u Usage
	for _, r := range g.store.st.Sandboxes {
		if _, isLost := lost[r.Node]; r.Tenant == tenant && !isLost {
			u.Sandboxes++
			u.CPUs += r.CPUs
			u.MemoryMB += r.MemoryMB
		}
	}
	return u
}

// sandboxCount is how many sandboxes the store records.
func (s *FileStore) sandboxCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.st.Sandboxes)
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
	out := g.lostSandboxes()
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
