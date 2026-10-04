package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// --- capabilities -------------------------------------------------------------

func (g *Gateway) capabilities(w http.ResponseWriter, r *http.Request, p Principal) {
	caps, ok := g.combinedCapabilities()
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, api.CodeInternal, "no node is answering")
		return
	}
	writeJSON(w, http.StatusOK, caps)
}

// combinedCapabilities is what the fleet offers, from the healthy nodes'
// last statuses. A fleet is expected to be uniform, and where it is not the
// answer is what every node can do — a capability only if all have it, the
// smallest limits, the strictest network ceiling — because a client that
// reads a capability may send a request the scheduler then has to place.
// Saying less than some node could do turns a request away; saying more
// would have it refused by the node it lands on.
func (g *Gateway) combinedCapabilities() (api.Capabilities, bool) {
	var sts []api.NodeStatus
	for _, c := range g.nodes.candidates() {
		sts = append(sts, c.Status)
	}
	if len(sts) == 0 {
		return api.Capabilities{}, false
	}
	return CombineCapabilities(sts), true
}

// CombineCapabilities folds several nodes' capabilities into the fleet's
// (see combinedCapabilities).
func CombineCapabilities(sts []api.NodeStatus) api.Capabilities {
	first := sts[0].Capabilities
	out := api.Capabilities{
		APIVersion: api.Version,
		Limits:     first.Limits,
		Network: api.NetworkCeiling{
			Default:  first.Network.Default,
			Ceiling:  first.Network.Ceiling,
			MayAllow: slices.Clone(first.Network.MayAllow),
		},
		Capabilities: map[string]bool{},
	}
	backends := []string{}
	for _, st := range sts {
		c := st.Capabilities
		if !slices.Contains(backends, c.Backend) {
			backends = append(backends, c.Backend)
		}
		for k := range c.Capabilities {
			out.Capabilities[k] = true
		}
		l := &out.Limits
		l.MaxCPUs = min(l.MaxCPUs, c.Limits.MaxCPUs)
		l.MaxMemoryMB = min(l.MaxMemoryMB, c.Limits.MaxMemoryMB)
		l.MaxDiskMB = min(l.MaxDiskMB, c.Limits.MaxDiskMB)
		// 0 is "no bound": the bounded one is the stricter.
		if l.MaxIdleTimeoutSecs == 0 || (c.Limits.MaxIdleTimeoutSecs > 0 && c.Limits.MaxIdleTimeoutSecs < l.MaxIdleTimeoutSecs) {
			l.MaxIdleTimeoutSecs = c.Limits.MaxIdleTimeoutSecs
		}
		if api.NetworkRank(c.Network.Ceiling) < api.NetworkRank(out.Network.Ceiling) {
			out.Network.Ceiling = c.Network.Ceiling
		}
		out.Network.MayAllow = intersectAllow(out.Network.MayAllow, c.Network.MayAllow)
	}
	for k := range out.Capabilities {
		for _, st := range sts {
			if !st.Capabilities.Capabilities[k] {
				out.Capabilities[k] = false
			}
		}
	}
	if api.NetworkRank(out.Network.Default.Mode) > api.NetworkRank(out.Network.Ceiling) {
		out.Network.Default = api.NetworkPolicy{Mode: out.Network.Ceiling, Allow: []string{}}
	}
	sort.Strings(backends)
	out.Backend = strings.Join(backends, "+")
	return out
}

// intersectAllow is the names both lists permit; "*" permits every name.
func intersectAllow(a, b []string) []string {
	switch {
	case slices.Contains(a, "*"):
		return slices.Clone(b)
	case slices.Contains(b, "*"):
		return a
	}
	out := []string{}
	for _, x := range a {
		if slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

// --- volumes ------------------------------------------------------------------

// Volume names are one namespace across the gateway, as on one node: a
// volume is named in a mount, and two users' "data" would make that name
// mean whichever node the sandbox landed on.

func (g *Gateway) createVolume(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeCreate) {
		return
	}
	var req api.CreateVolumeRequest
	if !decode(w, r, &req) {
		return
	}
	caps, ok := g.combinedCapabilities()
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, api.CodeInternal, "no node is answering")
		return
	}
	if !spec.ValidName(req.Name) {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "name: lowercase letters, digits and dashes, at most 63")
		return
	}
	key := "vol\x00" + req.Name
	if !g.claim(key) {
		writeErr(w, http.StatusConflict, api.CodeConflict, "a volume named "+req.Name+" already exists")
		return
	}
	defer g.unclaim(key)
	if _, taken := g.store.VolumeOwner(req.Name); taken {
		writeErr(w, http.StatusConflict, api.CodeConflict, "a volume named "+req.Name+" already exists")
		return
	}
	n := g.volumeNode(p, caps.Has(api.CapVolumes))
	if n == nil {
		writeErr(w, http.StatusServiceUnavailable, api.CodeInternal, "no node is taking new volumes")
		return
	}
	body, _ := json.Marshal(req)
	resp, err := n.do(r.Context(), http.MethodPost, "/v1/volumes", nil, bytes.NewReader(body), "application/json")
	if err != nil {
		writeErr(w, http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" did not answer")
		return
	}
	data, err := readBody(resp)
	if err != nil {
		writeErr(w, http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" did not answer")
		return
	}
	if resp.StatusCode == http.StatusCreated {
		if err := g.store.SetVolume(req.Name, Owner{User: p.User, Tenant: p.Tenant, Node: n.cfg.Name}); err != nil {
			g.logf("recording volume %s: %v", req.Name, err)
			writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the volume's owner could not be recorded")
			return
		}
	}
	relay(w, resp, data)
}

// volumeNode picks a node for a new volume: the one holding most of the
// caller's volumes, so a sandbox can mount them together, then the most free
// disk. With no node offering volumes, any node, to refuse it.
func (g *Gateway) volumeNode(p Principal, anyHas bool) *node {
	cands := g.nodes.candidates()
	if !anyHas {
		if name, ok := Fallback(cands, nil); ok {
			return g.nodes.get(name)
		}
		return nil
	}
	held := map[string]int{}
	for _, nodeName := range g.store.VolumesOf(p.User, p.Tenant) {
		held[nodeName]++
	}
	var best *Candidate
	for i := range cands {
		c := &cands[i]
		if c.Status.Cordoned || !c.Status.Capabilities.Has(api.CapVolumes) {
			continue
		}
		if best == nil || held[c.Name] > held[best.Name] ||
			(held[c.Name] == held[best.Name] && free(*c).DiskMB > free(*best).DiskMB) {
			best = c
		}
	}
	if best == nil {
		return nil
	}
	return g.nodes.get(best.Name)
}

func (g *Gateway) listVolumes(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	if caps, ok := g.combinedCapabilities(); ok && !caps.Has(api.CapVolumes) {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint has no volumes")
		return
	}
	var mu sync.Mutex
	out := []api.Volume{}
	err := fanOut(r.Context(), g.nodes.healthy(), func(ctx context.Context, n *node) error {
		if !n.hasCap(api.CapVolumes) {
			return nil
		}
		var list api.VolumeList
		if err := n.getJSON(ctx, "/v1/volumes", nil, &list); err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, v := range list.Volumes {
			if o, ok := g.store.VolumeOwner(v.Name); ok && o.Node == n.cfg.Name && mayAct(p, o) {
				out = append(out, v)
			}
		}
		return nil
	})
	if err != nil {
		writeRouteErr(w, err, ScopeRead)
		return
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, api.VolumeList{Volumes: out})
}

func (g *Gateway) deleteVolume(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeDelete) {
		return
	}
	name := r.PathValue("name")
	o, ok := g.store.VolumeOwner(name)
	if !ok || !mayAct(p, o) {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no volume named "+name)
		return
	}
	n := g.nodes.get(o.Node)
	if n == nil || !n.isHealthy() {
		writeRouteErr(w, ErrNodeDown, "")
		return
	}
	resp, err := n.do(r.Context(), http.MethodDelete, "/v1/volumes/"+url.PathEscape(name), nil, nil, "")
	if err != nil {
		writeErr(w, http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" did not answer")
		return
	}
	data, _ := readBody(resp)
	// Gone on the node, by this request or before it: forget it.
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		if err := g.store.ForgetVolume(name); err != nil {
			g.logf("forgetting volume %s: %v", name, err)
		}
	}
	relay(w, resp, data)
}

func (n *node) hasCap(c string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status != nil && n.status.Capabilities.Has(c)
}

// --- snapshots ----------------------------------------------------------------

func (g *Gateway) listSnapshots(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	var mu sync.Mutex
	out := []api.Snapshot{}
	err := fanOut(r.Context(), g.nodes.healthy(), func(ctx context.Context, n *node) error {
		var list api.SnapshotList
		if err := n.getJSON(ctx, "/v1/snapshots", nil, &list); err != nil {
			// A node without snapshots says so; it holds none.
			if api.IsCode(err, api.CodeUnsupported) {
				return nil
			}
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, s := range list.Snapshots {
			if o, ok := g.store.SnapshotOwner(s.ID); ok && o.Node == n.cfg.Name && mayAct(p, o) {
				out = append(out, s)
			}
		}
		return nil
	})
	if err != nil {
		writeRouteErr(w, err, ScopeRead)
		return
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	writeJSON(w, http.StatusOK, api.SnapshotList{Snapshots: out})
}

func (g *Gateway) deleteSnapshot(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeDelete) {
		return
	}
	id := r.PathValue("id")
	o, ok := g.store.SnapshotOwner(id)
	if !ok || !mayAct(p, o) {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such snapshot")
		return
	}
	n := g.nodes.get(o.Node)
	if n == nil || !n.isHealthy() {
		writeRouteErr(w, ErrNodeDown, "")
		return
	}
	resp, err := n.do(r.Context(), http.MethodDelete, "/v1/snapshots/"+url.PathEscape(id), nil, nil, "")
	if err != nil {
		writeErr(w, http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" did not answer")
		return
	}
	data, _ := readBody(resp)
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		if err := g.store.ForgetSnapshot(id); err != nil {
			g.logf("forgetting snapshot %s: %v", id, err)
		}
	}
	relay(w, resp, data)
}
