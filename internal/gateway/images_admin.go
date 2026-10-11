package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Images on the nodes, for a gateway's admin.
//
// Each node keeps the images its sandboxes start from (sandboxd's GET, POST
// and DELETE /v1/images). These routes are those, once for the fleet, so an
// admin downloads an image ahead of its first sandbox, or frees a disk,
// without going to each node:
//
//   - The gateway asks every healthy node and answers in sandboxd's own
//     shape, one Image per reference, with Image.Nodes saying where it is
//     and in what state, so a client that shows a node's images shows the
//     fleet's.
//   - An install goes to every node that takes new sandboxes (healthy, not
//     cordoned), and each node's own policy decides whether it may pull the
//     image: the gateway carries the request, never a permission a node
//     would not give.
//   - A removal is all or nothing as far as the gateway can tell: it is
//     refused, removing nothing, while any node still needs the image — a
//     sandbox starts from it, it is the node's default or pooled image, or
//     it is being installed — and then goes to every node that has it.
//   - A node that does not manage images (501) is left out, as it is from
//     sandboxd's own listing.
//
// Only an admin: an image on a node is disk every tenant's sandboxes share.
// Everyone else reads what the nodes have built through GET
// /v1/fleet/images.

// nodeImageTimeout bounds one node's answer to a listing, an install's start
// or a removal.
const nodeImageTimeout = 30 * time.Second

// nodeImages is one node's answer to GET /v1/images.
type nodeImages struct {
	node   *node
	images []api.Image
	err    error
}

// imagesOfNodes asks every healthy node for its images, in parallel. A node
// that does not manage images is left out.
func (g *Gateway) imagesOfNodes(ctx context.Context) []nodeImages {
	nodes := g.nodes.healthy()
	out := make([]nodeImages, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, nodeImageTimeout)
			defer cancel()
			var l api.ImageList
			err := n.getJSON(c, "/v1/images", nil, &l)
			out[i] = nodeImages{node: n, images: l.Images, err: err}
		}()
	}
	wg.Wait()
	kept := out[:0]
	for _, ni := range out {
		if api.IsCode(ni.err, api.CodeUnsupported) {
			continue
		}
		if ni.err != nil {
			g.logf("images of node %s: %v", ni.node.cfg.Name, ni.err)
		}
		kept = append(kept, ni)
	}
	return kept
}

// aggregateImages is the nodes' images as one list, one entry per
// reference: installing where any node is installing it, failed where one
// failed and none is installing, else installed.
func aggregateImages(lists []nodeImages) []api.Image {
	by := map[string]*api.Image{}
	for _, ni := range lists {
		if ni.err != nil {
			continue
		}
		for _, img := range ni.images {
			a := by[img.Image]
			if a == nil {
				a = &api.Image{Image: img.Image, State: api.ImageInstalled, Digest: img.Digest}
				by[img.Image] = a
			}
			a.Nodes = append(a.Nodes, api.ImageNode{Node: ni.node.cfg.Name, State: img.State, Error: img.Error})
			a.Bytes = max(a.Bytes, img.Bytes)
			a.InUse += img.InUse
			a.Default = a.Default || img.Default
			a.Pooled = a.Pooled || img.Pooled
			if img.InstalledAt != nil && (a.InstalledAt == nil || img.InstalledAt.Before(*a.InstalledAt)) {
				t := *img.InstalledAt
				a.InstalledAt = &t
			}
			switch {
			case img.State == api.ImageInstalling:
				a.State, a.Error = api.ImageInstalling, ""
				if img.Progress != nil {
					p := *img.Progress
					a.Progress = &p
				}
			case img.State == api.ImageFailed && a.State != api.ImageInstalling:
				a.State = api.ImageFailed
				a.Error = ni.node.cfg.Name + ": " + img.Error
			}
		}
	}
	out := make([]api.Image, 0, len(by))
	for _, a := range by {
		sort.Slice(a.Nodes, func(i, j int) bool { return a.Nodes[i].Node < a.Nodes[j].Node })
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Image < out[j].Image })
	return out
}

// adminListImages is GET /v1/admin/images.
func (g *Gateway) adminListImages(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	writeJSON(w, http.StatusOK, api.ImageList{Images: aggregateImages(g.imagesOfNodes(r.Context()))})
}

// sendToNode sends one image request to n and decodes a 2xx answer into
// out; anything else is the node's *api.Error.
func sendToNode(ctx context.Context, n *node, method string, query url.Values, body any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, nodeImageTimeout)
	defer cancel()
	var rd io.Reader
	ct := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd, ct = bytes.NewReader(b), "application/json"
	}
	resp, err := n.do(ctx, method, "/v1/images", query, rd, ct)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return readAPIError(resp)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// nodeErr writes err, from node name, as the gateway's answer: the node's
// own status and code where it said one, else 502; then says the rest.
func nodeErr(w http.ResponseWriter, name string, err error, rest string) {
	var e *api.Error
	if errors.As(err, &e) {
		writeErr(w, e.Status, e.Code, name+": "+e.Message+rest)
		return
	}
	writeErr(w, http.StatusBadGateway, api.CodeUnavailable, name+": did not answer"+rest)
}

// adminInstallImage is POST /v1/admin/images: every node that takes new
// sandboxes installs the image, as its own policy allows.
func (g *Gateway) adminInstallImage(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	var req api.InstallImageRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Image) == "" {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "image: a reference is needed")
		return
	}
	var targets []*node
	for _, n := range g.nodes.healthy() {
		if c, ok := n.candidate(); ok && !c.Status.Cordoned {
			targets = append(targets, n)
		}
	}
	if len(targets) == 0 {
		writeErr(w, http.StatusServiceUnavailable, api.CodeUnavailable, "no node takes new sandboxes")
		return
	}
	type result struct {
		img api.Image
		err error
	}
	results := make([]result, len(targets))
	var wg sync.WaitGroup
	for i, n := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var img api.Image
			err := sendToNode(r.Context(), n, http.MethodPost, nil, req, &img)
			results[i] = result{img, err}
		}()
	}
	wg.Wait()
	out := api.Image{Image: req.Image}
	var first error
	firstNode := ""
	ok := 0
	for i, res := range results {
		name := targets[i].cfg.Name
		if res.err != nil {
			if first == nil {
				first, firstNode = res.err, name
			}
			msg := res.err.Error()
			var e *api.Error
			if errors.As(res.err, &e) {
				msg = e.Message
			}
			out.Nodes = append(out.Nodes, api.ImageNode{Node: name, Error: msg})
			continue
		}
		ok++
		out.Nodes = append(out.Nodes, api.ImageNode{Node: name, State: res.img.State})
		if out.State == "" || res.img.State == api.ImageInstalling {
			out.State, out.Progress = res.img.State, res.img.Progress
		}
		out.Default = out.Default || res.img.Default
		out.Pooled = out.Pooled || res.img.Pooled
	}
	if ok == 0 {
		nodeErr(w, firstNode, first, "")
		return
	}
	g.logf("image %s: install asked of %d nodes by %s", req.Image, ok, p.KeyID)
	writeJSON(w, http.StatusAccepted, out)
}

// adminRemoveImage is DELETE /v1/admin/images?image=REF.
func (g *Gateway) adminRemoveImage(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	ref := r.URL.Query().Get("image")
	if strings.TrimSpace(ref) == "" {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "image: a reference is needed (?image=REF)")
		return
	}
	lists := g.imagesOfNodes(r.Context())
	var holders []*node
	for _, ni := range lists {
		if ni.err != nil {
			// What a node that did not answer has, the gateway cannot say;
			// removing from the others would leave the fleet half done.
			writeErr(w, http.StatusServiceUnavailable, api.CodeUnavailable, ni.node.cfg.Name+": did not list its images; try again")
			return
		}
		for _, img := range ni.images {
			if img.Image != ref {
				continue
			}
			name := ni.node.cfg.Name
			switch {
			case img.Default || img.Pooled:
				writeErr(w, http.StatusConflict, api.CodeConflict, fmt.Sprintf("%s: %s is the node's default or pooled image; change its policy to remove it", name, ref))
				return
			case img.InUse > 0:
				writeErr(w, http.StatusConflict, api.CodeConflict, fmt.Sprintf("%s: %s is what a running or suspended sandbox there starts from", name, ref))
				return
			case img.State == api.ImageInstalling:
				writeErr(w, http.StatusConflict, api.CodeConflict, fmt.Sprintf("%s: %s is being installed", name, ref))
				return
			}
			holders = append(holders, ni.node)
		}
	}
	if len(holders) == 0 {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, ref+" is on no node")
		return
	}
	out := api.RemovedImage{Image: ref}
	var failed []string
	var first error
	firstNode := ""
	for _, n := range holders {
		var rm api.RemovedImage
		if err := sendToNode(r.Context(), n, http.MethodDelete, url.Values{"image": {ref}}, nil, &rm); err != nil && !api.IsCode(err, api.CodeNotFound) {
			if first == nil {
				first, firstNode = err, n.cfg.Name
			}
			failed = append(failed, n.cfg.Name)
			continue
		}
		out.FreedBytes += rm.FreedBytes
	}
	g.logf("image %s: removed from %d of %d nodes by %s", ref, len(holders)-len(failed), len(holders), p.KeyID)
	if first != nil {
		nodeErr(w, firstNode, first, fmt.Sprintf(" (not removed from %s; removed from any other)", strings.Join(failed, ", ")))
		return
	}
	writeJSON(w, http.StatusOK, out)
}
