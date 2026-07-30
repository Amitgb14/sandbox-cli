package studioapi

import (
	"crypto/subtle"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// This file is the answer to one question: who may ask this process to start and
// stop containers? The CLI never had to answer it — a terminal already had — but
// an HTTP server does, and the answer cannot be "whoever can reach the port",
// because a web page the user merely visits can reach 127.0.0.1.
//
// Four checks, in the order withMiddleware applies them. None is redundant: the
// first two close different halves of the same attack, and the last two are
// hygiene that only matters once the first two are relied upon.

// maxRequestBody bounds every request body. The largest legitimate one is a
// prompt, and a megabyte of prompt is already far past what any agent accepts.
const maxRequestBody = 1 << 20

// hostAllowed guards against DNS rebinding: a page on attacker.example whose DNS
// answer is 127.0.0.1 reaches this server with the browser's same-origin policy
// satisfied, because as far as the browser is concerned the origin *is*
// attacker.example. What gives it away is the Host header, which carries the name
// the client dialled rather than the address it resolved to — so a request for
// anything but a loopback name is refused.
//
// AllowedHosts adds to loopback rather than replacing it, the same way a fleet
// task's `allow` adds to the fleet's: loopback is how this server is reached by
// design, and a configuration that could turn it off would be a footgun with no
// use case.
func (s *Server) hostAllowed(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]") // an IPv6 literal arrives bracketed
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	for _, allowed := range s.AllowedHosts {
		if strings.EqualFold(host, allowed) {
			return true
		}
	}
	return false
}

// originAllowed is the CSRF half, and it is the check that actually stops a
// malicious page from launching containers.
//
// Refusing to *reflect* an unlisted origin (applyCORS) only stops that page
// reading the response; the request still arrives and POST /runs still starts a
// container. Worse, a cross-origin POST escapes preflight entirely when it looks
// "simple" — a text/plain body carrying JSON, or no body at all, as
// `POST /runs/{id}/stop` takes — so CORS alone never sees it. A browser does
// attach Origin to every such request, so refusing an unlisted Origin outright is
// what closes it. A non-browser client (curl, the Studio backend, a test) sends
// no Origin and is unaffected; the bearer token is what governs those.
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if containsString(s.CORSOrigins, origin) {
		return true
	}
	// Same-origin: a page served from this very host:port. Safe to allow on its
	// own terms, and safe against rebinding because hostAllowed has already
	// established that r.Host is one we answer to.
	if u, err := url.Parse(origin); err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host) {
		return true
	}
	return false
}

// authorized enforces the optional bearer token. /health is exempt so a client
// can discover whether the server is up before it has a token to present.
//
// The comparison is constant-time: the token is a secret compared on every
// request, and a byte-by-byte early exit is exactly the shape a timing oracle
// needs. Cheap to get right, awkward to retrofit after someone binds this to a
// LAN interface.
func (s *Server) authorized(r *http.Request) bool {
	if s.Token == "" || r.URL.Path == "/health" {
		return true
	}
	presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if presented == "" && r.Method == http.MethodGet && isWebSocketUpgrade(r) {
		// The browser WebSocket API cannot set request headers, so a token-protected
		// server would have no way to serve a browser log stream at all. Narrowed to
		// a GET carrying an upgrade — which is the only shape a handshake takes — for
		// that reason: every other request has headers available and must use them,
		// since a query parameter lands in access logs and shell history. Without the
		// method check, any request at all could authenticate by query string just by
		// claiming to be an upgrade.
		presented = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(s.Token)) == 1
}

// checkContentType requires a JSON media type on any request that carries a
// body. Defense in depth behind originAllowed — a body this server will parse as
// JSON should be labelled as JSON, and insisting on that also means a
// cross-origin POST cannot stay "simple" enough to skip a preflight.
func checkContentType(r *http.Request) error {
	if r.ContentLength == 0 {
		return nil
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return fmt.Errorf("a request body requires Content-Type: application/json")
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return fmt.Errorf("unparseable Content-Type %q: %w", ct, err)
	}
	if mt != "application/json" {
		return fmt.Errorf("Content-Type %q is not supported; use application/json", mt)
	}
	return nil
}
