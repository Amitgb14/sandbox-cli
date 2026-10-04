package gateway

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A key is checked when a request arrives. Most requests are over before a
// revocation could matter, but a forwarded one can last as long as its
// client keeps it: an attached terminal, a followed output stream (logs
// --follow), a tunnel, a run waiting on its command. Each of those went on
// after its key was revoked, for hours if the client liked, because nothing
// looked at the key again. So every forwarded request is registered here,
// with the principal and the scope it was resolved with, for as long as it
// is open; and on every revocation (accessChanged) and every
// AccessRecheckInterval, each is held to its key again. One whose key is
// revoked, gone from the store, or no longer carries the scope is ended:
// its context is cancelled with errAccessRevoked, which cancels the request
// to the node (and, for an upgraded connection, makes the reverse proxy
// close the node's side), and a connection already taken over is closed on
// the client's side too, so both ends see it end.
//
// Only forwarded requests are registered. The gateway's own endpoints
// answer from the store and are short; cancelling one of them part way — a
// create between the node's answer and the owner record — would do harm a
// revocation does not need.

// errAccessRevoked is the cause a live request's context ends with.
var errAccessRevoked = errors.New("the API key this request was made with was revoked")

// liveReq is one open forwarded request.
type liveReq struct {
	p      Principal
	scope  string
	route  string // the route pattern, for the log and the record
	remote string
	id     string // the sandbox
	node   string
	cancel context.CancelCauseFunc

	mu    sync.Mutex
	conn  net.Conn // the client's connection, once the proxy has taken it over
	ended bool
}

// liveRequests is every open forwarded request, by the id of the key it
// was made with.
type liveRequests struct {
	mu    sync.Mutex
	byKey map[string]map[*liveReq]struct{}
}

func newLiveRequests() *liveRequests {
	return &liveRequests{byKey: map[string]map[*liveReq]struct{}{}}
}

func (l *liveRequests) add(lr *liveReq) {
	l.mu.Lock()
	defer l.mu.Unlock()
	set := l.byKey[lr.p.KeyID]
	if set == nil {
		set = map[*liveReq]struct{}{}
		l.byKey[lr.p.KeyID] = set
	}
	set[lr] = struct{}{}
}

func (l *liveRequests) remove(lr *liveReq) {
	l.mu.Lock()
	defer l.mu.Unlock()
	set := l.byKey[lr.p.KeyID]
	delete(set, lr)
	if len(set) == 0 {
		delete(l.byKey, lr.p.KeyID)
	}
}

func (l *liveRequests) snapshot() []*liveReq {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*liveReq
	for _, set := range l.byKey {
		for lr := range set {
			out = append(out, lr)
		}
	}
	return out
}

// len is how many requests are open.
func (l *liveRequests) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, set := range l.byKey {
		n += len(set)
	}
	return n
}

// track registers a forwarded request until done is called, and returns
// the request and writer to serve it with: the request's context is ended
// when its key loses access, and the writer remembers a connection the
// proxy takes over, so that it can be closed then.
func (g *Gateway) track(w http.ResponseWriter, r *http.Request, p Principal, scope, id, node string) (http.ResponseWriter, *http.Request, func()) {
	ctx, cancel := context.WithCancelCause(r.Context())
	lr := &liveReq{p: p, scope: scope, route: r.Pattern, remote: remoteIP(r.RemoteAddr), id: id, node: node, cancel: cancel}
	g.live.add(lr)
	done := func() {
		g.live.remove(lr)
		cancel(nil)
	}
	return &liveWriter{ResponseWriter: w, lr: lr}, r.WithContext(ctx), done
}

// recheckLive holds every open forwarded request to its key and ends those
// whose key no longer allows it, returning how many. It reads the store's
// keys once, and nothing when no request is open.
func (g *Gateway) recheckLive() int {
	open := g.live.snapshot()
	if len(open) == 0 {
		return 0
	}
	keys := map[string]Key{}
	for _, k := range g.store.Keys() {
		keys[k.ID] = k
	}
	n := 0
	for _, lr := range open {
		if why := liveLostAccess(lr, keys); why != "" {
			if g.endLive(lr, why) {
				n++
			}
		}
	}
	return n
}

// liveLostAccess says why lr may no longer stay open, or "".
func liveLostAccess(lr *liveReq, keys map[string]Key) string {
	k, ok := keys[lr.p.KeyID]
	switch {
	case !ok:
		return "its API key is no longer in the store"
	case k.Revoked:
		return "its API key was revoked"
	case k.User != lr.p.User || k.Tenant != lr.p.Tenant:
		return "its API key no longer names the same user"
	case lr.scope != "" && !(Principal{Scopes: k.Scopes}).Can(lr.scope):
		return "its API key no longer holds " + lr.scope
	}
	return ""
}

// endLive ends lr, once, and records it by key id: never the secret, which
// the gateway does not hold. It reports whether this call ended it.
func (g *Gateway) endLive(lr *liveReq, why string) bool {
	lr.mu.Lock()
	if lr.ended {
		lr.mu.Unlock()
		return false
	}
	lr.ended = true
	c := lr.conn
	lr.mu.Unlock()
	p := lr.p
	g.logf("%s %s by key %s: ended: %s", lr.route, lr.id, p.KeyID, why)
	g.audit.write(api.AuditEntry{
		Kind: "api", Action: "api.revoked", KeyID: p.KeyID, User: p.User, Tenant: p.Tenant,
		Remote: lr.remote, Sandbox: lr.id, Node: lr.node, Target: lr.route, Result: "closed",
	})
	lr.cancel(errAccessRevoked)
	if c != nil {
		// The proxy closes the node's side when the context ends; this is
		// the client's, which the proxy would close only once its copy from
		// the node returned.
		_ = c.Close()
	}
	return true
}

// liveWriter is a forwarded request's writer: it passes Flush and Hijack
// through, and keeps the connection Hijack hands over.
type liveWriter struct {
	http.ResponseWriter
	lr *liveReq
}

func (w *liveWriter) Flush() { _ = http.NewResponseController(w.ResponseWriter).Flush() }

func (w *liveWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return c, rw, err
	}
	w.lr.mu.Lock()
	w.lr.conn = c
	ended := w.lr.ended
	w.lr.mu.Unlock()
	if ended {
		// Ended between the node's 101 and this: close it now.
		_ = c.Close()
	}
	return c, rw, nil
}

func (w *liveWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
