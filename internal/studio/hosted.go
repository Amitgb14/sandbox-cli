package studio

// Hosted Studio: one Studio on a public address, used by many people, each
// with their own gateway API key.
//
// Local Studio answers one person on their own machine: loopback only, a
// token per launch, and the context's credential held by the process. None
// of that survives being put on the internet — every visitor would act as
// whoever started it, and runs would be launched with that machine's config,
// agent logins and API keys. Hosted mode keeps the parts that are only API
// calls and changes who is calling:
//
//   - A user signs in once with their own key (an invite link carries it in
//     the URL fragment, which a browser never sends to a server). Studio
//     checks it with the gateway, keeps it in memory, and gives the browser
//     an HttpOnly session cookie. The key is not in the browser after that.
//   - Every call is made with that session's key, so the gateway's
//     ownership, tenants and quotas decide what each person sees: Studio
//     adds no isolation of its own and needs none.
//   - Nothing is read from the machine Studio runs on: no context, no agent
//     logins, no environment, no config. The launcher is the caller's
//     (internal/cli), and is API-only in this mode.
//   - It holds no operator credential at all. An admin key is refused: the
//     operator's screens stay in local Studio, on the operator's machine.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Hosted configures a Studio served to many users. With it set on a Server,
// Handler serves hosted mode; Client, Token and Context are not used.
type Hosted struct {
	// Gateway is the gateway's base URL. Hosted Studio refuses a plain
	// sandboxd: it has one token, and whoever holds it is the operator.
	Gateway string
	// HTTP reaches the gateway (a private CA, say); nil is the default client.
	HTTP *http.Client
	// PublicOrigin is the scheme://host[:port] browsers load Studio from.
	// The only Origin accepted, and the default Host.
	PublicOrigin string
	// AllowedHosts are further Host headers to answer, for a proxy in front
	// that rewrites Host to its own upstream name.
	AllowedHosts []string
	// SessionIdle ends a session not used for this long; SessionMaxAge ends
	// one this long after sign-in whatever its use. Zero is 12h and 7 days.
	SessionIdle   time.Duration
	SessionMaxAge time.Duration
	// Now is the clock, for tests; nil is time.Now.
	Now func() time.Time
}

// Validate checks what Handler would otherwise have to refuse at run time.
func (h *Hosted) Validate() error {
	g, err := url.Parse(h.Gateway)
	if err != nil || (g.Scheme != "http" && g.Scheme != "https") || g.Host == "" {
		return fmt.Errorf("gateway %q: want http(s)://host[:port]", h.Gateway)
	}
	// A key travels to the gateway on every call: in the clear only where
	// nobody else is on the path.
	if g.Scheme == "http" && !loopbackHost(g.Host) {
		return fmt.Errorf("gateway %q: plain http only on loopback; use https", h.Gateway)
	}
	o, err := url.Parse(h.PublicOrigin)
	if err != nil || (o.Scheme != "http" && o.Scheme != "https") || o.Host == "" ||
		(o.Path != "" && o.Path != "/") || o.RawQuery != "" || o.Fragment != "" {
		return fmt.Errorf("public URL %q: want https://host[:port]", h.PublicOrigin)
	}
	// The session cookie is a credential; over plain http only for a Studio
	// nobody else can reach (tests, and trying it on one's own machine).
	if o.Scheme == "http" && !loopbackHost(o.Host) {
		return fmt.Errorf("public URL %q: https, unless it is loopback", h.PublicOrigin)
	}
	return nil
}

func (h *Hosted) origin() string {
	o, _ := url.Parse(h.PublicOrigin)
	return o.Scheme + "://" + o.Host
}

func (h *Hosted) secure() bool { return strings.HasPrefix(h.PublicOrigin, "https://") }

// cookieName is the session cookie. __Host- makes the browser refuse it
// unless it is Secure, host-only and Path=/, so no sibling domain can set or
// shadow it; a loopback http Studio cannot have Secure, and goes without.
func (h *Hosted) cookieName() string {
	if h.secure() {
		return "__Host-sbx_studio"
	}
	return "sbx_studio"
}

func (h *Hosted) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// hostAllowed is the DNS-rebinding guard of hosted mode: the public name, or
// one the operator listed, and nothing else.
func (h *Hosted) hostAllowed(host string) bool {
	o, _ := url.Parse(h.PublicOrigin)
	if strings.EqualFold(host, o.Host) {
		return true
	}
	name := host
	if n, _, err := net.SplitHostPort(host); err == nil {
		name = n
	}
	for _, a := range h.AllowedHosts {
		if strings.EqualFold(a, host) || strings.EqualFold(a, name) {
			return true
		}
	}
	return false
}

// hosted is what a hosted Server builds once, in Handler.
type hosted struct {
	cfg      *Hosted
	gateway  *url.URL
	rt       http.RoundTripper // to the gateway, dropping a session the gateway stops accepting
	sessions *sessionStore
	logins   *bucket
}

func (s *Server) setupHosted() *hosted {
	h := s.Hosted
	if err := h.Validate(); err != nil {
		// The command validates first; reaching here is a programming error,
		// and serving anyway would be serving something unchecked.
		panic("studio: " + err.Error())
	}
	g, _ := url.Parse(h.Gateway)
	base := http.RoundTripper(http.DefaultTransport)
	if h.HTTP != nil && h.HTTP.Transport != nil {
		base = h.HTTP.Transport
	}
	idle, maxAge := h.SessionIdle, h.SessionMaxAge
	if idle <= 0 {
		idle = 12 * time.Hour
	}
	if maxAge <= 0 {
		maxAge = 7 * 24 * time.Hour
	}
	hs := &hosted{
		cfg:      h,
		gateway:  g,
		sessions: newSessionStore(idle, maxAge, h.now),
		// Keys are 256 random bits, so this does not protect them; it keeps a
		// stream of junk from becoming a stream of calls to the gateway.
		logins: newBucket(5, time.Minute/10, h.now),
	}
	hs.rt = revokingTransport{next: base, sessions: hs.sessions}
	return hs
}

// hostedHandler is everything a hosted Studio serves.
func (s *Server) hostedHandler() http.Handler {
	s.hosted = s.setupHosted()
	mux := http.NewServeMux()
	api := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.hostedGuard(h)) }

	mux.Handle("POST /api/session", s.hostedHost(s.signInGuard(s.signIn)))
	api("GET /api/session", s.sessionInfo)
	api("DELETE /api/session", s.signOut)
	api("GET /api/info", s.info)
	api("/api/v1/", s.proxy())
	api("GET /api/ws/attach", s.attach)
	api("GET /api/ws/desktop", s.desktop)
	api("GET /api/agents/state", s.agentStates)
	api("POST /api/runs", s.launch)
	api("GET /api/agents", s.agents)
	api("POST /api/sandboxes/{id}/resize", s.resize)
	// Studio's settings (templates, egress rules and groups, agents' saved
	// keys) are this machine's user's, kept in their ~/.config: hosted users
	// get the built-in sizes and nothing else, and no route that writes.
	api("GET /api/templates", s.hostedTemplates)
	api("GET /api/egress", s.hostedEgress)
	mux.Handle("/api/", s.hostedGuard(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "no such endpoint")
	}))
	mux.Handle("/", s.hostedHost(s.ui()))
	return mux
}

// hostedHost refuses a Host that is not Studio's public name, and sets the
// headers every response carries.
func (s *Server) hostedHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosted.cfg.hostAllowed(r.Host) {
			writeErr(w, http.StatusForbidden, "this Studio does not answer that name")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// originOK: a request that changes something, and every WebSocket (which
// CORS does not cover), must come from Studio's own pages. A GET without an
// Origin is a same-origin navigation or fetch; one with a foreign Origin is
// refused too, since the session cookie is SameSite=Strict and a browser
// would not have sent it from there anyway.
func (s *Server) originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	want := s.hosted.cfg.origin()
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if isWebSocketUpgrade(r) {
			return o == want
		}
		return o == "" || o == want
	}
	return o == want
}

// hostedGuard applies, in order: Host, Origin, a live session, and for a
// body, a JSON content type. A Bearer token or ?token= is not looked at: the
// only credential hosted Studio takes from a browser is its session cookie.
func (s *Server) hostedGuard(next http.HandlerFunc) http.Handler {
	return s.hostedHost(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.originOK(r) {
			writeErr(w, http.StatusForbidden, "cross-origin requests are refused")
			return
		}
		sess := s.sessionOf(r)
		if sess == nil {
			writeNoSession(w)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodDelete &&
			r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") &&
			!strings.HasPrefix(r.URL.Path, "/api/v1/") {
			writeErr(w, http.StatusUnsupportedMediaType, "want Content-Type: application/json")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	}))
}

// hostedTemplates is the built-in sizes only: saved ones are the host
// user's, in studio.json, which hosted Studio neither reads nor writes.
func (s *Server) hostedTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"templates": append([]Template{}, builtinTemplates...)})
}

// hostedEgress is no rules and no groups: the server's policy is what bounds
// a hosted user's egress, and the host user's own rules are not theirs.
func (s *Server) hostedEgress(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"rules": []EgressRule{}, "groups": []EgressGroup{}})
}

// writeNoSession is the answer to a request whose session is missing or has
// just ended: the page shows "open your invite link again" on this code.
func writeNoSession(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{
		"code": "session", "message": "no session: open your invite link again"}})
}

// signInGuard is the guard of the one route without a session: Origin is
// required, not merely checked, and the body must be JSON.
func (s *Server) signInGuard(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Origin") != s.hosted.cfg.origin() {
			writeErr(w, http.StatusForbidden, "cross-origin requests are refused")
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeErr(w, http.StatusUnsupportedMediaType, "want Content-Type: application/json")
			return
		}
		next(w, r)
	})
}

func (s *Server) sessionOf(r *http.Request) *session {
	c, err := r.Cookie(s.hosted.cfg.cookieName())
	if err != nil {
		return nil
	}
	return s.hosted.sessions.lookup(c.Value)
}

type sessionKey struct{}

// sessionFrom is the session the guard admitted the request under; nil in
// local mode.
func sessionFrom(ctx context.Context) *session {
	s, _ := ctx.Value(sessionKey{}).(*session)
	return s
}

// validKeyShape is what a gateway API key looks like: "sgk_" and lowercase
// base32. Anything else is refused before it costs a call to the gateway.
func validKeyShape(k string) bool {
	rest, ok := strings.CutPrefix(k, "sgk_")
	if !ok || len(rest) < 16 || len(rest) > 128 {
		return false
	}
	for _, c := range rest {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// signIn exchanges a gateway API key for a session.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &body) {
		return
	}
	hs := s.hosted
	if !hs.logins.ready() {
		writeErr(w, http.StatusTooManyRequests, "too many failed sign-ins; wait a minute")
		return
	}
	key := strings.TrimSpace(body.Key)
	if !validKeyShape(key) {
		hs.logins.take()
		writeErr(w, http.StatusUnauthorized, "that is not an API key")
		return
	}
	// The key's own client, over the transport that drops a session the
	// gateway stops accepting. There is no session yet, so nothing to drop.
	c := api.NewClientWithHTTP(hs.gateway.String(), key, &http.Client{Transport: hs.rt})
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	who, err := c.Whoami(ctx)
	if err != nil {
		var e *api.Error
		switch {
		case errors.As(err, &e) && e.Status == http.StatusUnauthorized:
			hs.logins.take()
			writeErr(w, http.StatusUnauthorized, "this key is not accepted: revoked, expired or mistyped")
		case errors.As(err, &e) && e.Status == http.StatusNotFound:
			writeErr(w, http.StatusBadGateway, "hosted Studio needs a gateway, and this endpoint is a plain sandboxd")
		default:
			s.logf("sign-in: the gateway did not answer: %v", err)
			writeErr(w, http.StatusBadGateway, "the gateway did not answer")
		}
		return
	}
	if slices.Contains(who.Scopes, "admin") {
		writeErr(w, http.StatusForbidden, "an admin key cannot sign in to hosted Studio; use sandbox-cli studio on your own machine")
		return
	}
	id, sess := hs.sessions.create(key, c, who)
	http.SetCookie(w, &http.Cookie{
		Name: hs.cfg.cookieName(), Value: id, Path: "/",
		HttpOnly: true, Secure: hs.cfg.secure(), SameSite: http.SameSiteStrictMode,
		MaxAge: int(hs.sessions.maxAge / time.Second),
	})
	s.logf("sign-in: %s (tenant %s)", who.User, who.Tenant)
	writeJSON(w, http.StatusOK, sess.view())
}

func (s *Server) sessionInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, sessionFrom(r.Context()).view())
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request) {
	s.hosted.sessions.drop(sessionFrom(r.Context()))
	http.SetCookie(w, &http.Cookie{
		Name: s.hosted.cfg.cookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.hosted.cfg.secure(), SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// revokingTransport ends a session the gateway answers 401 for: its key was
// revoked, so the next call is refused here rather than at the gateway, and
// the page tells its user to open their invite link again.
type revokingTransport struct {
	next     http.RoundTripper
	sessions *sessionStore
}

func (t revokingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		if sess := sessionFrom(req.Context()); sess != nil {
			t.sessions.drop(sess)
		}
	}
	return resp, err
}

// --- sessions -------------------------------------------------------------------

type session struct {
	hash    [32]byte // of the cookie value: the store holds no usable cookie
	key     string
	client  *api.Client
	who     api.Whoami
	created time.Time
	seen    time.Time
}

func (s *session) view() map[string]any {
	return map[string]any{"user": s.who.User, "tenant": s.who.Tenant, "scopes": s.who.Scopes}
}

const (
	sessionsPerKey = 20
	sessionsMax    = 1000
)

type sessionStore struct {
	mu     sync.Mutex
	m      map[[32]byte]*session
	idle   time.Duration
	maxAge time.Duration
	now    func() time.Time
}

func newSessionStore(idle, maxAge time.Duration, now func() time.Time) *sessionStore {
	return &sessionStore{m: map[[32]byte]*session{}, idle: idle, maxAge: maxAge, now: now}
}

// create makes a session and returns its cookie value. Past the caps, the
// oldest session — of that key first — makes room: a cap that refused new
// sign-ins would let anyone holding one key lock its owner out.
func (st *sessionStore) create(key string, c *api.Client, who api.Whoami) (string, *session) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	id := hex.EncodeToString(raw[:])
	now := st.now()
	sess := &session{hash: sha256.Sum256([]byte(id)), key: key, client: c, who: who, created: now, seen: now}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.countLocked(func(s *session) bool { return s.key == key }) >= sessionsPerKey {
		st.evictOldestLocked(func(s *session) bool { return s.key == key })
	}
	if len(st.m) >= sessionsMax {
		st.evictOldestLocked(func(*session) bool { return true })
	}
	st.m[sess.hash] = sess
	return id, sess
}

// lookup is the live session for a cookie value, nil when there is none or
// it has expired (and is then forgotten).
func (st *sessionStore) lookup(id string) *session {
	if id == "" {
		return nil
	}
	h := sha256.Sum256([]byte(id))
	now := st.now()
	st.mu.Lock()
	defer st.mu.Unlock()
	sess, ok := st.m[h]
	if !ok {
		return nil
	}
	if now.Sub(sess.seen) > st.idle || now.Sub(sess.created) > st.maxAge {
		delete(st.m, h)
		return nil
	}
	sess.seen = now
	return sess
}

func (st *sessionStore) drop(sess *session) {
	if sess == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.m, sess.hash)
}

func (st *sessionStore) countLocked(match func(*session) bool) int {
	n := 0
	for _, s := range st.m {
		if match(s) {
			n++
		}
	}
	return n
}

func (st *sessionStore) evictOldestLocked(match func(*session) bool) {
	var oldest *session
	for _, s := range st.m {
		if match(s) && (oldest == nil || s.created.Before(oldest.created)) {
			oldest = s
		}
	}
	if oldest != nil {
		delete(st.m, oldest.hash)
	}
}

// --- a token bucket for failed sign-ins -----------------------------------------

// bucket holds up to burst tokens and gains one every per. A failed sign-in
// takes one; with none left, sign-ins wait. One bucket for everyone: behind
// a proxy every request comes from the proxy's address, so there is no
// client address to count by.
type bucket struct {
	mu     sync.Mutex
	tokens float64
	burst  float64
	per    time.Duration
	last   time.Time
	now    func() time.Time
}

func newBucket(burst int, per time.Duration, now func() time.Time) *bucket {
	return &bucket{tokens: float64(burst), burst: float64(burst), per: per, last: now(), now: now}
}

func (b *bucket) refillLocked() {
	now := b.now()
	b.tokens = min(b.burst, b.tokens+float64(now.Sub(b.last))/float64(b.per))
	b.last = now
}

// ready reports whether a sign-in may be tried.
func (b *bucket) ready() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked()
	return b.tokens >= 1
}

// take spends a token on a failed sign-in.
func (b *bucket) take() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked()
	b.tokens = max(0, b.tokens-1)
}
