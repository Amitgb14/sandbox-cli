package gateway

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// updateHandler is PATCH /v1/sandboxes/{ref}, read rather than forwarded
// as it came, for the two fields a node cannot judge alone.
//
// Labels are replaced whole, and the node would replace the gateway's own
// with them: drop gateway.owner and the sandbox vanishes from its owner's
// list and name lookups; set it and a client is writing the record the
// gateway keeps ownership in. So a client's gateway.* label is refused, as
// at create — unless it is one the sandbox already has, unchanged, which is
// what a client that read the sandbox and sends its labels back holds — and
// the sandbox's own gateway.* labels are put back on whatever it sends.
//
// A name is unique per user, across every node, where a node knows only its
// own sandboxes. It is checked against the caller's sandboxes everywhere,
// under the same claim a create takes, so a rename and a create cannot both
// take one name.
func (g *Gateway) updateHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	id, o, n, err := g.resolve(r.Context(), p, r.PathValue("ref"), ScopeCreate)
	if err != nil {
		writeRouteErr(w, err, ScopeCreate)
		return
	}
	var req api.UpdateSandboxRequest
	if !decode(w, r, &req) {
		return
	}
	w, r, done := g.track(w, r, p, ScopeCreate, id, o.Node)
	defer done()
	ctx := r.Context()

	// A lifetime is the operator's limit on the user, so only an admin may
	// lengthen one; the owner may bring its end nearer, or give one to a
	// sandbox that has none, which only narrows what they hold. Through
	// the node alone the owner could lengthen it to the node's limit, past
	// the policy the gateway holds them to.
	if req.LifetimeSecs != nil && !p.Can(ScopeAdmin) {
		cur, err := n.client.Sandbox(ctx, id)
		if err != nil {
			writeNodeErr(w, err)
			return
		}
		if cur.LifetimeSecs > 0 && *req.LifetimeSecs > cur.LifetimeSecs {
			writeErr(w, http.StatusForbidden, api.CodeRefused, "a sandbox's lifetime can only be shortened; a longer one comes from your policy, which an admin sets")
			return
		}
	}

	if req.Labels != nil {
		cur, err := n.client.Sandbox(ctx, id)
		if err != nil {
			writeNodeErr(w, err)
			return
		}
		merged := map[string]string{}
		for k, v := range *req.Labels {
			if strings.HasPrefix(k, "gateway.") {
				if have, ok := cur.Labels[k]; !ok || have != v {
					writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "label "+k+": gateway.* labels are set by the gateway")
					return
				}
				continue
			}
			merged[k] = v
		}
		for k, v := range cur.Labels {
			if strings.HasPrefix(k, "gateway.") {
				merged[k] = v
			}
		}
		req.Labels = &merged
	}

	if req.Name != nil && *req.Name != "" {
		name := *req.Name
		key := "sbx\x00" + p.User + "\x00" + p.Tenant + "\x00" + name
		if !g.claim(key) {
			writeErr(w, http.StatusConflict, api.CodeConflict, "a sandbox named "+name+" already exists")
			return
		}
		defer g.unclaim(key)
		list, err := g.listSandboxes(ctx, p, nil, false)
		if err != nil {
			writeNodeErr(w, err)
			return
		}
		for _, sb := range list {
			if sb.ID != id && sb.Name == name && sb.State != api.StateTerminated {
				writeErr(w, http.StatusConflict, api.CodeConflict, "a sandbox named "+name+" already exists")
				return
			}
		}
	}

	// A new network policy may name allowlist groups, which the node does
	// not know: they become hosts here, as at create (egress_groups.go).
	if f := g.applyGroups(ctx, &req.Network); f != nil {
		f.write(w)
		return
	}

	if f := g.resolveExportSecrets(ctx, p, req.MetricsExport); f != nil {
		f.write(w)
		return
	}

	sb, err := n.client.UpdateSandbox(ctx, id, req)
	if err != nil {
		var ae *api.Error
		if req.Name != nil && errors.As(err, &ae) && ae.Code == api.CodeConflict && strings.HasPrefix(ae.Message, "a sandbox named ") {
			// Not the caller's own sandbox — those were checked above — but
			// another user's on the same node, which a node keeps names
			// unique among. Said without naming it.
			writeErr(w, http.StatusConflict, api.CodeConflict, "that name cannot be used for this sandbox; choose another")
			return
		}
		writeNodeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sb)
}

// writeNodeErr passes on a node's API error as it was, and anything else as
// the node not answering.
func writeNodeErr(w http.ResponseWriter, err error) {
	var ae *api.Error
	if errors.As(err, &ae) {
		writeErr(w, ae.Status, ae.Code, ae.Message)
		return
	}
	writeErr(w, http.StatusBadGateway, api.CodeInternal, "the sandbox's node did not answer")
}
