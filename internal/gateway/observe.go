package gateway

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// observe wraps every guarded route: it counts the request by its route
// pattern and status, times a create, and writes the audit entry of an
// authenticated one. Both happen when the status is decided — the first
// WriteHeader, Write or Hijack — not when the handler returns, because an
// attached terminal or a followed output stream can last for hours, and an
// audit record of it written only at its end is no record while it runs.
//
// The handler fills in what only it learns, through the reqInfo in the
// request's context: the guard the principal, the router the sandbox and
// its node.

type reqInfo struct {
	mu        sync.Mutex
	principal Principal
	sandbox   string
	node      string
}

type reqInfoKey struct{}

func infoFrom(ctx context.Context) *reqInfo {
	ri, _ := ctx.Value(reqInfoKey{}).(*reqInfo)
	return ri
}

// noteSandbox records the sandbox a request acted on, for its audit entry.
func noteSandbox(ctx context.Context, id, node string) {
	if ri := infoFrom(ctx); ri != nil {
		ri.mu.Lock()
		ri.sandbox, ri.node = id, node
		ri.mu.Unlock()
	}
}

func notePrincipal(ctx context.Context, p Principal) {
	if ri := infoFrom(ctx); ri != nil {
		ri.mu.Lock()
		ri.principal = p
		ri.mu.Unlock()
	}
}

const createPattern = "POST /v1/sandboxes"

func (g *Gateway) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ri := &reqInfo{}
		r = r.WithContext(context.WithValue(r.Context(), reqInfoKey{}, ri))
		ow := &observedWriter{ResponseWriter: w}
		start := time.Now()
		ow.decided = func(code int) { g.requestDone(r, ri, code, time.Since(start)) }
		next.ServeHTTP(ow, r)
		ow.decide(http.StatusOK)
	})
}

func (g *Gateway) requestDone(r *http.Request, ri *reqInfo, code int, took time.Duration) {
	route := r.Pattern
	if route == "" {
		route = "other"
	}
	g.metrics.requests.Inc(route, strconv.Itoa(code))
	if route == createPattern && code == http.StatusCreated {
		g.metrics.created.Inc()
		g.metrics.createLat.Observe(took.Seconds())
	}
	ri.mu.Lock()
	p, sandbox, node := ri.principal, ri.sandbox, ri.node
	ri.mu.Unlock()
	if p.User == "" {
		// Not authenticated: there is no one to record, and the attempt's
		// count is in the metrics.
		return
	}
	g.audit.write(api.AuditEntry{
		Kind: "api", Action: route, KeyID: p.KeyID, User: p.User, Tenant: p.Tenant,
		Remote: remoteIP(r.RemoteAddr), Sandbox: sandbox, Node: node, Target: target(r), Status: code,
	})
}

// target is what a route not about a sandbox names in its path. Only an
// authenticated request gets here, and the value is cut short: a path
// segment is the caller's text.
func target(r *http.Request) string {
	for _, k := range []string{"name", "id"} {
		if v := r.PathValue(k); v != "" {
			if len(v) > 128 {
				v = v[:128]
			}
			return v
		}
	}
	return ""
}

func remoteIP(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// observedWriter reports the status once. It passes Flush and Hijack
// through, which the reverse proxy needs for a followed stream and for an
// attach or a tunnel.
type observedWriter struct {
	http.ResponseWriter
	once    sync.Once
	decided func(int)
}

func (w *observedWriter) decide(code int) { w.once.Do(func() { w.decided(code) }) }

func (w *observedWriter) WriteHeader(code int) {
	if code >= 200 || code == http.StatusSwitchingProtocols {
		w.decide(code)
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *observedWriter) Write(b []byte) (int, error) {
	w.decide(http.StatusOK)
	return w.ResponseWriter.Write(b)
}

func (w *observedWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *observedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("the connection cannot be taken over")
	}
	c, rw, err := h.Hijack()
	if err == nil {
		w.decide(http.StatusSwitchingProtocols)
	}
	return c, rw, err
}

func (w *observedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
