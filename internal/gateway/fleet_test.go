package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/metrics"
)

// --- metrics -------------------------------------------------------------------

func scrapeGateway(t *testing.T, g *Gateway) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Host = "127.0.0.1:9101"
	rec := httptest.NewRecorder()
	g.MetricsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != metrics.ContentType {
		t.Fatalf("GET /metrics: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestGatewayMetrics(t *testing.T) {
	n1, n2 := startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...)
	tg := startGateway(t, func(c *Config) { c.Quota.Sandboxes = 2 }, n1, n2)
	alice := tg.client("alice-the-user", "acme-tenant", ScopeRead, ScopeCreate, ScopeDelete)
	ctx := ctxT(t)
	a, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "secret-project-name"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err = alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
	wantCode(t, err, api.CodeRefused)
	if err := alice.TerminateSandbox(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	resp := tg.raw(http.MethodGet, "/v1/sandboxes/"+a.ID, "", "")
	resp.Body.Close()

	got := scrapeGateway(t, tg.g)
	for _, want := range []string{
		`sandbox_gateway_http_requests_total{route="POST /v1/sandboxes",code="201"} 2`,
		`sandbox_gateway_http_requests_total{route="POST /v1/sandboxes",code="403"} 1`,
		`sandbox_gateway_http_requests_total{route="DELETE /v1/sandboxes/{ref}",code="204"} 1`,
		`sandbox_gateway_http_requests_total{route="GET /v1/sandboxes/{ref}",code="401"} 1`,
		"sandbox_gateway_sandboxes_created_total 2",
		`sandbox_gateway_sandboxes_terminated_total{how="delete"} 1`,
		`sandbox_gateway_creates_refused_total{reason="quota"} 1`,
		"sandbox_gateway_create_seconds_count 2",
		`sandbox_gateway_node_up{node="n1"} 1`,
		`sandbox_gateway_node_up{node="n2"} 1`,
		`sandbox_gateway_node_capacity_cpus{node="n1"} 64`,
		"sandbox_gateway_sandboxes_recorded 1",
		"sandbox_gateway_sandboxes_lost 0",
		"sandbox_gateway_ssh_connections_open 0",
		"sandbox_gateway_ssh_auth_failures_total 0",
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %q", want)
		}
	}
	if s1, s2 := tg.g.metrics.scheduled.Value("n1"), tg.g.metrics.scheduled.Value("n2"); s1+s2 != 2 {
		t.Errorf("scheduled n1=%v n2=%v; want 2 in all", s1, s2)
	}
	// Nothing a client chose is a label: not its user, tenant, sandbox name
	// or id, nor a path it sent.
	for _, s := range []string{"alice-the-user", "acme-tenant", "secret-project-name", a.ID} {
		if strings.Contains(got, s) {
			t.Errorf("metrics carry %q", s)
		}
	}
	if t.Failed() {
		t.Log(got)
	}
}

// A create no node has room for is counted as refused for capacity.
func TestGatewayMetricsNoCapacity(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	n1.cordoned.Store(true)
	tg.g.PollNow(ctxT(t))
	_, err := tg.user("alice").CreateSandbox(ctxT(t), api.CreateSandboxRequest{})
	if err == nil {
		t.Fatal("a create on a cordoned fleet succeeded")
	}
	if v := tg.g.metrics.refused.Value(refusedCapacity); v != 1 {
		t.Fatalf("no_capacity = %v", v)
	}
}

// --- audit -----------------------------------------------------------------------

func readAudit(t *testing.T, path string) (string, []api.AuditEntry) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("audit log mode %v", fi.Mode().Perm())
	}
	var out []api.AuditEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e api.AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		out = append(out, e)
	}
	return string(data), out
}

func TestAuditLogRecordsWhoWithoutSecrets(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	logPath := filepath.Join(t.TempDir(), "audit", "gateway.jsonl")
	tg := startGateway(t, func(c *Config) { c.Audit = NewAuditLog(logPath) }, n1)
	tg.g.SetSSH(fakeSSH{})
	ctx := ctxT(t)
	secret, key, err := tg.store.CreateKey("alice", "acme", []string{ScopeRead, ScopeCreate, ScopeDelete, ScopeSSH})
	if err != nil {
		t.Fatal(err)
	}
	alice := api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client())
	sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	access, err := alice.SSHAccess(ctx, sb.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	created, err := tg.admin.CreateKey(ctx, api.CreateKeyRequest{User: "bob", Scopes: []string{ScopeRead}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.Sandbox(ctx, "web"); err != nil {
		t.Fatal(err)
	}
	// Someone else's key on alice's sandbox: recorded, as not found.
	bob := api.NewClientWithHTTP(tg.ts.URL, created.Secret, tg.ts.Client())
	_, err = bob.Sandbox(ctx, sb.ID)
	wantCode(t, err, api.CodeNotFound)
	// No credential: not recorded (there is no one to record).
	resp := tg.raw(http.MethodGet, "/v1/sandboxes", "sgk_not-a-key", "")
	resp.Body.Close()

	raw, entries := readAudit(t, logPath)
	for _, s := range []string{secret, created.Secret, access.User, "sgk_not-a-key", hashSecret(secret)} {
		if strings.Contains(raw, s) {
			t.Fatalf("the audit log holds a secret (%.8s…):\n%s", s, raw)
		}
	}
	find := func(action, user string, status int) *api.AuditEntry {
		for i := range entries {
			e := &entries[i]
			if e.Action == action && e.User == user && e.Status == status {
				return e
			}
		}
		t.Errorf("no %s by %s with %d in\n%s", action, user, status, raw)
		return &api.AuditEntry{}
	}
	e := find("POST /v1/sandboxes", "alice", 201)
	if e.KeyID != key.ID || e.Tenant != "acme" || e.Sandbox != sb.ID || e.Node != "n1" || e.Kind != "api" || e.Remote == "" || e.Time.IsZero() {
		t.Errorf("create entry %+v", e)
	}
	if e := find("GET /v1/sandboxes/{ref}", "alice", 200); e.Sandbox != sb.ID {
		t.Errorf("a get by name records the id: %+v", e)
	}
	if e := find("POST /v1/sandboxes/{ref}/ssh-access", "alice", 201); e.Sandbox != sb.ID {
		t.Errorf("ssh-access entry %+v", e)
	}
	find("POST /v1/admin/keys", "root", 201)
	if e := find("GET /v1/sandboxes/{ref}", "bob", 404); e.Sandbox != "" {
		t.Errorf("a refused lookup names the sandbox it probed: %+v", e)
	}
	for _, e := range entries {
		if e.Status == http.StatusUnauthorized {
			t.Errorf("an unauthenticated request was recorded: %+v", e)
		}
	}

	// The admin reads it back, newest last, bounded and filtered by time.
	list, err := tg.admin.Audit(ctx, time.Time{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 2 || !list.Truncated || list.Entries[1].Action != "GET /v1/sandboxes/{ref}" || list.Entries[1].User != "bob" {
		t.Fatalf("last two entries: %+v", list)
	}
	list, err = tg.admin.Audit(ctx, time.Now().Add(time.Hour), 0)
	if err != nil || len(list.Entries) != 0 {
		t.Fatalf("entries from the future: %+v %v", list, err)
	}
	_, err = alice.Audit(ctx, time.Time{}, 0)
	wantCode(t, err, api.CodeRefused)
	for _, q := range []string{"?limit=0", "?limit=100000", "?since=yesterday"} {
		var eb api.ErrorBody
		if code := tg.adminDo(http.MethodGet, "/v1/admin/audit"+q, nil, &eb); code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, code)
		}
	}
}

func TestAuditEndpointWithoutALog(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	_, err := tg.admin.Audit(ctxT(t), time.Time{}, 0)
	wantCode(t, err, api.CodeUnsupported)
}

// ownerStore answers OwnerOf for the SSH harness, whose memStore has none.
type ownerStore struct{ Store }

func (ownerStore) OwnerOf(string) (Owner, bool) { return Owner{User: "alice", Node: "n1"}, true }

func TestSSHAuditAndMetrics(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "gateway.jsonl")
	gm := newGatewayMetrics()
	h := newHarness(t, func(c *SSHConfig) {
		c.Store = ownerStore{c.Store}
		c.Audit = NewAuditLog(logPath)
		c.Metrics = &SSHMetrics{m: gm}
	})
	k, _ := newKey(t)
	h.store.addKey(t, "sk_1", "alice", "", k.PublicKey())
	h.store.tokens["sgt_secret-token-value"] = SSHToken{User: "alice", Sandbox: h.a, Expires: time.Now().Add(time.Minute)}

	c := h.mustDial("alpha", ssh.PublicKeys(k))
	if out, _ := run(t, c, "cat marker"); out != "alpha\n" {
		t.Fatalf("exec: %q", out)
	}
	if gm.sshConns.Load() != 1 {
		t.Errorf("connections open = %d", gm.sshConns.Load())
	}
	tc := h.mustDial("sgt_secret-token-value")
	if out, _ := run(t, tc, "cat marker"); out != "alpha\n" {
		t.Fatalf("token exec: %q", out)
	}
	stranger, _ := newKey(t)
	if c, err := h.dial("alpha", nil, ssh.PublicKeys(stranger)); err == nil {
		c.Close()
		t.Fatal("a stranger logged in")
	}
	if c, err := h.dial("sgt_wrong-token", nil); err == nil {
		c.Close()
		t.Fatal("a wrong token logged in")
	}
	c.Close()
	tc.Close()
	waitFor(t, "connections to close", func() bool { return gm.sshConns.Load() == 0 && gm.sshSessions.Load() == 0 })
	if v := gm.sshAuthFail.Value(); v != 2 {
		t.Errorf("auth failures = %v, want 2", v)
	}

	raw, entries := readAudit(t, logPath)
	if strings.Contains(raw, "sgt_") {
		t.Fatalf("the audit log holds a token:\n%s", raw)
	}
	var keyLogin, tokenLogin, session, refusedKey, refusedToken bool
	for _, e := range entries {
		switch {
		case e.Action == "ssh.login" && e.Result == "ok" && e.Fingerprint == ssh.FingerprintSHA256(k.PublicKey()):
			keyLogin = e.KeyID == "sk_1" && e.User == "alice" && e.Sandbox == h.a && e.Node == "n1"
		case e.Action == "ssh.login" && e.Result == "ok" && e.Fingerprint == "token":
			tokenLogin = e.KeyID == "ssh-token" && e.Sandbox == h.a
		case e.Action == "ssh.session" && e.Session == "exec":
			session = true
		case e.Action == "ssh.login" && e.Result == "refused" && e.Fingerprint == ssh.FingerprintSHA256(stranger.PublicKey()):
			refusedKey = e.User == ""
		case e.Action == "ssh.login" && e.Result == "refused" && e.Fingerprint == "token":
			refusedToken = true
		}
	}
	if !keyLogin || !tokenLogin || !session || !refusedKey || !refusedToken {
		t.Errorf("key login %v, token login %v, session %v, refused key %v, refused token %v in\n%s",
			keyLogin, tokenLogin, session, refusedKey, refusedToken, raw)
	}
}

// --- drain -----------------------------------------------------------------------

func TestDrain(t *testing.T) {
	n1, n2 := startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...)
	tg := startGateway(t, nil, n1, n2)
	alice := tg.user("alice")
	ctx := ctxT(t)
	var on1 []string
	for len(on1) < 3 {
		sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if nodeOf(tg, sb.ID) == "n1" {
			on1 = append(on1, sb.ID)
		}
	}

	_, err := alice.DrainNode(ctx, "n1", false)
	wantCode(t, err, api.CodeRefused)
	_, err = tg.admin.DrainNode(ctx, "nope", false)
	wantCode(t, err, api.CodeNotFound)

	res, err := tg.admin.DrainNode(ctx, "n1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cordoned || res.Remaining != 3 || len(res.Terminated) != 0 || !n1.cordoned.Load() {
		t.Fatalf("drain without terminate: %+v", res)
	}
	for range 4 {
		sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if nodeOf(tg, sb.ID) != "n2" {
			t.Fatal("a sandbox landed on a draining node")
		}
	}
	// A node restarted (an upgrade) comes back uncordoned; the gateway puts
	// the cordon back, and places nothing there meanwhile.
	n1.cordoned.Store(false)
	tg.g.PollNow(ctx)
	if c, _ := tg.g.nodes.get("n1").candidate(); !c.Status.Cordoned {
		t.Fatal("a node the gateway cordoned is a candidate after restarting")
	}
	waitFor(t, "the cordon to be put back", func() bool { return n1.cordoned.Load() })

	res, err = tg.admin.DrainNode(ctx, "n1", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Remaining != 0 || len(res.Terminated) != 3 || len(res.Failed) != 0 {
		t.Fatalf("drain with terminate: %+v", res)
	}
	for _, id := range on1 {
		if _, ok := tg.store.OwnerOf(id); ok {
			t.Errorf("%s is still recorded", id)
		}
		// Its owner still reads its final state, as after a delete.
		sb, err := alice.Sandbox(ctx, id)
		if err != nil || sb.State != api.StateTerminated {
			t.Errorf("%s after a drain: %+v %v", id, sb, err)
		}
	}
	if n1.count() != 0 {
		t.Fatalf("n1 holds %d sandboxes after a drain", n1.count())
	}
	if v := tg.g.metrics.terminated.Value("drain"); v != 3 {
		t.Errorf("terminated by drain = %v", v)
	}

	// Uncordoning clears what the gateway remembers: a restart then leaves
	// the node uncordoned.
	if _, err := tg.admin.CordonNode(ctx, "n1", false); err != nil {
		t.Fatal(err)
	}
	if n1.cordoned.Load() {
		t.Fatal("uncordon did not reach the node")
	}
	tg.g.PollNow(ctx)
	if c, ok := tg.g.nodes.get("n1").candidate(); !ok || c.Status.Cordoned {
		t.Fatal("an uncordoned node is not a candidate")
	}

	// A node that is not answering cannot be drained.
	n1.down.Store(true)
	waitFor(t, "n1 to be unhealthy", func() bool { return !tg.g.nodes.get("n1").isHealthy() })
	_, err = tg.admin.DrainNode(ctx, "n1", true)
	wantCode(t, err, api.CodeUnavailable)
}

// --- node loss -------------------------------------------------------------------

func TestNodeLoss(t *testing.T) {
	n1, n2 := startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...)
	grace := 600 * time.Millisecond
	tg := startGateway(t, func(c *Config) { c.Quota.Sandboxes = 1; c.NodeLostAfter = grace }, n1, n2)
	alice := tg.client("alice", "acme", ScopeRead, ScopeCreate, ScopeDelete)
	ctx := ctxT(t)
	// n2 is cordoned so the first sandbox lands on n1.
	if _, err := tg.admin.CordonNode(ctx, "n2", true); err != nil {
		t.Fatal(err)
	}
	sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if nodeOf(tg, sb.ID) != "n1" {
		t.Fatal("not on n1")
	}
	if _, err := tg.admin.CordonNode(ctx, "n2", false); err != nil {
		t.Fatal(err)
	}

	n1.down.Store(true)
	waitFor(t, "n1 to be unhealthy", func() bool { return !tg.g.nodes.get("n1").isHealthy() })
	// Within the grace period: unavailable, and still counted.
	_, err = alice.Sandbox(ctx, sb.ID)
	wantCode(t, err, api.CodeUnavailable)
	if e := err.(*api.Error); e.Status != http.StatusServiceUnavailable || !strings.Contains(e.Message, "not answering") {
		t.Fatalf("a sandbox on a down node: %v", err)
	}
	if lost, err := tg.admin.LostSandboxes(ctx); err != nil || len(lost) != 0 {
		t.Fatalf("lost within the grace period: %+v %v", lost, err)
	}
	if _, err = alice.CreateSandbox(ctx, api.CreateSandboxRequest{}); !api.IsCode(err, api.CodeRefused) {
		t.Fatalf("over quota within the grace period: %v", err)
	}

	// Past it: listed as lost, no longer counted, and still unavailable.
	waitFor(t, "the sandbox to be reported lost", func() bool {
		lost, err := tg.admin.LostSandboxes(ctx)
		return err == nil && len(lost) == 1
	})
	lost, _ := tg.admin.LostSandboxes(ctx)
	if l := lost[0]; l.ID != sb.ID || l.User != "alice" || l.Tenant != "acme" || l.Node != "n1" || l.NodeDownSince.IsZero() {
		t.Fatalf("lost %+v", l)
	}
	if !strings.Contains(scrapeGateway(t, tg.g), "sandbox_gateway_sandboxes_lost 1\n") {
		t.Error("the lost gauge does not say 1")
	}
	other, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatalf("a lost sandbox still counts against the quota: %v", err)
	}
	_, err = alice.Sandbox(ctx, sb.ID)
	wantCode(t, err, api.CodeUnavailable)
	_, err = tg.user("bob").LostSandboxes(ctx)
	wantCode(t, err, api.CodeRefused)

	// The node comes back, its sandbox having ended meanwhile: it is
	// reconciled from the node's own listing at once, not at the next tick.
	req := httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/"+sb.ID, nil)
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+n1.token)
	rec := httptest.NewRecorder()
	n1.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("terminating on the node: %d", rec.Code)
	}
	n1.down.Store(false)
	waitFor(t, "the lost sandbox to be reconciled", func() bool {
		_, held := tg.store.OwnerOf(sb.ID)
		return !held
	})
	if lost, err := tg.admin.LostSandboxes(ctx); err != nil || len(lost) != 0 {
		t.Fatalf("lost after the node came back: %+v %v", lost, err)
	}
	if _, err := alice.Sandbox(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
}

// A node that comes back with its sandboxes still running has them back,
// counted again.
func TestNodeLossRecovers(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, func(c *Config) { c.NodeLostAfter = 300 * time.Millisecond }, n1)
	alice := tg.client("alice", "acme", ScopeRead, ScopeCreate)
	ctx := ctxT(t)
	sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	n1.down.Store(true)
	waitFor(t, "lost", func() bool { return len(tg.g.lostSandboxes()) == 1 })
	if u := tg.g.usageOf("acme"); u.Sandboxes != 0 {
		t.Fatalf("a lost sandbox counts: %+v", u)
	}
	n1.down.Store(false)
	waitFor(t, "n1 back", func() bool { return tg.g.nodes.get("n1").isHealthy() })
	if u := tg.g.usageOf("acme"); u.Sandboxes != 1 || len(tg.g.lostSandboxes()) != 0 {
		t.Fatalf("after the node came back: usage %+v, lost %v", u, tg.g.lostSandboxes())
	}
	if got, err := alice.Sandbox(ctx, sb.ID); err != nil || got.State != api.StateRunning {
		t.Fatalf("%+v %v", got, err)
	}
}

// A sandbox recorded on a node no longer configured is lost at once.
func TestSandboxOnARemovedNodeIsLost(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	if err := tg.store.SetOwner("sbx_gone_0123456789abcdef", Owner{User: "alice", Node: "gone"}); err != nil {
		t.Fatal(err)
	}
	lost, err := tg.admin.LostSandboxes(ctxT(t))
	if err != nil || len(lost) != 1 || lost[0].Node != "gone" || !lost[0].NodeDownSince.IsZero() {
		t.Fatalf("%+v %v", lost, err)
	}
	if u := tg.g.usageOf(""); u.Sandboxes != 0 {
		t.Fatalf("a sandbox on a removed node counts: %+v", u)
	}
}
