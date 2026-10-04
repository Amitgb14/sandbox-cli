package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// --- create -------------------------------------------------------------------

func (g *Gateway) createSandbox(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeCreate) {
		return
	}
	var req api.CreateSandboxRequest
	if !decode(w, r, &req) {
		return
	}
	// The owner labels decide who may act on the sandbox; a request that
	// sets one is refused outright, not overwritten, so a client that tried
	// is told. The whole gateway. prefix is kept for the gateway.
	for k := range req.Labels {
		if strings.HasPrefix(k, "gateway.") {
			writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "label "+k+": gateway.* labels are set by the gateway")
			return
		}
	}
	if req.Name != "" && !spec.ValidName(req.Name) {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "name "+req.Name+": lowercase letters, digits and dashes, starting with a letter or digit, at most 63")
		return
	}
	if len(req.Labels) > spec.MaxLabels-2 {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "labels: at most 30 through a gateway, which adds its own two")
		return
	}
	for _, err := range []error{spec.ValidateLabels(req.Labels), spec.ValidateVolumeMounts(req.Volumes)} {
		if err != nil {
			writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
			return
		}
	}
	caps, ok := g.combinedCapabilities()
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, api.CodeInternal, "no node is answering")
		return
	}

	want := Want{Image: req.Image, CPUs: req.CPUs, MemoryMB: req.MemoryMB, DiskMB: req.DiskMB}
	if want.CPUs == 0 {
		want.CPUs = g.cfg.Defaults.CPUs
	}
	if want.MemoryMB == 0 {
		want.MemoryMB = g.cfg.Defaults.MemoryMB
	}
	if want.DiskMB == 0 {
		want.DiskMB = g.cfg.Defaults.DiskMB
	}
	if req.Network != nil {
		want.NetworkMode = req.Network.Mode
		switch req.Network.Mode {
		case api.NetworkAllowlist:
			want.Caps = append(want.Caps, api.CapEgressAllowlist)
		case api.NetworkOpen:
			want.Caps = append(want.Caps, api.CapEgressOpen)
		}
	}
	// A snapshot and a volume live on one node, so the sandbox goes there.
	// Where no node has the capability at all, nothing is pinned: the
	// request goes to a node, which refuses it in its own words.
	switch {
	case req.SnapshotID != "" && caps.Has(api.CapMemorySnapshot):
		o, ok := g.store.SnapshotOwner(req.SnapshotID)
		if !ok || !mayAct(p, o) {
			writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such snapshot")
			return
		}
		want.Node = o.Node
		want.Caps = append(want.Caps, api.CapMemorySnapshot)
	case len(req.Volumes) > 0 && caps.Has(api.CapVolumes):
		for _, m := range req.Volumes {
			o, ok := g.store.VolumeOwner(m.Name)
			if !ok || !mayAct(p, o) {
				writeErr(w, http.StatusNotFound, api.CodeNotFound, "no volume named "+m.Name)
				return
			}
			if want.Node != "" && o.Node != want.Node {
				writeErr(w, http.StatusConflict, api.CodeConflict, "the volumes named are on different nodes; one sandbox mounts volumes of one node")
				return
			}
			want.Node = o.Node
		}
		want.Caps = append(want.Caps, api.CapVolumes)
	}

	if req.Name != "" {
		key := "sbx\x00" + p.User + "\x00" + p.Tenant + "\x00" + req.Name
		if !g.claim(key) {
			writeErr(w, http.StatusConflict, api.CodeConflict, "a sandbox named "+req.Name+" already exists")
			return
		}
		defer g.unclaim(key)
		list, err := g.listSandboxes(r.Context(), p, nil, false)
		if err != nil {
			writeRouteErr(w, err, "")
			return
		}
		for _, sb := range list {
			if sb.Name == req.Name && sb.State != api.StateTerminated {
				writeErr(w, http.StatusConflict, api.CodeConflict, "a sandbox named "+req.Name+" already exists")
				return
			}
		}
	}

	res := api.NodeResources{CPUs: want.CPUs, MemoryMB: want.MemoryMB, DiskMB: want.DiskMB}
	hold, err := g.reserveQuota(p.Tenant, res)
	if err != nil {
		writeErr(w, http.StatusForbidden, api.CodeRefused, err.Error())
		return
	}
	defer hold.release()

	labels := make(map[string]string, len(req.Labels)+2)
	for k, v := range req.Labels {
		labels[k] = v
	}
	labels[LabelOwner] = p.User
	if p.Tenant != "" {
		labels[LabelTenant] = p.Tenant
	}
	req.Labels = labels
	body, err := json.Marshal(req)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "encoding the request failed")
		return
	}

	// A node refusing the name (409, someone else's sandbox there has it)
	// is not the end: names are per user, so the next node is tried. Only an
	// unpinned create can meet that, since a node's other conflicts are about
	// volumes, which pin it.
	var tried []string
	var conflict []byte // the last node's refusal of the name
	for {
		n, pl, err := g.place(want, tried, res)
		if err != nil && conflict != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write(conflict)
			return
		}
		if err != nil {
			msg := err.Error()
			if want.Node != "" {
				msg = "node " + want.Node + ", which holds what this sandbox needs, is not taking new sandboxes"
			}
			writeErr(w, http.StatusServiceUnavailable, api.CodeInternal, msg)
			return
		}
		resp, err := n.do(r.Context(), http.MethodPost, "/v1/sandboxes", nil, bytes.NewReader(body), "application/json")
		if err != nil {
			n.finishIf(pl, false)
			g.logf("create on %s: %v", n.cfg.Name, err)
			writeErr(w, http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" did not answer")
			return
		}
		data, err := readBody(resp)
		if err != nil {
			n.finishIf(pl, false)
			writeErr(w, http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" did not answer")
			return
		}
		if resp.StatusCode == http.StatusConflict && req.Name != "" && want.Node == "" {
			n.finishIf(pl, false)
			tried = append(tried, n.cfg.Name)
			conflict = data
			continue
		}
		if resp.StatusCode != http.StatusCreated {
			n.finishIf(pl, false)
			relay(w, resp, data)
			return
		}
		var sb api.Sandbox
		if err := json.Unmarshal(data, &sb); err != nil || !spec.ValidID(sb.ID) {
			n.finishIf(pl, false)
			writeErr(w, http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" answered with no sandbox")
			return
		}
		// Recorded before the caller hears of it: a sandbox the store does
		// not hold is one nobody can reach, so if the record fails the
		// sandbox is taken down rather than left running unowned.
		o := Owner{User: p.User, Tenant: p.Tenant, Node: n.cfg.Name}
		named, names := api.NodeOfID(sb.ID)
		if names && named != n.cfg.Name {
			err = errors.New("the node made an id naming node " + named)
		} else {
			err = hold.record(sb.ID, o, sb.CPUs, sb.MemoryMB)
		}
		if err != nil {
			n.finishIf(pl, false)
			g.logf("create on %s: %v; terminating %s", n.cfg.Name, err, sb.ID)
			if resp, derr := n.do(context.WithoutCancel(r.Context()), http.MethodDelete, "/v1/sandboxes/"+url.PathEscape(sb.ID), nil, nil, ""); derr == nil {
				resp.Body.Close()
			}
			writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the sandbox's owner could not be recorded; it was terminated")
			return
		}
		n.finishIf(pl, true)
		relay(w, resp, data)
		return
	}
}

// place chooses a node and reserves room on it. When no node can run the
// request, it goes to one anyway, unreserved, to be refused there.
func (g *Gateway) place(want Want, tried []string, res api.NodeResources) (*node, *placement, error) {
	g.schedMu.Lock()
	defer g.schedMu.Unlock()
	want.Exclude = tried
	cands := g.nodes.candidates()
	name, err := Schedule(cands, want)
	if errors.Is(err, errNoCapable) {
		if want.Node != "" {
			name, err = want.Node, nil
		} else if fb, ok := Fallback(cands, tried); ok {
			name, err = fb, nil
		}
		if err == nil {
			if n := g.nodes.get(name); n != nil {
				return n, nil, nil
			}
			err = errNoCandidate
		}
	}
	if err != nil {
		return nil, nil, err
	}
	n := g.nodes.get(name)
	if n == nil {
		return nil, nil, errNoCandidate
	}
	return n, n.reserve(res), nil
}

func (n *node) finishIf(p *placement, created bool) {
	if p != nil {
		n.finish(p, created)
	}
}

// --- list ---------------------------------------------------------------------

func (g *Gateway) listHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	filters := r.URL.Query()["label"]
	for _, l := range filters {
		if k, _, ok := strings.Cut(l, "="); !ok || k == "" {
			writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "label: want key=value")
			return
		}
	}
	list, err := g.listSandboxes(r.Context(), p, filters, p.Can(ScopeAdmin))
	if err != nil {
		writeRouteErr(w, err, ScopeRead)
		return
	}
	writeJSON(w, http.StatusOK, api.SandboxList{Sandboxes: list})
}

// listSandboxes asks every healthy node for the caller's sandboxes and
// merges them, newest first, as a node orders them. The owner label narrows
// what each node sends; the store decides what is returned, so a label on a
// sandbox made some other way reaches no one. all lists every sandbox, for
// an admin.
func (g *Gateway) listSandboxes(ctx context.Context, p Principal, filters []string, all bool) ([]api.Sandbox, error) {
	q := url.Values{}
	for _, f := range filters {
		q.Add("label", f)
	}
	if !all {
		q.Add("label", LabelOwner+"="+p.User)
	}
	var mu sync.Mutex
	out := []api.Sandbox{}
	err := fanOut(ctx, g.nodes.healthy(), func(ctx context.Context, n *node) error {
		var list api.SandboxList
		if err := n.getJSON(ctx, "/v1/sandboxes", q, &list); err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, sb := range list.Sandboxes {
			if !all {
				o, ok := g.ownerOf(sb.ID)
				if !ok || o.Node != n.cfg.Name || o.User != p.User || o.Tenant != p.Tenant {
					continue
				}
			}
			out = append(out, sb)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// --- forwarding ---------------------------------------------------------------

// afterFunc sees a node's response to a forwarded request before the caller
// does; an error turns it into a 502.
type afterFunc func(resp *http.Response, id string, o Owner) error

// forward resolves the sandbox the path names and hands the request to its
// node with the node's token, never the caller's. The path names the
// sandbox by id from here on: a name the node resolved could be someone
// else's sandbox there.
func (g *Gateway) forward(scope string, after afterFunc) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, p Principal) {
		ref := r.PathValue("ref")
		id, o, n, err := g.resolve(r.Context(), p, ref, scope)
		if err != nil {
			writeRouteErr(w, err, scope)
			return
		}
		rest, ok := strings.CutPrefix(r.URL.Path, "/v1/sandboxes/"+ref)
		target := "/v1/sandboxes/" + url.PathEscape(id) + rest
		// The path is forwarded decoded, so a segment that arrived as
		// ..%2F..%2Fsbx_other would reach the node as a walk to a sandbox
		// the router never looked at. Only a path already clean goes on.
		if !ok || path.Clean(target) != target {
			writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such endpoint")
			return
		}
		var modify func(*http.Response) error
		if after != nil {
			modify = func(resp *http.Response) error { return after(resp, id, o) }
		}
		g.proxy(w, r, n, target, modify)
	}
}

// proxy sends r to n at path. It streams both ways — output followed live,
// and the 101 of an attach or a tunnel spliced through — and strips what
// was the caller's: its credential, its cookies, its origin.
func (g *Gateway) proxy(w http.ResponseWriter, r *http.Request, n *node, path string, modify func(*http.Response) error) {
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = n.base.Scheme
			pr.Out.URL.Host = n.base.Host
			pr.Out.URL.Path = strings.TrimSuffix(n.base.Path, "/") + path
			pr.Out.URL.RawPath = ""
			pr.Out.Host = n.base.Host
			for _, h := range []string{"Authorization", "Cookie", "Origin", "Referer"} {
				pr.Out.Header.Del(h)
			}
			if n.token != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+n.token)
			}
		},
		Transport:      n.rt,
		FlushInterval:  -1,
		ModifyResponse: modify,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() == nil {
				g.logf("forwarding to %s: %v", n.cfg.Name, err)
			}
			writeErr(w, http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" did not answer")
		},
	}
	rp.ServeHTTP(w, r)
}

// afterDelete forgets a terminated sandbox. Its owner is kept a while in
// memory, so the caller can still read its final state and its events, and
// delete it again, as on a node.
func (g *Gateway) afterDelete(resp *http.Response, id string, o Owner) error {
	if resp.StatusCode != http.StatusNoContent {
		return nil
	}
	g.tombs.add(id, o)
	if err := g.store.ForgetSandbox(id); err != nil {
		g.logf("forgetting %s: %v", id, err)
	}
	return nil
}

// afterSnapshot records a new snapshot's owner and node, so a sandbox
// started from it goes where it is. A snapshot the store could not record
// is not handed to the caller.
func (g *Gateway) afterSnapshot(resp *http.Response, id string, o Owner) error {
	if resp.StatusCode != http.StatusCreated {
		return nil
	}
	data, err := readBody(resp)
	if err != nil {
		return err
	}
	var snap api.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil || snap.ID == "" {
		return errors.New("the node answered with no snapshot")
	}
	if err := g.store.SetSnapshot(snap.ID, o); err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	resp.ContentLength = int64(len(data))
	return nil
}
