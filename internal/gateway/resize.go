package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// resizeHandler serves POST /v1/sandboxes/{ref}/resize. The node restarts
// the sandbox at its new size (sandboxd's resize); the gateway holds what
// it adds to two limits a create is held to, since a resize is a way to
// take more than a create could:
//
//   - the owner's tenant quota, counting only the growth: a sandbox it
//     already holds is counted once;
//   - the node's free room, for the growth too: the sandbox cannot move,
//     so a node without the room refuses it rather than another taking it.
//
// The store follows the node's answer, which may be the old size when the
// new one did not start there.
func (g *Gateway) resizeHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	id, o, n, err := g.resolve(r.Context(), p, r.PathValue("ref"), ScopeCreate)
	if err != nil {
		writeRouteErr(w, err, ScopeCreate)
		return
	}
	var req api.ResizeSandboxRequest
	if !decode(w, r, &req) {
		return
	}
	w, r, done := g.track(w, r, p, ScopeCreate, id, o.Node)
	defer done()
	ctx := r.Context()

	cur, err := n.client.Sandbox(ctx, id)
	if err != nil {
		writeNodeErr(w, err)
		return
	}
	grow := api.NodeResources{}
	if req.CPUs > cur.CPUs {
		grow.CPUs = req.CPUs - cur.CPUs
	}
	if req.MemoryMB > cur.MemoryMB {
		grow.MemoryMB = req.MemoryMB - cur.MemoryMB
	}
	if req.DiskMB > cur.DiskMB {
		grow.DiskMB = req.DiskMB - cur.DiskMB
	}
	hold, err := g.reserveGrowth(ctx, o.Tenant, grow)
	if err != nil {
		var q *errQuota
		if errors.As(err, &q) {
			g.metrics.refused.Inc(refusedQuota)
			writeErr(w, http.StatusForbidden, api.CodeRefused, err.Error())
			return
		}
		g.writeStoreErr(w, err)
		return
	}
	defer hold.release()
	if msg := roomFor(n, grow); msg != "" {
		writeErr(w, http.StatusConflict, api.CodeConflict, msg)
		return
	}
	pl := n.reserve(grow)

	out, err := n.client.Resize(ctx, id, req)
	n.finish(pl, err == nil)
	if err != nil {
		// A refused or failed resize may still have changed the size (a
		// disk grown, the old CPUs back): the record follows the node.
		if sb, gerr := n.client.Sandbox(context.WithoutCancel(ctx), id); gerr == nil {
			if rerr := hold.record(ctx, g.store.ForTenant(o.Tenant), o, sb); rerr != nil {
				g.logf("resize %s: recording its size: %v", id, rerr)
			}
		}
		writeNodeErr(w, err)
		return
	}
	if err := hold.record(ctx, g.store.ForTenant(o.Tenant), o, out); err != nil {
		g.logf("resize %s: recording its size: %v", id, err)
	}
	g.logf("sandbox %s resized by key %s to %v CPUs, %d MB, disk %d MB", id, p.KeyID, out.CPUs, out.MemoryMB, out.DiskMB)
	writeJSON(w, http.StatusOK, out)
}

// roomFor is why n cannot take grow more, or "" when it can: its free
// room as its last status said, less what is placed on it since.
func roomFor(n *node, grow api.NodeResources) string {
	if grow == (api.NodeResources{}) {
		return ""
	}
	c, ok := n.candidate()
	if !ok {
		return "node " + n.cfg.Name + " is not answering"
	}
	free := c.Status.Free
	free.CPUs -= c.Reserved.CPUs
	free.MemoryMB -= c.Reserved.MemoryMB
	free.DiskMB -= c.Reserved.DiskMB
	if grow.CPUs > free.CPUs+1e-9 || grow.MemoryMB > free.MemoryMB || grow.DiskMB > free.DiskMB {
		return fmt.Sprintf("node %s, where the sandbox runs, has room for %v more CPUs, %d MB of memory and %d MB of disk", n.cfg.Name, max(free.CPUs, 0), max(free.MemoryMB, 0), max(free.DiskMB, 0))
	}
	return ""
}
