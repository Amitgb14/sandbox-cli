package server

import (
	"crypto/subtle"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// This file is the answer to one question: who may ask this process to create
// sandboxes and run commands in them? The CLI never had to answer it — a
// terminal already had — but an HTTP server does, and the answer cannot be
// "whoever can reach the port", because a web page the user merely visits can
// reach 127.0.0.1. Ported from the Studio daemon (internal/studioapi in beta.15),
// where each check was added for a reproduced attack.
//
// Four checks, in the order withMiddleware applies them. None is redundant: the
// first two close different halves of the same attack, and the last two are
// hygiene that only matters once the first two are relied upon.

// maxRequestBody bounds a JSON request body. The largest legitimate one is a run
// request carrying stdin, and a megabyte of that is far past what a command
// line needs; larger input goes through a file write.
const maxRequestBody = 1 << 20

// maxRawBody bounds a raw body: a file write or a chunk of stdin.
const maxRawBody = 64 << 20

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
// Refusing to *reflect* an unlisted origin only stops that page reading the
// response; the request still arrives and POST /v1/sandboxes still creates a
// sandbox. Worse, a cross-origin POST escapes preflight entirely when it looks
// "simple" — a text/plain body carrying JSON, or no body at all, as a DELETE
// does not — so CORS alone never sees it. A browser does
// attach Origin to every such request, so refusing an unlisted Origin outright is
// what closes it. A non-browser client (curl, the CLI, an SDK, a test) sends
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

// authorized enforces the bearer token. The health route is exempt so a client
// can discover whether the server is up before it has a token to present.
//
// It reads healthPath rather than a literal: two literals drifting apart is how
// an exemption silently stops matching the route it exempts — which happened in
// the Studio daemon this was ported from.
//
// The comparison is constant-time: the token is a secret compared on every
// request, and a byte-by-byte early exit is exactly the shape a timing oracle
// needs.
func (s *Server) authorized(r *http.Request) bool {
	if s.Token == "" || r.URL.Path == healthPath {
		return true
	}
	return s.tokenMatches(r)
}

// tokenMatches compares the presented bearer token against this server's,
// **without** the /health exemption above.
//
// Held apart because "this request is allowed" and "this caller proved who it
// is" stop being the same question at exactly one endpoint. /health answers
// unauthenticated so a client with no token can still be told it needs one — and
// that is precisely why anything sensitive it might carry has to ask this
// instead: an exemption meant to make an error message possible must not become
// a way to read the configuration.
func (s *Server) tokenMatches(r *http.Request) bool {
	if s.Token == "" {
		return false
	}
	// Header only. The Studio daemon also accepted ?token= on a WebSocket
	// handshake, because the browser WebSocket API cannot set headers; that
	// comes back, narrowed the same way, with the first WebSocket endpoint (PTY
	// sessions). Until then a token in a URL is only a token in an access log.
	presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(s.Token)) == 1
}

// checkContentType requires the media type the route expects on any request
// that carries a body: JSON for JSON routes, octet-stream for the raw ones (file
// writes, stdin). Defense in depth behind originAllowed — insisting on a typed
// body also means a cross-origin POST cannot stay "simple" enough to skip a
// preflight.
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
