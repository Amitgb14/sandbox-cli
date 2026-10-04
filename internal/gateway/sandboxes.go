package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	_, f := g.create(r.Context(), p, req, nil)
	f.write(w)
}

// createFail is the answer to a create, as a node or the gateway put it: on
// success the node's 201 and its body, otherwise the refusal.
type createFail struct {
	status int
	body   []byte
	// transient marks a refusal that waiting may cure — the tenant's quota
	// full, no node with room, a node not answering — which a job's run
	// waits out rather than counting as a failed attempt.
	transient bool
}

func (f *createFail) write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.status)
	_, _ = w.Write(f.body)
}

// err is the refusal as a client would have read it.
func (f *createFail) err() error {
	var eb api.ErrorBody
	if json.Unmarshal(f.body, &eb) != nil || eb.Error.Code == "" {
		eb.Error = api.ErrorDetail{Code: api.CodeInternal, Message: strings.TrimSpace(string(f.body))}
	}
	return &api.Error{Status: f.status, Code: eb.Error.Code, Message: eb.Error.Message}
}

func failWith(status int, code, msg string) *createFail {
	body, _ := json.Marshal(api.ErrorBody{Error: api.ErrorDetail{Code: code, Message: msg}})
	return &createFail{status: status, body: body}
}

func transientFail(status int, code, msg string) *createFail {
	f := failWith(status, code, msg)
	f.transient = true
	return f
}

// create is the one create path: POST /v1/sandboxes, and every run of a job
// (jobs.go). It validates, places, holds the quota, stamps the owner labels
// and extra — gateway.* labels only the gateway sets — and records the owner
// before the sandbox is handed back. It returns the sandbox and the response
// to send: the node's 201, or the refusal.
func (g *Gateway) create(ctx context.Context, p Principal, req api.CreateSandboxRequest, extra map[string]string) (api.Sandbox, *createFail) {
	return g.createSpread(ctx, p, req, extra, nil)
}

// createSpread is create, placing the sandbox away from the nodes in spread
// (a node name and how many of the caller's replicas it already holds), as a
// service's replicas are.
func (g *Gateway) createSpread(ctx context.Context, p Principal, req api.CreateSandboxRequest, extra map[string]string, spread map[string]int) (api.Sandbox, *createFail) {
	if req.Name != "" && !spec.ValidName(req.Name) {
		return api.Sandbox{}, failWith(http.StatusBadRequest, api.CodeInvalidRequest, "name "+req.Name+": lowercase letters, digits and dashes, starting with a letter or digit, at most 63")
	}
	if max := spec.MaxLabels - 2 - len(extra); len(req.Labels) > max {
		return api.Sandbox{}, failWith(http.StatusBadRequest, api.CodeInvalidRequest, fmt.Sprintf("labels: at most %d through a gateway, which adds its own", max))
	}
	for _, err := range []error{spec.ValidateLabels(req.Labels), spec.ValidateVolumeMounts(req.Volumes)} {
		if err != nil {
			return api.Sandbox{}, failWith(http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		}
	}
	if _, ok := g.combinedCapabilities(); !ok {
		return api.Sandbox{}, transientFail(http.StatusServiceUnavailable, api.CodeUnavailable, "no node is answering")
	}

	want := Want{Image: req.Image, CPUs: req.CPUs, MemoryMB: req.MemoryMB, DiskMB: req.DiskMB, Spread: spread}
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
	//
	// "Some node has it", not the fleet's combined capabilities, which say a
	// capability only when every node has it: in a mixed fleet that skipped
	// the ownership check, and a create naming another user's volume or
	// snapshot was placed on whichever node, where the one holding it
	// mounted or restored it for them (reproduced in
	// TestMixedFleetVolumesAndSnapshotsStayOwned).
	switch {
	case req.SnapshotID != "" && g.anyNodeHas(api.CapMemorySnapshot):
		o, ok := g.store.SnapshotOwner(req.SnapshotID)
		if !ok || !mayAct(p, o) {
			return api.Sandbox{}, failWith(http.StatusNotFound, api.CodeNotFound, "no such snapshot")
		}
		want.Node = o.Node
		want.Caps = append(want.Caps, api.CapMemorySnapshot)
	case len(req.Volumes) > 0 && g.anyNodeHas(api.CapVolumes):
		for _, m := range req.Volumes {
			o, ok := g.store.VolumeOwner(m.Name)
			if !ok || !mayAct(p, o) {
				return api.Sandbox{}, failWith(http.StatusNotFound, api.CodeNotFound, "no volume named "+m.Name)
			}
			if want.Node != "" && o.Node != want.Node {
				return api.Sandbox{}, failWith(http.StatusConflict, api.CodeConflict, "the volumes named are on different nodes; one sandbox mounts volumes of one node")
			}
			want.Node = o.Node
		}
		want.Caps = append(want.Caps, api.CapVolumes)
	}

	if req.Name != "" {
		key := "sbx\x00" + p.User + "\x00" + p.Tenant + "\x00" + req.Name
		if !g.claim(key) {
			return api.Sandbox{}, failWith(http.StatusConflict, api.CodeConflict, "a sandbox named "+req.Name+" already exists")
		}
		defer g.unclaim(key)
		list, err := g.listSandboxes(ctx, p, nil, false)
		if err != nil {
			var ae *api.Error
			if errors.As(err, &ae) {
				return api.Sandbox{}, failWith(ae.Status, ae.Code, ae.Message)
			}
			return api.Sandbox{}, transientFail(http.StatusBadGateway, api.CodeInternal, "a node did not answer")
		}
		for _, sb := range list {
			if sb.Name == req.Name && sb.State != api.StateTerminated {
				return api.Sandbox{}, failWith(http.StatusConflict, api.CodeConflict, "a sandbox named "+req.Name+" already exists")
			}
		}
	}

	res := api.NodeResources{CPUs: want.CPUs, MemoryMB: want.MemoryMB, DiskMB: want.DiskMB}
	hold, err := g.reserveQuota(p.Tenant, res)
	if err != nil {
		g.metrics.refused.Inc(refusedQuota)
		return api.Sandbox{}, transientFail(http.StatusForbidden, api.CodeRefused, err.Error())
	}
	defer hold.release()

	labels := make(map[string]string, len(req.Labels)+len(extra)+2)
	for k, v := range req.Labels {
		labels[k] = v
	}
	for k, v := range extra {
		labels[k] = v
	}
	labels[LabelOwner] = p.User
	if p.Tenant != "" {
		labels[LabelTenant] = p.Tenant
	}
	req.Labels = labels
	body, err := json.Marshal(req)
	if err != nil {
		return api.Sandbox{}, failWith(http.StatusInternalServerError, api.CodeInternal, "encoding the request failed")
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
			return api.Sandbox{}, &createFail{status: http.StatusConflict, body: conflict}
		}
		if err != nil {
			g.metrics.refused.Inc(refusedCapacity)
			msg := err.Error()
			if want.Node != "" {
				msg = "node " + want.Node + ", which holds what this sandbox needs, is not taking new sandboxes"
			}
			return api.Sandbox{}, transientFail(http.StatusServiceUnavailable, api.CodeUnavailable, msg)
		}
		resp, err := n.do(ctx, http.MethodPost, "/v1/sandboxes", nil, bytes.NewReader(body), "application/json")
		if err != nil {
			n.finishIf(pl, false)
			g.logf("create on %s: %v", n.cfg.Name, err)
			// A node can stop answering between polls and still be marked
			// healthy. When the connection was never made the node never
			// saw the request, so the next node may run it; otherwise it
			// may have, and a second sandbox is not ours to start.
			if neverSent(err) && want.Node == "" {
				tried = append(tried, n.cfg.Name)
				continue
			}
			return api.Sandbox{}, transientFail(http.StatusServiceUnavailable, api.CodeUnavailable, "node "+n.cfg.Name+" did not answer")
		}
		data, err := readBody(resp)
		if err != nil {
			n.finishIf(pl, false)
			return api.Sandbox{}, transientFail(http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" did not answer")
		}
		if resp.StatusCode == http.StatusConflict && req.Name != "" && want.Node == "" {
			n.finishIf(pl, false)
			tried = append(tried, n.cfg.Name)
			conflict = data
			continue
		}
		if resp.StatusCode != http.StatusCreated {
			n.finishIf(pl, false)
			return api.Sandbox{}, &createFail{status: resp.StatusCode, body: data}
		}
		var sb api.Sandbox
		if err := json.Unmarshal(data, &sb); err != nil || !spec.ValidID(sb.ID) {
			n.finishIf(pl, false)
			return api.Sandbox{}, failWith(http.StatusBadGateway, api.CodeInternal, "node "+n.cfg.Name+" answered with no sandbox")
		}
		// Recorded before the caller hears of it: a sandbox the store does
		// not hold is one nobody can reach, so if the record fails the
		// sandbox is taken down rather than left running unowned.
		o := Owner{User: p.User, Tenant: p.Tenant, Node: n.cfg.Name}
		named, names := api.NodeOfID(sb.ID)
		if names && named != n.cfg.Name {
			err = errors.New("the node made an id naming node " + named)
		} else {
			err = hold.record(sb.ID, o, sb.CPUs, sb.MemoryMB, sb.DiskMB)
		}
		if err != nil {
			n.finishIf(pl, false)
			g.logf("create on %s: %v; terminating %s", n.cfg.Name, err, sb.ID)
			if resp, derr := n.do(context.WithoutCancel(ctx), http.MethodDelete, "/v1/sandboxes/"+url.PathEscape(sb.ID), nil, nil, ""); derr == nil {
				resp.Body.Close()
			}
			return api.Sandbox{}, failWith(http.StatusInternalServerError, api.CodeInternal, "the sandbox's owner could not be recorded; it was terminated")
		}
		n.created(pl, sb.ID)
		noteSandbox(ctx, sb.ID, n.cfg.Name)
		return sb, &createFail{status: http.StatusCreated, body: data}
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
	g.metrics.scheduled.Inc(name)
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
			// A node that cannot be connected to is left out, as it is
			// once a poll marks it unhealthy: one node gone between polls
			// does not take every user's listing down with it.
			if neverSent(err) {
				g.logf("listing on %s: %v", n.cfg.Name, err)
				return nil
			}
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
		// Held to its key for as long as it is open, not only now: an
		// attach, a followed output stream or a tunnel lasts as long as its
		// client keeps it (live.go).
		w, r, done := g.track(w, r, p, scope, id, o.Node)
		defer done()
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
			if errors.Is(context.Cause(r.Context()), errAccessRevoked) {
				// Ended before the node answered: say why, as a request
				// made with the key now would be told.
				writeErr(w, http.StatusUnauthorized, api.CodeUnauthorized, errAccessRevoked.Error())
				return
			}
			if r.Context().Err() == nil {
				g.logf("forwarding to %s: %v", n.cfg.Name, err)
			}
			writeUnreachable(w, n)
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
	if _, held := g.store.OwnerOf(id); held {
		g.metrics.terminated.Inc("delete")
	}
	g.roomBack(o.Node, id)
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

// createOpts is what the gateway itself adds to a create of its own: a
// service's replica carries the gateway's labels, which a request may not
// set, and is spread across nodes.
type createOpts struct {
	labels map[string]string
	spread map[string]int
}

// created is a sandbox the gateway made for itself, and the node it is on.
type created struct {
	sb   api.Sandbox
	node string
}

// createError is a create the gateway made for itself that did not happen.
type createError struct{ fail *createFail }

func (e *createError) Error() string { return e.fail.err().Error() }

// refused reports whether the request itself was refused, so trying again
// will not help: a client error that is not a name clash, and not one waiting
// cures (a full quota, no node with room).
func (e *createError) refused() bool {
	st := e.fail.status
	return !e.fail.transient && st >= 400 && st < 500 && st != http.StatusConflict
}

// createFor is the create path for the gateway's own sandboxes (a service's
// replicas): create, with the node the sandbox landed on.
func (g *Gateway) createFor(ctx context.Context, p Principal, req api.CreateSandboxRequest, opts createOpts) (*created, *createError) {
	sb, f := g.createSpread(ctx, p, req, opts.labels, opts.spread)
	if f.status != http.StatusCreated {
		return nil, &createError{fail: f}
	}
	o, _ := g.store.OwnerOf(sb.ID)
	return &created{sb: sb, node: o.Node}, nil
}

// roomBack tells the scheduler that a sandbox the gateway terminated on
// node has given back its room (node.released). Called after the node
// answered the delete with 204, and before the store forgets the sandbox's
// size; a 404 means it ended earlier, and the node's status already says so.
func (g *Gateway) roomBack(node, id string) {
	n := g.nodes.get(node)
	if n == nil {
		return
	}
	if res, ok := g.store.SizeOf(id); ok {
		n.released(id, res)
	}
}
