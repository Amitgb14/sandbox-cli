package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Draining a node takes it out of service: it is cordoned, so nothing new
// lands there, and then either its sandboxes are left to end on their own
// (an idle timeout, their users deleting them) or the gateway terminates
// them. Upgrading a node is cordon, drain or wait, upgrade, uncordon
// (docs/self-hosting.md).
//
// The gateway remembers the cordon it asked for. A node holds its cordon in
// memory, so one that restarts — which is what an upgrade does — comes back
// uncordoned; the gateway puts the cordon back on its next poll, and until
// then places nothing there, so a node restarted mid-drain does not fill
// up again before the operator says it may.

// drainConcurrency bounds the terminations a drain has in flight at once.
const drainConcurrency = 8

// cordonNode asks the node to cordon or uncordon itself and remembers the
// wish (wantCordon) for when the node restarts without it.
func (g *Gateway) cordonNode(ctx context.Context, n *node, cordoned bool) error {
	n.mu.Lock()
	n.wantCordon = cordoned
	n.mu.Unlock()
	body, _ := json.Marshal(api.CordonRequest{Cordoned: cordoned})
	resp, err := n.do(ctx, http.MethodPost, "/v1/node/cordon", nil, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return readAPIError(resp)
	}
	n.mu.Lock()
	if n.status != nil {
		st := *n.status
		st.Cordoned = cordoned
		n.status = &st
	}
	n.mu.Unlock()
	return nil
}

// nodePolled puts back a cordon a node lost by restarting.
func (g *Gateway) nodePolled(n *node, st api.NodeStatus) {
	n.mu.Lock()
	want := n.wantCordon
	n.mu.Unlock()
	if !want || st.Cordoned {
		return
	}
	ctx, cancel := context.WithTimeout(g.baseCtx(), 10*time.Second)
	defer cancel()
	if err := g.cordonNode(ctx, n, true); err != nil {
		g.logf("node %s came back uncordoned and could not be cordoned again: %v", n.cfg.Name, err)
		return
	}
	g.logf("node %s came back uncordoned; cordoned it again, as it was asked to be", n.cfg.Name)
}

// adminDrain is POST /v1/admin/nodes/{name}/drain.
func (g *Gateway) adminDrain(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	var req api.DrainRequest
	if !decode(w, r, &req) {
		return
	}
	n := g.nodes.get(r.PathValue("name"))
	if n == nil {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such node")
		return
	}
	if !n.isHealthy() {
		writeErr(w, http.StatusServiceUnavailable, api.CodeUnavailable, "node "+n.cfg.Name+" is not answering; it cannot be drained until it does")
		return
	}
	ctx := r.Context()
	if err := g.cordonNode(ctx, n, true); err != nil {
		g.logf("drain %s: cordon: %v", n.cfg.Name, err)
		writeErr(w, http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" did not take the cordon")
		return
	}
	g.logf("node %s cordoned for a drain by %s (terminate=%v)", n.cfg.Name, p.KeyID, req.Terminate)
	var list api.SandboxList
	if err := n.getJSON(ctx, "/v1/sandboxes", nil, &list); err != nil {
		g.logf("drain %s: listing: %v", n.cfg.Name, err)
		writeErr(w, http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" is cordoned, but did not list its sandboxes")
		return
	}
	var live []string
	for _, sb := range list.Sandboxes {
		if sb.State != api.StateTerminated {
			live = append(live, sb.ID)
		}
	}
	res := api.DrainResult{Node: n.cfg.Name, Cordoned: true, Terminated: []string{}}
	if !req.Terminate {
		res.Remaining = len(live)
		writeJSON(w, http.StatusOK, res)
		return
	}

	// Every sandbox on the node goes, including any not made through the
	// gateway: the point of the drain is a node with nothing on it.
	var mu sync.Mutex
	sem := make(chan struct{}, drainConcurrency)
	var wg sync.WaitGroup
	for _, id := range live {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			err := g.terminateOn(ctx, n, id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Failed = append(res.Failed, api.DrainFailure{ID: id, Error: err.Error()})
				return
			}
			res.Terminated = append(res.Terminated, id)
			g.audit.write(api.AuditEntry{Kind: "api", Action: "drain.terminate", KeyID: p.KeyID, User: p.User,
				Tenant: p.Tenant, Remote: remoteIP(r.RemoteAddr), Sandbox: id, Node: n.cfg.Name, Status: http.StatusNoContent})
		}()
	}
	wg.Wait()
	sort.Strings(res.Terminated)
	sort.Slice(res.Failed, func(i, j int) bool { return res.Failed[i].ID < res.Failed[j].ID })
	res.Remaining = len(res.Failed)
	writeJSON(w, http.StatusOK, res)
}

// terminateOn deletes one sandbox on n and forgets its owner, as a DELETE
// through the gateway would. Not found counts as done: it ended meanwhile.
func (g *Gateway) terminateOn(ctx context.Context, n *node, id string) error {
	ctx = context.WithoutCancel(ctx) // a caller who hangs up does not leave a drain half-done
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := n.do(ctx, http.MethodDelete, "/v1/sandboxes/"+url.PathEscape(id), nil, nil, "")
	if err != nil {
		return fmt.Errorf("the node did not answer")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return readAPIError(resp)
	}
	if o, ok := g.store.OwnerOf(id); ok {
		g.tombs.add(id, o)
	}
	if err := g.store.ForgetSandbox(id); err != nil {
		g.logf("drain: forgetting %s: %v", id, err)
	}
	g.metrics.terminated.Inc("drain")
	return nil
}
