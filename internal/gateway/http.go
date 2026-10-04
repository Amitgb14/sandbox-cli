package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The HTTP front speaks Sandbox API v1, so every client of a node works
// against the gateway with its address and credential changed, and adds the
// gateway's own endpoints (api/types.go lists them).
//
// Of a node's request guard it keeps what a network service needs: a
// credential on every request but the health check, cross-origin requests
// refused, a typed and capped body, unknown fields refused. It drops the
// Host check: that defends a server reached on loopback against DNS
// rebinding, and the gateway is reached by name on a network, behind a key
// no page can hold.

const (
	maxJSONBody = 1 << 20
	maxRawBody  = 64 << 20
)

type handlerFunc func(w http.ResponseWriter, r *http.Request, p Principal)

// Handler returns the gateway's HTTP handler.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	route := func(pattern string, raw bool, h handlerFunc) {
		mux.Handle(pattern, g.observe(g.guard(raw, h)))
	}
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	route("GET /v1/capabilities", false, g.capabilities)
	route("GET /v1/whoami", false, g.whoami)

	route("POST /v1/sandboxes", false, g.createSandbox)
	route("GET /v1/sandboxes", false, g.listHandler)
	// Every route on one sandbox is forwarded to its node once the router
	// has said the caller may; each names the scope it needs. Attach and a
	// tunnel are GETs that write — stdin, signals, bytes to a guest port —
	// so they need create, not read.
	for _, rt := range []struct {
		pattern string
		raw     bool
		scope   string
		after   afterFunc
	}{
		{"GET /v1/sandboxes/{ref}", false, ScopeRead, nil},
		{"PATCH /v1/sandboxes/{ref}", false, ScopeCreate, nil},
		{"DELETE /v1/sandboxes/{ref}", false, ScopeDelete, g.afterDelete},
		{"POST /v1/sandboxes/{ref}/run", false, ScopeCreate, nil},
		{"POST /v1/sandboxes/{ref}/processes", false, ScopeCreate, nil},
		{"GET /v1/sandboxes/{ref}/processes", false, ScopeRead, nil},
		{"GET /v1/sandboxes/{ref}/processes/{pid}", false, ScopeRead, nil},
		{"GET /v1/sandboxes/{ref}/processes/{pid}/output", false, ScopeRead, nil},
		{"POST /v1/sandboxes/{ref}/processes/{pid}/stdin", true, ScopeCreate, nil},
		{"POST /v1/sandboxes/{ref}/processes/{pid}/signal", false, ScopeCreate, nil},
		{"GET /v1/sandboxes/{ref}/processes/{pid}/attach", false, ScopeCreate, nil},
		{"POST /v1/sandboxes/{ref}/suspend", false, ScopeCreate, nil},
		{"POST /v1/sandboxes/{ref}/resume", false, ScopeCreate, nil},
		{"POST /v1/sandboxes/{ref}/snapshots", false, ScopeCreate, g.afterSnapshot},
		{"GET /v1/sandboxes/{ref}/tunnel", false, ScopeCreate, nil},
		{"GET /v1/sandboxes/{ref}/files", false, ScopeRead, nil},
		{"PUT /v1/sandboxes/{ref}/files", true, ScopeCreate, nil},
		{"DELETE /v1/sandboxes/{ref}/files", false, ScopeCreate, nil},
		{"GET /v1/sandboxes/{ref}/dirs", false, ScopeRead, nil},
		{"GET /v1/sandboxes/{ref}/events", false, ScopeRead, nil},
	} {
		route(rt.pattern, rt.raw, g.forward(rt.scope, rt.after))
	}
	route("POST /v1/sandboxes/{ref}/ssh-access", false, g.sshAccess)

	route("GET /v1/snapshots", false, g.listSnapshots)
	route("DELETE /v1/snapshots/{id}", false, g.deleteSnapshot)
	route("POST /v1/volumes", false, g.createVolume)
	route("GET /v1/volumes", false, g.listVolumes)
	route("DELETE /v1/volumes/{name}", false, g.deleteVolume)

	route("GET /v1/ssh", false, g.sshEndpoint)
	route("POST /v1/ssh-keys", false, g.addSSHKey)
	route("GET /v1/ssh-keys", false, g.listSSHKeys)
	route("DELETE /v1/ssh-keys/{id}", false, g.removeSSHKey)

	route("POST /v1/admin/keys", false, g.adminCreateKey)
	route("GET /v1/admin/keys", false, g.adminListKeys)
	route("DELETE /v1/admin/keys/{id}", false, g.adminRevokeKey)
	route("GET /v1/admin/ssh-keys", false, g.adminListSSHKeys)
	route("DELETE /v1/admin/ssh-keys/{id}", false, g.adminRemoveSSHKey)
	route("GET /v1/admin/nodes", false, g.adminListNodes)
	route("POST /v1/admin/nodes", false, g.adminAddNode)
	route("DELETE /v1/admin/nodes/{name}", false, g.adminRemoveNode)
	route("POST /v1/admin/nodes/{name}/cordon", false, g.adminCordon)
	route("POST /v1/admin/nodes/{name}/drain", false, g.adminDrain)
	route("GET /v1/admin/lost", false, g.adminLost)
	route("GET /v1/admin/audit", false, g.adminAudit)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such endpoint")
	})
	return mux
}

// guard applies the origin check, the credential, the content type and the
// body cap, in that order: a request refused for its origin learns nothing
// about its credential.
func (g *Gateway) guard(raw bool, next handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !g.originAllowed(r) {
			writeErr(w, http.StatusForbidden, api.CodeForbiddenOrigin, "requests from this origin are not accepted")
			return
		}
		p, ok := g.authenticate(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeErr(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing or unknown API key")
			return
		}
		notePrincipal(r.Context(), p)
		if err := checkContentType(r, raw); err != nil {
			writeErr(w, http.StatusUnsupportedMediaType, api.CodeInvalidRequest, err.Error())
			return
		}
		limit := int64(maxJSONBody)
		if raw {
			limit = maxRawBody
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next(w, r, p)
	})
}

// originAllowed refuses a browser's cross-origin request: a page the user
// visits must not drive the API with whatever the browser holds. A client
// that is not a browser sends no Origin.
func (g *Gateway) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || slices.Contains(g.cfg.CORSOrigins, origin) {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// authenticate turns the bearer key into a principal. The key's secret is
// never logged; its id is what names it.
func (g *Gateway) authenticate(r *http.Request) (Principal, bool) {
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || secret == "" {
		return Principal{}, false
	}
	k, ok := g.store.KeyBySecret(secret)
	if !ok {
		return Principal{}, false
	}
	return Principal{User: k.User, Tenant: k.Tenant, KeyID: k.ID, Scopes: k.Scopes}, true
}

func checkContentType(r *http.Request, raw bool) error {
	if r.ContentLength == 0 {
		return nil
	}
	want := "application/json"
	if raw {
		want = "application/octet-stream"
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return fmt.Errorf("a request body requires Content-Type: %s", want)
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return fmt.Errorf("unparseable Content-Type %q: %w", ct, err)
	}
	if mt != want {
		return fmt.Errorf("Content-Type %q is not supported here; use %s", mt, want)
	}
	return nil
}

// need refuses a principal without scope.
func need(w http.ResponseWriter, p Principal, scope string) bool {
	if p.Can(scope) {
		return true
	}
	writeErr(w, http.StatusForbidden, api.CodeRefused, "this API key does not have the "+scope+" scope")
	return false
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "request body: "+err.Error())
		return false
	}
	if dec.More() {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "request body: more than one JSON value")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, api.ErrorBody{Error: api.ErrorDetail{Code: code, Message: msg}})
}

// writeRouteErr maps the router's errors to API errors. Not found is the
// answer both for a sandbox that does not exist and for one that is someone
// else's.
func writeRouteErr(w http.ResponseWriter, err error, scope string) {
	var ae *api.Error
	switch {
	case errors.Is(err, ErrUnauthenticated):
		writeErr(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing or unknown API key")
	case errors.Is(err, ErrForbidden):
		writeErr(w, http.StatusForbidden, api.CodeRefused, "this API key does not have the "+scope+" scope")
	case errors.Is(err, ErrNotFound):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such sandbox")
	case errors.Is(err, ErrNodeDown):
		// Unavailable, not a state: the node may come back with the
		// sandbox running (lost.go).
		writeErr(w, http.StatusServiceUnavailable, api.CodeUnavailable, ErrNodeDown.Error())
	case errors.As(err, &ae):
		writeErr(w, ae.Status, ae.Code, ae.Message)
	case errors.As(err, new(*net.OpError)):
		// A node stopped answering before a poll noticed: unavailable,
		// as once it has.
		writeErr(w, http.StatusServiceUnavailable, api.CodeUnavailable, "a node did not answer")
	default:
		writeErr(w, http.StatusBadGateway, api.CodeInternal, "a node did not answer")
	}
}

// writeUnreachable answers for a node a request could not reach:
// unavailable, as for a node the gateway already knows is down. A node not
// answering is not a bug, and the client may try again.
func writeUnreachable(w http.ResponseWriter, n *node) {
	writeErr(w, http.StatusServiceUnavailable, api.CodeUnavailable, "node "+n.cfg.Name+" did not answer")
}

// neverSent reports whether a round trip's error means no connection to the
// node was made, so the node cannot have acted on the request.
func neverSent(err error) bool {
	var oe *net.OpError
	return errors.As(err, &oe) && oe.Op == "dial"
}

// relay writes a node's response as it came.
func relay(w http.ResponseWriter, resp *http.Response, body []byte) {
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// readBody reads a node's response body, bounded.
func readBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// fanOut calls fn on every healthy node at once and returns the first error.
func fanOut(ctx context.Context, nodes []*node, fn func(context.Context, *node) error) error {
	errs := make(chan error, len(nodes))
	for _, n := range nodes {
		go func() { errs <- fn(ctx, n) }()
	}
	var first error
	for range nodes {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
	}
	return first
}
