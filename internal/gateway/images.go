package gateway

import (
	"net/http"
	"sort"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// listImages is GET /v1/fleet/images: the images built on the nodes a
// new sandbox can go to, from each node's last status (NodeStatus.Images and
// the images it keeps a pool of). A sandbox from one of them starts without a
// pull. Read-only, for anyone who may list sandboxes: installing and removing
// images is the gateway's admin's (images_admin.go), and the fleet does not
// claim the images capability, so a client offers it only to an admin.
func (g *Gateway) listImages(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	seen := map[string]bool{}
	for _, c := range g.nodes.candidates() {
		if c.Status.Cordoned {
			continue
		}
		for _, img := range c.Status.Images {
			seen[img] = true
		}
		for img := range c.Status.Pooled {
			seen[img] = true
		}
	}
	out := api.ImageList{Images: []api.Image{}}
	for img := range seen {
		if img != "" {
			out.Images = append(out.Images, api.Image{Image: img, State: "installed"})
		}
	}
	sort.Slice(out.Images, func(i, j int) bool { return out.Images[i].Image < out.Images[j].Image })
	writeJSON(w, http.StatusOK, out)
}
