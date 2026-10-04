package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// The HTTP router is the gateway's ingress for public services: a request
// whose Host names a service goes to one of its healthy replicas, round
// robin, through the replica's node's tunnel to the service's port. It is
// served on its own listener (sandbox-gateway serve --router-listen), never
// beside the API, and asks for no credential: a public service is public.
//
// Naming, under one wildcard name (*.DOMAIN, one certificate):
//
//	<service>.DOMAIN            a service of the default tenant
//	<service>--<tenant>.DOMAIN  a service of tenant <tenant>
//
// It cannot collide across tenants: a service name never holds "--", so the
// first "--" in the label always ends the service's name, and the rest is
// the tenant exactly. A tenant that is not itself a DNS label (tenants may
// hold capitals, dots and @) has no name here, and its services cannot be
// made public. Names are per tenant, not per user, because that is the
// unit a hostname can carry; two users of one tenant cannot both have a
// public "web".

// RouterConfig is where the router serves, for the URL a service is shown
// with. Domain is what the Host must end in.
type RouterConfig struct {
	Domain string // apps.example.com
	Scheme string // https (default) or http, as clients reach it
	Port   int    // the port clients reach it on; 0 is the scheme's own
}

var tenantLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// routerLabel is the DNS label a service is routed by.
func routerLabel(name, tenant string) (string, bool) {
	if tenant == "" {
		return name, true
	}
	l := name + "--" + tenant
	if !tenantLabelRE.MatchString(tenant) || len(l) > 63 {
		return "", false
	}
	return l, true
}

// routerURL is where the router serves a service, or "" without a router.
func (g *Gateway) routerURL(name, tenant string) string {
	rc := g.cfg.Router
	l, ok := routerLabel(name, tenant)
	if rc.Domain == "" || !ok {
		return ""
	}
	scheme := rc.Scheme
	if scheme == "" {
		scheme = "https"
	}
	host := l + "." + rc.Domain
	if rc.Port != 0 && !(scheme == "https" && rc.Port == 443) && !(scheme == "http" && rc.Port == 80) {
		host += ":" + strconv.Itoa(rc.Port)
	}
	return scheme + "://" + host
}

// RouterHandler is the router: serve it on its own listener.
func (g *Gateway) RouterHandler() http.Handler {
	domain := strings.ToLower(strings.TrimSuffix(g.cfg.Router.Domain, "."))
	tr := &http.Transport{
		DialContext:         g.routerDial,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     30 * time.Second,
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// The hop-by-hop headers and any X-Forwarded-* a client sent are
			// gone by now (ReverseProxy removes them before Rewrite); these
			// are the router's own.
			pr.SetXForwarded()
			target := pr.In.Context().Value(routeKey{}).(string)
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = target
			pr.Out.Host = pr.In.Host
		},
		Transport:      tr,
		FlushInterval:  -1,
		ModifyResponse: hostOnlyCookies,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			g.logf("router: %s: %v", r.Host, err)
			http.Error(w, "the service did not answer", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := g.routeHost(r.Host, domain)
		if !ok {
			http.Error(w, "no such service", http.StatusNotFound)
			return
		}
		target, ok := g.pickReplica(s)
		if !ok {
			http.Error(w, "no replica of this service is healthy", http.StatusServiceUnavailable)
			return
		}
		rp.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), routeKey{}, target)))
	})
}

type routeKey struct{}

// hostOnlyCookies takes the Domain off every cookie a replica sets. Every
// service is a sibling under DOMAIN, so a cookie for DOMAIN itself would be
// sent to every other tenant's service, and one set there would reach this
// one: a service could plant a session in another's. Host-only, a cookie
// stays with the service that set it. A Set-Cookie that does not parse is
// dropped rather than passed on as it came.
func hostOnlyCookies(resp *http.Response) error {
	lines := resp.Header.Values("Set-Cookie")
	if len(lines) == 0 {
		return nil
	}
	resp.Header.Del("Set-Cookie")
	for _, l := range lines {
		c, err := http.ParseSetCookie(l)
		if err != nil {
			continue
		}
		c.Domain = ""
		if v := c.String(); v != "" {
			resp.Header.Add("Set-Cookie", v)
		}
	}
	return nil
}

// routeHost finds the public service a Host names.
func (g *Gateway) routeHost(host, domain string) (*service, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	label, ok := strings.CutSuffix(host, "."+domain)
	if !ok || domain == "" || label == "" || strings.Contains(label, ".") {
		return nil, false
	}
	name, tenant, _ := strings.Cut(label, "--")
	if !validServiceName(name) {
		return nil, false
	}
	s := g.services.get(tenant, name)
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone || !s.rec.Spec.Public {
		return nil, false
	}
	return s, true
}

// pickReplica is the next healthy replica, round robin, as the dial target
// "<sandbox id>:<port>" the router's transport understands.
func (g *Gateway) pickReplica(s *service) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !userActive(g.store, s.rec.User) {
		return "", false
	}
	var up []replicaRecord
	for _, m := range s.rec.Members {
		if h := s.health[m.Sandbox]; h != nil && h.healthy && !h.lost && !h.fatal {
			up = append(up, m)
		}
	}
	if len(up) == 0 {
		return "", false
	}
	m := up[s.rr%uint64(len(up))]
	s.rr++
	return m.Sandbox + ":" + strconv.Itoa(s.specOf(m.Rev).Port), true
}

// routerDial opens the tunnel a routed request goes over. The address is
// the one pickReplica made, never anything a client sent; the node is the
// one the store records for the sandbox.
func (g *Gateway) routerDial(ctx context.Context, _, addr string) (net.Conn, error) {
	id, p, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(p)
	if err != nil || !spec.ValidID(id) {
		return nil, ErrNotFound
	}
	o, ok := g.store.OwnerOf(id)
	if !ok {
		return nil, ErrNotFound
	}
	n := g.nodes.get(o.Node)
	if n == nil || !n.isHealthy() {
		return nil, ErrNodeDown
	}
	return dialTunnel(ctx, n, id, port)
}
