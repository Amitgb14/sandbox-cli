package studio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Gateway keys for the tests: the shape validKeyShape accepts.
var (
	aliceKey = "sgk_" + strings.Repeat("a", 52)
	bobKey   = "sgk_" + strings.Repeat("b", 52)
	adminKey = "sgk_" + strings.Repeat("c", 52)
)

// testGateway stands in for sandbox-gateway: it answers /v1/whoami from its
// key table, refuses an unknown key with 401 as the gateway does, records
// the credential and cookies of every request, and hands the rest to a real
// sandboxd on the fake backend (with sandboxd's own token).
type testGateway struct {
	mu      sync.Mutex
	keys    map[string]api.Whoami
	auths   []string // Authorization of each request, in order
	cookies []string
	sd      http.Handler
}

func newTestGateway() *testGateway {
	b := fake.New(api.CapEgressAllowlist)
	return &testGateway{
		keys: map[string]api.Whoami{
			aliceKey: {User: "alice", Tenant: "alice", Scopes: []string{"sandbox:read", "sandbox:create", "sandbox:delete"}},
			bobKey:   {User: "bob", Tenant: "bob", Scopes: []string{"sandbox:read"}},
			adminKey: {User: "ops", Scopes: []string{"admin"}},
		},
		sd: (&server.Server{Backend: b, Policy: spec.DefaultPolicyFor(b.Capabilities()), Token: "sandboxd-token"}).Handler(),
	}
}

func (g *testGateway) revoke(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.keys, key)
}

func (g *testGateway) seen() (auths, cookies []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.auths...), append([]string(nil), g.cookies...)
}

func (g *testGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	g.mu.Lock()
	g.auths = append(g.auths, auth)
	if c := r.Header.Get("Cookie"); c != "" {
		g.cookies = append(g.cookies, c)
	}
	who, ok := g.keys[strings.TrimPrefix(auth, "Bearer ")]
	g.mu.Unlock()
	if r.URL.Path == "/v1/health" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, api.ErrorBody{Error: api.ErrorDetail{Code: api.CodeUnauthorized, Message: "unknown key"}})
		return
	}
	if r.URL.Path == "/v1/whoami" {
		writeJSON(w, http.StatusOK, who)
		return
	}
	r.Header.Set("Authorization", "Bearer sandboxd-token")
	g.sd.ServeHTTP(w, r)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type hostedEnv struct {
	s      *Server
	st     *httptest.Server
	gw     *testGateway
	clock  *clock
	origin string
}

// hostedUnderTest is a hosted Studio in front of testGateway, on loopback
// http (the one place hosted mode allows it), with a clock the test moves.
func hostedUnderTest(t *testing.T, edit ...func(*Server)) *hostedEnv {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	gw := newTestGateway()
	gs := httptest.NewServer(gw)
	t.Cleanup(gs.Close)
	clk := &clock{t: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	st := httptest.NewUnstartedServer(nil)
	origin := "http://" + st.Listener.Addr().String()
	s := &Server{
		Hosted: &Hosted{Gateway: gs.URL, PublicOrigin: origin, Now: clk.now,
			SessionIdle: time.Hour, SessionMaxAge: 24 * time.Hour},
		// Set so a test can prove hosted mode never uses them.
		Token: testToken,
	}
	for _, f := range edit {
		f(s)
	}
	st.Config.Handler = s.Handler()
	st.Start()
	t.Cleanup(st.Close)
	return &hostedEnv{s: s, st: st, gw: gw, clock: clk, origin: origin}
}

type hreq struct {
	method, path string
	cookie       string
	origin       string
	host         string
	headers      map[string]string
	body         any
	raw          string // a body sent as is, with headers' Content-Type
}

func (e *hostedEnv) do(t *testing.T, q hreq) (*http.Response, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	switch {
	case q.raw != "":
		rd = bytes.NewReader([]byte(q.raw))
	case q.body != nil:
		b, _ := json.Marshal(q.body)
		rd = bytes.NewReader(b)
	default:
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(q.method, e.st.URL+q.path, rd)
	if q.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if q.origin != "" {
		req.Header.Set("Origin", q.origin)
	}
	if q.cookie != "" {
		req.Header.Set("Cookie", "sbx_studio="+q.cookie)
	}
	if q.host != "" {
		req.Host = q.host
	}
	for k, v := range q.headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// signIn exchanges key for a session and returns the cookie's value.
func (e *hostedEnv) signIn(t *testing.T, key string) string {
	t.Helper()
	r, body := e.do(t, hreq{method: "POST", path: "/api/session", origin: e.origin, body: map[string]string{"key": key}})
	if r.StatusCode != http.StatusOK {
		t.Fatalf("sign-in: %d %v", r.StatusCode, body)
	}
	for _, c := range r.Cookies() {
		if c.Name == "sbx_studio" {
			return c.Value
		}
	}
	t.Fatal("sign-in set no session cookie")
	return ""
}

func TestHostedSignIn(t *testing.T) {
	e := hostedUnderTest(t)
	r, body := e.do(t, hreq{method: "POST", path: "/api/session", origin: e.origin, body: map[string]string{"key": aliceKey}})
	if r.StatusCode != http.StatusOK || body["user"] != "alice" || body["tenant"] != "alice" {
		t.Fatalf("sign-in: %d %v", r.StatusCode, body)
	}
	var c *http.Cookie
	for _, x := range r.Cookies() {
		if x.Name == "sbx_studio" {
			c = x
		}
	}
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || len(c.Value) != 64 {
		t.Fatalf("cookie: %+v", c)
	}
	if strings.Contains(c.Value, aliceKey) {
		t.Fatal("the cookie carries the key")
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control: %q", r.Header.Get("Cache-Control"))
	}

	for _, tc := range []struct {
		name   string
		q      hreq
		status int
	}{
		{"unknown key", hreq{body: map[string]string{"key": "sgk_" + strings.Repeat("z", 52)}}, http.StatusUnauthorized},
		{"not a key", hreq{body: map[string]string{"key": "hunter2"}}, http.StatusUnauthorized},
		{"admin key", hreq{body: map[string]string{"key": adminKey}}, http.StatusForbidden},
		{"no Origin", hreq{origin: "-", body: map[string]string{"key": aliceKey}}, http.StatusForbidden},
		{"foreign Origin", hreq{origin: "https://evil.example", body: map[string]string{"key": aliceKey}}, http.StatusForbidden},
		{"not JSON", hreq{raw: "key=" + aliceKey, headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}}, http.StatusUnsupportedMediaType},
		{"unknown field", hreq{body: map[string]string{"key": aliceKey, "user": "bob"}}, http.StatusBadRequest},
	} {
		q := tc.q
		q.method, q.path = "POST", "/api/session"
		switch q.origin {
		case "":
			q.origin = e.origin
		case "-":
			q.origin = ""
		}
		r, body := e.do(t, q)
		if r.StatusCode != tc.status {
			t.Errorf("%s: %d %v, want %d", tc.name, r.StatusCode, body, tc.status)
		}
		if len(r.Cookies()) != 0 {
			t.Errorf("%s: set a cookie", tc.name)
		}
	}
}

// A plain sandboxd has one token, and whoever holds it is the operator:
// hosted Studio needs a gateway's per-user keys, and says so.
func TestHostedSignInAgainstAPlainSandboxdIsRefused(t *testing.T) {
	b := fake.New()
	sd := (&server.Server{Backend: b, Policy: spec.DefaultPolicyFor(b.Capabilities()), Token: "sandboxd-token"}).Handler()
	// Any key is sandboxd's token here, so the only refusal left is the
	// missing /v1/whoami.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("Authorization", "Bearer sandboxd-token")
		sd.ServeHTTP(w, r)
	}))
	t.Cleanup(up.Close)
	st := httptest.NewUnstartedServer(nil)
	origin := "http://" + st.Listener.Addr().String()
	st.Config.Handler = (&Server{Hosted: &Hosted{Gateway: up.URL, PublicOrigin: origin}}).Handler()
	st.Start()
	t.Cleanup(st.Close)
	e := &hostedEnv{st: st, origin: origin}
	r, body := e.do(t, hreq{method: "POST", path: "/api/session", origin: origin, body: map[string]string{"key": aliceKey}})
	if r.StatusCode != http.StatusBadGateway || !strings.Contains(body["error"].(map[string]any)["message"].(string), "gateway") {
		t.Fatalf("plain sandboxd: %d %v", r.StatusCode, body)
	}
	if len(r.Cookies()) != 0 {
		t.Fatal("set a cookie")
	}
}

func TestHostedSignInIsRateLimited(t *testing.T) {
	e := hostedUnderTest(t)
	bad := map[string]string{"key": "sgk_" + strings.Repeat("z", 52)}
	for i := range 5 {
		if r, _ := e.do(t, hreq{method: "POST", path: "/api/session", origin: e.origin, body: bad}); r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, r.StatusCode)
		}
	}
	if r, _ := e.do(t, hreq{method: "POST", path: "/api/session", origin: e.origin, body: map[string]string{"key": aliceKey}}); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("after 5 failures, even a good key: %d, want 429", r.StatusCode)
	}
	e.clock.advance(7 * time.Second)
	e.signIn(t, aliceKey)
}

// Every hosted route but sign-in needs a session, and nothing else stands in
// for one: not local Studio's token, not a valid gateway key presented
// directly, not ?token=.
func TestHostedRoutesNeedASession(t *testing.T) {
	e := hostedUnderTest(t)
	routes := []hreq{
		{method: "GET", path: "/api/info"},
		{method: "GET", path: "/api/session"},
		{method: "DELETE", path: "/api/session"},
		{method: "GET", path: "/api/v1/sandboxes"},
		{method: "POST", path: "/api/v1/sandboxes", body: map[string]any{}},
		{method: "GET", path: "/api/agents"},
		{method: "GET", path: "/api/agents/state"},
		{method: "POST", path: "/api/runs", body: map[string]any{"command": []string{"true"}}},
		{method: "GET", path: "/api/no-such"},
	}
	for _, creds := range []map[string]string{
		nil,
		{"Authorization": "Bearer " + testToken},
		{"Authorization": "Bearer " + aliceKey},
	} {
		for _, q := range routes {
			q.origin, q.headers = e.origin, creds
			if r, body := e.do(t, q); r.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s with %v: %d %v, want 401", q.method, q.path, creds, r.StatusCode, body)
			}
		}
	}
	for _, path := range []string{"/api/info?token=" + testToken, "/api/v1/sandboxes?token=" + aliceKey} {
		if r, _ := e.do(t, hreq{method: "GET", path: path}); r.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", path, r.StatusCode)
		}
	}
	// A cookie that was never issued.
	if r, _ := e.do(t, hreq{method: "GET", path: "/api/info", cookie: strings.Repeat("0", 64)}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("forged cookie: %d", r.StatusCode)
	}
	for _, path := range []string{"/api/ws/attach?sandbox=x&token=" + testToken, "/api/ws/desktop?sandbox=x&token=" + testToken} {
		if resp := e.dialWS(t, path, e.origin, ""); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", path, resp.StatusCode)
		}
	}
	if auths, _ := e.gw.seen(); len(auths) != 0 {
		t.Errorf("requests without a session reached the gateway: %v", auths)
	}
}

func TestHostedOriginAndHost(t *testing.T) {
	e := hostedUnderTest(t, func(s *Server) { s.Hosted.AllowedHosts = []string{"studio.internal"} })
	cookie := e.signIn(t, aliceKey)

	if r, _ := e.do(t, hreq{method: "GET", path: "/api/info", cookie: cookie}); r.StatusCode != http.StatusOK {
		t.Errorf("same-origin GET without Origin: %d", r.StatusCode)
	}
	if r, _ := e.do(t, hreq{method: "GET", path: "/api/info", cookie: cookie, origin: "https://evil.example"}); r.StatusCode != http.StatusForbidden {
		t.Errorf("GET from another origin: %d", r.StatusCode)
	}
	if r, _ := e.do(t, hreq{method: "DELETE", path: "/api/session", cookie: cookie}); r.StatusCode != http.StatusForbidden {
		t.Errorf("DELETE without Origin: %d", r.StatusCode)
	}
	// DNS rebinding: a page whose own name resolves to this address.
	for _, path := range []string{"/", "/api/info"} {
		if r, _ := e.do(t, hreq{method: "GET", path: path, cookie: cookie, host: "rebind.example"}); r.StatusCode != http.StatusForbidden {
			t.Errorf("foreign Host on %s: %d", path, r.StatusCode)
		}
	}
	// Loopback is not special when hosted: only the public name is.
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(e.origin, "http://"))
	if r, _ := e.do(t, hreq{method: "GET", path: "/", host: "localhost:" + port}); r.StatusCode != http.StatusForbidden {
		t.Errorf("localhost Host: %d", r.StatusCode)
	}
	if r, _ := e.do(t, hreq{method: "GET", path: "/", host: "studio.internal:8080"}); r.StatusCode != http.StatusOK {
		t.Errorf("allowed host: %d", r.StatusCode)
	}
	if resp := e.dialWS(t, "/api/ws/attach?sandbox=x", "https://evil.example", cookie); resp.StatusCode != http.StatusForbidden {
		t.Errorf("WebSocket from another origin: %d", resp.StatusCode)
	}
	if resp := e.dialWS(t, "/api/ws/attach?sandbox=x", "", cookie); resp.StatusCode != http.StatusForbidden {
		t.Errorf("WebSocket without Origin: %d", resp.StatusCode)
	}
}

// Each session's calls carry its own key, and the session cookie never
// reaches the gateway.
func TestHostedCallsCarryTheSessionsKey(t *testing.T) {
	e := hostedUnderTest(t)
	alice, bob := e.signIn(t, aliceKey), e.signIn(t, bobKey)
	for _, tc := range []struct{ cookie, key string }{{alice, aliceKey}, {bob, bobKey}, {alice, aliceKey}} {
		before, _ := e.gw.seen()
		r, body := e.do(t, hreq{method: "GET", path: "/api/v1/sandboxes", cookie: tc.cookie,
			headers: map[string]string{"Authorization": "Bearer " + adminKey}})
		if r.StatusCode != http.StatusOK {
			t.Fatalf("list: %d %v", r.StatusCode, body)
		}
		after, _ := e.gw.seen()
		if got := after[len(before):]; len(got) != 1 || got[0] != "Bearer "+tc.key {
			t.Errorf("the gateway saw %v, want the session's key", got)
		}
	}
	// Through clientFor too: info asks for capabilities.
	before, _ := e.gw.seen()
	if r, body := e.do(t, hreq{method: "GET", path: "/api/info", cookie: bob}); r.StatusCode != http.StatusOK ||
		body["context"] != "hosted" || body["hosted"] != true || body["user"] != "bob" || body["capabilities"] == nil {
		t.Errorf("info: %d %v", r.StatusCode, body)
	}
	after, _ := e.gw.seen()
	for _, a := range after[len(before):] {
		if a != "Bearer "+bobKey {
			t.Errorf("info used %q", a)
		}
	}
	if _, cookies := e.gw.seen(); len(cookies) != 0 {
		t.Errorf("cookies reached the gateway: %v", cookies)
	}
}

// A key revoked at the gateway ends the session at the next call.
func TestHostedRevokedKeyEndsTheSession(t *testing.T) {
	e := hostedUnderTest(t)
	cookie := e.signIn(t, aliceKey)
	e.gw.revoke(aliceKey)
	if r, _ := e.do(t, hreq{method: "GET", path: "/api/v1/sandboxes", cookie: cookie}); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after revocation: %d", r.StatusCode)
	}
	if r, body := e.do(t, hreq{method: "GET", path: "/api/session", cookie: cookie}); r.StatusCode != http.StatusUnauthorized || body["error"].(map[string]any)["code"] != "session" {
		t.Errorf("the session outlived its key: %d %v", r.StatusCode, body)
	}

	// Through clientFor (info's capabilities call) as well as the proxy.
	cookie = e.signIn(t, bobKey)
	e.gw.revoke(bobKey)
	if r, body := e.do(t, hreq{method: "GET", path: "/api/info", cookie: cookie}); r.StatusCode != http.StatusUnauthorized || body["error"].(map[string]any)["code"] != "session" {
		t.Errorf("info with a revoked key: %d %v; want the session-ended answer", r.StatusCode, body)
	}
	if r, _ := e.do(t, hreq{method: "GET", path: "/api/session", cookie: cookie}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("a 401 to a clientFor call left the session: %d", r.StatusCode)
	}
}

func TestHostedSignOut(t *testing.T) {
	e := hostedUnderTest(t)
	cookie := e.signIn(t, aliceKey)
	r, _ := e.do(t, hreq{method: "DELETE", path: "/api/session", cookie: cookie, origin: e.origin})
	if r.StatusCode != http.StatusNoContent {
		t.Fatalf("sign out: %d", r.StatusCode)
	}
	if c := r.Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Errorf("sign out did not clear the cookie: %+v", c)
	}
	if r, _ := e.do(t, hreq{method: "GET", path: "/api/session", cookie: cookie}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("after sign out: %d", r.StatusCode)
	}
}

func TestHostedSessionsExpire(t *testing.T) {
	e := hostedUnderTest(t)
	idle := e.signIn(t, aliceKey)
	e.clock.advance(61 * time.Minute)
	if r, _ := e.do(t, hreq{method: "GET", path: "/api/session", cookie: idle}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("idle past an hour: %d", r.StatusCode)
	}
	busy := e.signIn(t, aliceKey)
	for range 25 { // 24h35m, never idle for an hour
		e.clock.advance(59 * time.Minute)
		e.do(t, hreq{method: "GET", path: "/api/session", cookie: busy})
	}
	if r, _ := e.do(t, hreq{method: "GET", path: "/api/session", cookie: busy}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("in use past its maximum age: %d", r.StatusCode)
	}
}

func TestHostedSessionsPerKeyAreCapped(t *testing.T) {
	e := hostedUnderTest(t)
	first := e.signIn(t, aliceKey)
	for range sessionsPerKey {
		e.clock.advance(time.Second)
		e.signIn(t, aliceKey)
	}
	if r, _ := e.do(t, hreq{method: "GET", path: "/api/session", cookie: first}); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("the oldest session was kept past the cap: %d", r.StatusCode)
	}
	if n := len(e.s.hosted.sessions.m); n != sessionsPerKey {
		t.Errorf("%d sessions, want %d", n, sessionsPerKey)
	}
}

// Nothing of the machine Studio runs on is shown to its users: not whether
// it holds an agent login, not which API keys are in its environment.
func TestHostedAgentsRevealNothingOfTheHost(t *testing.T) {
	const secret = "sk-test-do-not-show-0123456789"
	t.Setenv("ANTHROPIC_API_KEY", secret)
	e := hostedUnderTest(t)
	d, _ := agents.Lookup("claude")
	for _, rel := range d.AuthPaths {
		p := filepath.Join(agenthome.LoginDir(d), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cookie := e.signIn(t, aliceKey)
	req, _ := http.NewRequest("GET", e.st.URL+"/api/agents", nil)
	req.Header.Set("Cookie", "sbx_studio="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct{ Agents []Agent }
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || len(got.Agents) == 0 {
		t.Fatalf("agents: %v %v", err, got)
	}
	for _, a := range got.Agents {
		if a.Login != "in sandbox" || len(a.Env) != 0 {
			t.Errorf("%s: login %q env %v; want nothing of the host", a.Name, a.Login, a.Env)
		}
	}
}

func TestHostedLaunchUsesTheSessionsClient(t *testing.T) {
	var got *api.Client
	e := hostedUnderTest(t, func(s *Server) {
		s.Launch = func(ctx context.Context, c *api.Client, req LaunchRequest) (LaunchResult, error) {
			got = c
			if _, err := c.Capabilities(ctx); err != nil {
				return LaunchResult{}, err
			}
			return LaunchResult{Sandbox: "sbx_1", PID: 1}, nil
		}
	})
	cookie := e.signIn(t, aliceKey)
	before, _ := e.gw.seen()
	r, body := e.do(t, hreq{method: "POST", path: "/api/runs", cookie: cookie, origin: e.origin,
		body: map[string]any{"command": []string{"true"}}})
	if r.StatusCode != http.StatusCreated || got == nil {
		t.Fatalf("launch: %d %v", r.StatusCode, body)
	}
	after, _ := e.gw.seen()
	if a := after[len(before):]; len(a) != 1 || a[0] != "Bearer "+aliceKey {
		t.Errorf("the launcher's calls carried %v", a)
	}
	// Unattended agents are jobs when hosted.
	r, body = e.do(t, hreq{method: "POST", path: "/api/runs", cookie: cookie, origin: e.origin,
		body: map[string]any{"agent": "claude", "prompt": "hi"}})
	if r.StatusCode != http.StatusBadRequest || !strings.Contains(body["error"].(map[string]any)["message"].(string), "job") {
		t.Errorf("headless agent: %d %v", r.StatusCode, body)
	}
}

func TestHostedValidate(t *testing.T) {
	for _, tc := range []struct {
		h  Hosted
		ok bool
	}{
		{Hosted{Gateway: "http://127.0.0.1:8443", PublicOrigin: "https://studio.example.com"}, true},
		{Hosted{Gateway: "https://gw.example.com", PublicOrigin: "http://127.0.0.1:7080"}, true},
		{Hosted{Gateway: "http://gw.example.com", PublicOrigin: "https://studio.example.com"}, false},
		{Hosted{Gateway: "http://127.0.0.1:8443", PublicOrigin: "http://studio.example.com"}, false},
		{Hosted{Gateway: "http://127.0.0.1:8443", PublicOrigin: "https://studio.example.com/app"}, false},
		{Hosted{Gateway: "unix:///run/gw.sock", PublicOrigin: "https://studio.example.com"}, false},
		{Hosted{Gateway: "http://127.0.0.1:8443"}, false},
	} {
		if err := tc.h.Validate(); (err == nil) != tc.ok {
			t.Errorf("%+v: %v, want ok=%v", tc.h, err, tc.ok)
		}
	}
	h := Hosted{PublicOrigin: "https://studio.example.com"}
	if h.cookieName() != "__Host-sbx_studio" || !h.secure() {
		t.Errorf("https: cookie %q secure %v", h.cookieName(), h.secure())
	}
}

func TestValidKeyShape(t *testing.T) {
	for k, want := range map[string]bool{
		aliceKey:                               true,
		"sgk_short":                            false,
		"sk-" + strings.Repeat("a", 52):        false,
		"sgk_" + strings.Repeat("A", 52):       false,
		"sgk_" + strings.Repeat("a", 51) + "/": false,
		"":                                     false,
	} {
		if validKeyShape(k) != want {
			t.Errorf("%q: want %v", k, want)
		}
	}
}

// dialWS starts a WebSocket handshake with an Origin and a session cookie
// and returns the response.
func (e *hostedEnv) dialWS(t *testing.T, path, origin, cookie string) *http.Response {
	t.Helper()
	addr := strings.TrimPrefix(e.st.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	req := "GET " + path + " HTTP/1.1\r\nHost: " + addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + wsTestKey + "\r\nSec-WebSocket-Version: 13\r\n"
	if origin != "" {
		req += "Origin: " + origin + "\r\n"
	}
	if cookie != "" {
		req += "Cookie: sbx_studio=" + cookie + "\r\n"
	}
	if _, err := conn.Write([]byte(req + "\r\n")); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
