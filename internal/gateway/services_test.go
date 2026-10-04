package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
)

// The fake backend runs builtins only and has no guest network, so a
// replica's "app" is an httptest server per sandbox, reached by the
// backend's DialGuest: the gateway's health checks and its router go
// through the real tunnel endpoint of a real sandboxd handler to get there.
// A replica created with APP=broken in its environment answers /healthz
// with 500, which is how a broken revision is made.

const appPort = 8080

type fakeApp struct {
	t    *testing.T
	mu   sync.Mutex
	env  map[string]map[string]string
	sick map[string]bool
	srvs map[string]*httptest.Server
}

func newFakeApp(t *testing.T) *fakeApp {
	a := &fakeApp{t: t, env: map[string]map[string]string{}, sick: map[string]bool{}, srvs: map[string]*httptest.Server{}}
	t.Cleanup(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, s := range a.srvs {
			s.Close()
		}
	})
	return a
}

func (a *fakeApp) setSick(id string, sick bool) {
	a.mu.Lock()
	a.sick[id] = sick
	a.mu.Unlock()
}

func (a *fakeApp) server(id string) *httptest.Server {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.srvs[id]; s != nil {
		return s
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		sick := a.sick[id] || a.env[id]["APP"] == "broken"
		a.mu.Unlock()
		if r.URL.Path == "/healthz" {
			if sick {
				http.Error(w, "sick", http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, "ok")
			return
		}
		if r.Header.Get("Upgrade") == "echo" {
			conn, brw, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
			_ = brw.Flush()
			_, _ = io.Copy(conn, brw)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id, "host": r.Host, "path": r.URL.Path,
			"xff": r.Header.Get("X-Forwarded-For"), "xfh": r.Header.Get("X-Forwarded-Host"),
			"xfp": r.Header.Get("X-Forwarded-Proto"), "conn": r.Header.Get("Connection"),
			"keep": r.Header.Get("Keep-Alive"), "auth": r.Header.Get("Authorization")})
	}))
	a.srvs[id] = s
	return s
}

// appBackend is the fake backend with DialGuest reaching the fake app.
type appBackend struct {
	*fake.Backend
	app *fakeApp
}

func (b *appBackend) Create(ctx context.Context, sp backend.Spec) error {
	if err := b.Backend.Create(ctx, sp); err != nil {
		return err
	}
	b.app.mu.Lock()
	b.app.env[sp.ID] = sp.Env
	b.app.mu.Unlock()
	return nil
}

func (b *appBackend) DialGuest(ctx context.Context, id string, port int) (io.ReadWriteCloser, error) {
	if port != appPort {
		return nil, errors.New("connection refused")
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", b.app.server(id).Listener.Addr().String())
}

type svcEnv struct {
	*testGateway
	app    *fakeApp
	router *httptest.Server
	path   string
}

var svcCaps = append([]string{api.CapTunnel}, allCaps...)

func startAppNode(t *testing.T, name string, app *fakeApp) *testNode {
	return startNodeWith(t, name, func(f *fake.Backend) backend.Backend { return &appBackend{Backend: f, app: app} }, svcCaps...)
}

// startServices starts a gateway with fast service ticks and a router over
// nodes named by names, each with the fake app behind its tunnels.
func startServices(t *testing.T, names ...string) *svcEnv {
	t.Helper()
	return startServicesWith(t, nil, names...)
}

// startServicesWith is startServices with the gateway's config changed by mod.
func startServicesWith(t *testing.T, mod func(*Config), names ...string) *svcEnv {
	t.Helper()
	app := newFakeApp(t)
	var nodes []*testNode
	for _, n := range names {
		nodes = append(nodes, startAppNode(t, n, app))
	}
	path := filepath.Join(t.TempDir(), "state.json")
	tg := startGatewayAtWith(t, path, mod, nodes...)
	return &svcEnv{testGateway: tg, app: app, router: startRouter(t, tg), path: path}
}

func svcConfig(cfg *Config) {
	cfg.ServiceInterval = 30 * time.Millisecond
	cfg.Router = RouterConfig{Domain: "apps.test", Scheme: "http"}
}

// startGatewayAt is startGateway on a given state file, so a test can stop
// a gateway and start another on what it left.
func startGatewayAt(t *testing.T, path string, nodes ...*testNode) *testGateway {
	t.Helper()
	return startGatewayAtWith(t, path, nil, nodes...)
}

func startGatewayAtWith(t *testing.T, path string, mod func(*Config), nodes ...*testNode) *testGateway {
	t.Helper()
	st, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Store: st, PollInterval: 100 * time.Millisecond, FailAfter: 2, Logf: t.Logf}
	svcConfig(&cfg)
	for _, n := range nodes {
		cfg.StaticNodes = append(cfg.StaticNodes, n.config())
	}
	if mod != nil {
		mod(&cfg)
	}
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	g.Start(context.Background())
	ts := httptest.NewServer(g.Handler())
	tg := &testGateway{t: t, g: g, ts: ts, store: st, nodes: nodes}
	t.Cleanup(func() { tg.stop() })
	tg.admin = tg.client("root", "", ScopeAdmin)
	return tg
}

// stop closes the gateway and its store; a second stop does nothing.
func (tg *testGateway) stop() {
	if tg.ts == nil {
		return
	}
	tg.ts.Close()
	tg.g.Close()
	tg.store.Close()
	tg.ts = nil
}

func startRouter(t *testing.T, tg *testGateway) *httptest.Server {
	rs := httptest.NewServer(tg.g.RouterHandler())
	t.Cleanup(rs.Close)
	return rs
}

func webSpec(replicas int) api.ServiceSpec {
	return api.ServiceSpec{Name: "web", Replicas: replicas, Port: appPort, Public: true,
		Command: []string{"sleep", "3600"},
		Health:  &api.ServiceHealth{HTTP: "/healthz", EverySecs: 1, TimeoutSecs: 1, Failures: 2}}
}

// waitService polls the service until ok says it is as wanted.
func waitService(t *testing.T, c *api.Client, name string, what string, ok func(api.Service) bool) api.Service {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last api.Service
	var err error
	for time.Now().Before(deadline) {
		last, err = c.Service(context.Background(), name)
		if err == nil && ok(last) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := json.MarshalIndent(last, "", "  ")
	t.Fatalf("service %s never became %s (last error %v):\n%s", name, what, err, data)
	return last
}

func ready(n int) func(api.Service) bool {
	return func(s api.Service) bool { return s.Ready == n && len(s.Replicas) == n }
}

// get asks the router for path with Host host.
func (e *svcEnv) get(host, path string, hdr ...string) (int, map[string]string) {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.router.URL+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Host = host
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := e.router.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &body)
	return resp.StatusCode, body
}

func ids(s api.Service) map[string]bool {
	out := map[string]bool{}
	for _, r := range s.Replicas {
		out[r.Sandbox] = true
	}
	return out
}

func totalCount(e *svcEnv) int {
	n := 0
	for _, tn := range e.nodes {
		n += tn.count()
	}
	return n
}

func TestServiceReplicasAreCreatedAndSpread(t *testing.T) {
	e := startServices(t, "a", "b", "c")
	alice := e.user("alice")
	ctx := ctxT(t)
	sp := webSpec(3)
	sp.Placement.Spread = api.SpreadNode
	sp.Env = map[string]string{"APP": "v1"}
	created, err := alice.DeployService(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.Desired != 3 || created.Spec.Env != nil || len(created.EnvNames) != 1 {
		t.Errorf("created = %+v", created)
	}
	if created.URL != "http://web.apps.test" {
		t.Errorf("url = %q", created.URL)
	}
	s := waitService(t, alice, "web", "ready", ready(3))
	nodes := map[string]bool{}
	for _, r := range s.Replicas {
		nodes[r.Node] = true
		if r.State != api.ReplicaHealthy || r.Revision != 1 || r.LastCheck == nil {
			t.Errorf("replica %+v", r)
		}
		sb, err := alice.Sandbox(ctx, r.Sandbox)
		if err != nil {
			t.Fatal(err)
		}
		if sb.Labels[api.LabelService] != "web" || sb.Labels[api.LabelServiceRevision] != "1" || sb.Labels[LabelOwner] != "alice" {
			t.Errorf("labels = %v", sb.Labels)
		}
		// The command runs as a detached process in each.
		procs, err := alice.Processes(ctx, r.Sandbox)
		if err != nil || len(procs) != 1 || procs[0].Argv[0] != "sleep" || procs[0].State != api.ProcessRunning {
			t.Errorf("processes = %+v, %v", procs, err)
		}
	}
	if len(nodes) != 3 {
		t.Errorf("replicas on %v; want one on each of 3 nodes", nodes)
	}
	// A user still cannot stamp the gateway's service labels themselves.
	_, err = alice.CreateSandbox(ctx, api.CreateSandboxRequest{Labels: map[string]string{api.LabelService: "web"}})
	wantCode(t, err, api.CodeInvalidRequest)
}

func TestServiceReplacesAFailingReplica(t *testing.T) {
	e := startServices(t, "a")
	alice := e.user("alice")
	if _, err := alice.DeployService(ctxT(t), webSpec(2)); err != nil {
		t.Fatal(err)
	}
	s := waitService(t, alice, "web", "ready", ready(2))
	sick := s.Replicas[0].Sandbox
	e.app.setSick(sick, true)
	s = waitService(t, alice, "web", "healed", func(s api.Service) bool { return ready(2)(s) && !ids(s)[sick] })
	if s.Restarts != 1 {
		t.Errorf("restarts = %d; want 1", s.Restarts)
	}
	restarted := 0
	for _, r := range s.Replicas {
		restarted += r.Restarts
	}
	if restarted != 1 {
		t.Errorf("replicas' restarts add to %d; want 1: %+v", restarted, s.Replicas)
	}
	if n := e.nodes[0].count(); n != 2 {
		t.Errorf("the node holds %d sandboxes; the failed replica should be terminated", n)
	}
}

func TestServiceReplacesAReplicaWhoseCommandExits(t *testing.T) {
	e := startServices(t, "a")
	alice := e.user("alice")
	sp := api.ServiceSpec{Name: "worker", Replicas: 1, Command: []string{"true"},
		Health: &api.ServiceHealth{Command: []string{"true"}, EverySecs: 1}}
	if _, err := alice.DeployService(ctxT(t), sp); err != nil {
		t.Fatal(err)
	}
	s := waitService(t, alice, "worker", "restarted", func(s api.Service) bool { return s.Restarts >= 1 })
	if s.Error != "" && !strings.Contains(s.Error, "exited") {
		t.Logf("error: %s", s.Error)
	}
}

func TestServiceCommandHealthCheck(t *testing.T) {
	e := startServices(t, "a")
	alice := e.user("alice")
	sp := api.ServiceSpec{Name: "batch", Replicas: 1, Health: &api.ServiceHealth{Command: []string{"false"}, EverySecs: 1, Failures: 1}}
	if _, err := alice.DeployService(ctxT(t), sp); err != nil {
		t.Fatal(err)
	}
	s := waitService(t, alice, "batch", "replaced", func(s api.Service) bool { return s.Restarts >= 1 })
	if s.Ready != 0 {
		t.Errorf("ready = %d with a health command that always fails", s.Ready)
	}
}

func TestServiceReplacesReplicasOfALostNode(t *testing.T) {
	e := startServices(t, "a", "b")
	alice := e.user("alice")
	sp := webSpec(2)
	sp.Placement.Spread = api.SpreadNode
	if _, err := alice.DeployService(ctxT(t), sp); err != nil {
		t.Fatal(err)
	}
	s := waitService(t, alice, "web", "ready", ready(2))
	var onB string
	for _, r := range s.Replicas {
		if r.Node == "b" {
			onB = r.Sandbox
		}
	}
	if onB == "" {
		t.Fatalf("no replica on b: %+v", s.Replicas)
	}
	e.nodes[1].down.Store(true)
	s = waitService(t, alice, "web", "moved off b", func(s api.Service) bool {
		if !ready(2)(s) {
			return false
		}
		for _, r := range s.Replicas {
			if r.Node != "a" {
				return false
			}
		}
		return true
	})
	if s.Restarts != 0 {
		t.Errorf("restarts = %d; a lost node is not the replica's failure", s.Restarts)
	}
	if got := e.store.Retiring(); len(got) != 1 || got[0] != onB {
		t.Errorf("retiring = %v; want the lost replica %s queued", got, onB)
	}
	// When b answers again, the replica it held is terminated.
	e.nodes[1].down.Store(false)
	deadline := time.Now().Add(20 * time.Second)
	for e.nodes[1].count() != 0 || len(e.store.Retiring()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("b still holds %d sandboxes; retiring %v", e.nodes[1].count(), e.store.Retiring())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServiceRollingUpdate(t *testing.T) {
	e := startServices(t, "a", "b")
	alice := e.user("alice")
	ctx := ctxT(t)
	sp := webSpec(2)
	sp.Env = map[string]string{"APP": "v1"}
	if _, err := alice.DeployService(ctx, sp); err != nil {
		t.Fatal(err)
	}
	waitService(t, alice, "web", "ready", ready(2))

	// The router keeps answering all through the rollout.
	var bad atomic.Int64
	var served atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			req, _ := http.NewRequest(http.MethodGet, e.router.URL+"/", nil)
			req.Host = "web.apps.test"
			resp, err := e.router.Client().Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				bad.Add(1)
			} else {
				served.Add(1)
			}
			if resp != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	sp.Env = map[string]string{"APP": "v2"}
	up, err := alice.UpdateService(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if up.Revision != 2 || up.Rollout == nil || up.Rollout.State != api.RolloutInProgress {
		t.Errorf("after PUT: revision %d rollout %+v", up.Revision, up.Rollout)
	}
	s := waitService(t, alice, "web", "rolled out", func(s api.Service) bool {
		if !ready(2)(s) || s.Rollout == nil || s.Rollout.State != api.RolloutDone {
			return false
		}
		for _, r := range s.Replicas {
			if r.Revision != 2 {
				return false
			}
		}
		return true
	})
	close(stop)
	wg.Wait()
	if s.Serving != 2 || s.Revision != 2 {
		t.Errorf("serving %d revision %d", s.Serving, s.Revision)
	}
	if bad.Load() != 0 {
		t.Errorf("%d of %d requests failed during the rollout", bad.Load(), bad.Load()+served.Load())
	}
	if n := totalCount(e); n != 2 {
		t.Errorf("%d sandboxes left; the old revision's should be terminated", n)
	}

	// A change to the count alone is not a new revision.
	sp.Replicas = 1
	up, err = alice.UpdateService(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if up.Revision != 2 {
		t.Errorf("scaling through PUT made revision %d", up.Revision)
	}
	waitService(t, alice, "web", "scaled", ready(1))
}

func TestServiceBrokenRolloutStopsAndOldReplicasServe(t *testing.T) {
	e := startServices(t, "a", "b")
	alice := e.user("alice")
	ctx := ctxT(t)
	sp := webSpec(2)
	if _, err := alice.DeployService(ctx, sp); err != nil {
		t.Fatal(err)
	}
	before := ids(waitService(t, alice, "web", "ready", ready(2)))
	sp.Env = map[string]string{"APP": "broken"}
	if _, err := alice.UpdateService(ctx, sp); err != nil {
		t.Fatal(err)
	}
	s := waitService(t, alice, "web", "failed", func(s api.Service) bool {
		return s.Rollout != nil && s.Rollout.State == api.RolloutFailed
	})
	if s.Rollout.From != 1 || s.Rollout.To != 2 || !strings.Contains(s.Rollout.Reason, "revision 2") {
		t.Errorf("rollout = %+v", s.Rollout)
	}
	s = waitService(t, alice, "web", "serving revision 1", func(s api.Service) bool {
		if !ready(2)(s) {
			return false
		}
		for _, r := range s.Replicas {
			if r.Revision != 1 {
				return false
			}
		}
		return true
	})
	for id := range ids(s) {
		if !before[id] {
			t.Errorf("replica %s is new; the old replicas should have kept serving", id)
		}
	}
	if s.Serving != 1 || s.Revision != 2 {
		t.Errorf("serving %d revision %d", s.Serving, s.Revision)
	}
	for range 4 {
		if code, _ := e.get("web.apps.test", "/"); code != http.StatusOK {
			t.Errorf("router answered %d after a failed rollout", code)
		}
	}
	if n := totalCount(e); n != 2 {
		t.Errorf("%d sandboxes; the broken revision's should be terminated", n)
	}
}

func TestServiceScale(t *testing.T) {
	e := startServices(t, "a")
	alice := e.user("alice")
	ctx := ctxT(t)
	if _, err := alice.DeployService(ctx, webSpec(1)); err != nil {
		t.Fatal(err)
	}
	waitService(t, alice, "web", "ready", ready(1))
	if s, err := alice.ScaleService(ctx, "web", 3); err != nil || s.Desired != 3 {
		t.Fatalf("scale: %+v %v", s, err)
	}
	waitService(t, alice, "web", "scaled up", ready(3))
	if _, err := alice.ScaleService(ctx, "web", 1); err != nil {
		t.Fatal(err)
	}
	waitService(t, alice, "web", "scaled down", ready(1))
	deadline := time.Now().Add(10 * time.Second)
	for e.nodes[0].count() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("node holds %d sandboxes after scaling to 1", e.nodes[0].count())
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, err := alice.ScaleService(ctx, "web", MaxServiceReplicas+1)
	wantCode(t, err, api.CodeInvalidRequest)
}

func TestServiceDeleteTerminatesReplicas(t *testing.T) {
	e := startServices(t, "a", "b")
	alice := e.user("alice")
	ctx := ctxT(t)
	if _, err := alice.DeployService(ctx, webSpec(2)); err != nil {
		t.Fatal(err)
	}
	waitService(t, alice, "web", "ready", ready(2))
	if err := alice.DeleteService(ctx, "web"); err != nil {
		t.Fatal(err)
	}
	if n := totalCount(e); n != 0 {
		t.Errorf("%d sandboxes after the delete", n)
	}
	_, err := alice.Service(ctx, "web")
	wantCode(t, err, api.CodeNotFound)
	wantCode(t, alice.DeleteService(ctx, "web"), api.CodeNotFound)
	if list, err := alice.Services(ctx); err != nil || len(list) != 0 {
		t.Errorf("list = %v, %v", list, err)
	}
	// The name is free again.
	if _, err := alice.DeployService(ctx, webSpec(0)); err != nil {
		t.Errorf("redeploy: %v", err)
	}
}

func TestServiceRouter(t *testing.T) {
	e := startServices(t, "a", "b")
	alice := e.user("alice")
	bob := e.client("bob", "acme", ScopeRead, ScopeCreate, ScopeDelete)
	ctx := ctxT(t)
	if _, err := alice.DeployService(ctx, webSpec(2)); err != nil {
		t.Fatal(err)
	}
	if s, err := bob.DeployService(ctx, webSpec(1)); err != nil || s.URL != "http://web--acme.apps.test" {
		t.Fatalf("bob's web: %+v %v", s, err)
	}
	priv := webSpec(1)
	priv.Name, priv.Public = "priv", false
	if _, err := alice.DeployService(ctx, priv); err != nil {
		t.Fatal(err)
	}
	aliceWeb := ids(waitService(t, alice, "web", "ready", ready(2)))
	bobWeb := ids(waitService(t, bob, "web", "ready", ready(1)))
	waitService(t, alice, "priv", "ready", ready(1))

	// Round robin over alice's replicas, and only hers.
	seen := map[string]int{}
	for range 6 {
		code, body := e.get("web.apps.test:80", "/hello", "X-Forwarded-For", "6.6.6.6", "Authorization", "Bearer app")
		if code != http.StatusOK {
			t.Fatalf("router: %d", code)
		}
		if !aliceWeb[body["id"]] {
			t.Fatalf("web.apps.test reached %s, not one of alice's replicas", body["id"])
		}
		seen[body["id"]]++
		if body["host"] != "web.apps.test:80" || body["xfh"] != "web.apps.test:80" || body["xfp"] != "http" || body["path"] != "/hello" {
			t.Errorf("forwarded as %v", body)
		}
		if body["xff"] != "127.0.0.1" {
			t.Errorf("X-Forwarded-For = %q; the client's own must be replaced", body["xff"])
		}
		if body["auth"] != "Bearer app" {
			t.Errorf("the app's own Authorization did not pass: %q", body["auth"])
		}
	}
	if len(seen) != 2 || seen[firstKey(seen)] != 3 {
		t.Errorf("round robin: %v", seen)
	}
	// Hop-by-hop headers are not passed on.
	if _, body := e.get("web.apps.test", "/", "Connection", "Keep-Alive, X-Drop", "Keep-Alive", "timeout=5", "X-Drop", "1"); body["keep"] != "" {
		t.Errorf("Keep-Alive reached the replica: %v", body)
	}
	// Another tenant's service of the same name is its own.
	if code, body := e.get("web--acme.apps.test", "/"); code != http.StatusOK || !bobWeb[body["id"]] {
		t.Errorf("web--acme: %d %v", code, body)
	}
	for _, host := range []string{"priv.apps.test", "nope.apps.test", "web.other.test", "x.web.apps.test",
		"web--nobody.apps.test", "apps.test", "web--acme--x.apps.test", "WEB--ACME.apps.test.evil"} {
		if code, _ := e.get(host, "/"); code != http.StatusNotFound {
			t.Errorf("%s: %d; want 404", host, code)
		}
	}
	// Case does not matter to DNS, so not here either.
	if code, body := e.get("WEB--ACME.Apps.Test.", "/"); code != http.StatusOK || !bobWeb[body["id"]] {
		t.Errorf("upper case: %d %v", code, body)
	}

	// An upgrade passes through.
	conn, err := net.Dial("tcp", e.router.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: web.apps.test\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	fmt.Fprint(conn, "ping\n")
	if line, err := br.ReadString('\n'); err != nil || line != "ping\n" {
		t.Errorf("echo: %q %v", line, err)
	}

	// No healthy replica: 503.
	for id := range aliceWeb {
		e.app.setSick(id, true)
	}
	if _, err := alice.ScaleService(ctx, "web", 0); err != nil {
		t.Fatal(err)
	}
	waitService(t, alice, "web", "empty", ready(0))
	if code, _ := e.get("web.apps.test", "/"); code != http.StatusServiceUnavailable {
		t.Errorf("no replicas: %d; want 503", code)
	}
}

func firstKey(m map[string]int) string {
	for k := range m {
		return k
	}
	return ""
}

func TestServiceOwnershipAndValidation(t *testing.T) {
	e := startServices(t, "a")
	alice := e.user("alice")
	bob := e.user("bob")
	ctx := ctxT(t)
	if _, err := alice.DeployService(ctx, webSpec(1)); err != nil {
		t.Fatal(err)
	}
	// Another user's service is not found, whatever is asked of it.
	_, err := bob.Service(ctx, "web")
	wantCode(t, err, api.CodeNotFound)
	_, err = bob.ScaleService(ctx, "web", 5)
	wantCode(t, err, api.CodeNotFound)
	_, err = bob.UpdateService(ctx, webSpec(5))
	wantCode(t, err, api.CodeNotFound)
	wantCode(t, bob.DeleteService(ctx, "web"), api.CodeNotFound)
	if list, err := bob.Services(ctx); err != nil || len(list) != 0 {
		t.Errorf("bob lists %v, %v", list, err)
	}
	// Names are unique per tenant: bob is in alice's (the default one).
	_, err = bob.DeployService(ctx, webSpec(1))
	wantCode(t, err, api.CodeConflict)
	// Another tenant has its own namespace.
	carol := e.client("carol", "acme", ScopeRead, ScopeCreate)
	if _, err := carol.DeployService(ctx, webSpec(0)); err != nil {
		t.Errorf("carol in acme: %v", err)
	}
	// An admin sees every service, and reaches another tenant's by ?tenant=.
	if list, err := e.admin.Services(ctx); err != nil || len(list) != 2 {
		t.Errorf("admin lists %d, %v", len(list), err)
	}
	resp := e.raw(http.MethodGet, "/v1/services/web?tenant=acme", e.adminKey(), "")
	var got api.Service
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || got.Owner != "carol" {
		t.Errorf("admin ?tenant=acme: %d %+v", resp.StatusCode, got)
	}
	// ...and a user's ?tenant= is ignored.
	if s, err := alice.Service(ctx, "web"); err != nil || s.Owner != "alice" {
		t.Errorf("alice: %+v %v", s, err)
	}

	// Scopes.
	reader := e.client("alice", "", ScopeRead)
	_, err = reader.DeployService(ctx, api.ServiceSpec{Name: "x"})
	wantCode(t, err, api.CodeRefused)
	creator := e.client("alice", "", ScopeRead, ScopeCreate)
	wantCode(t, creator.DeleteService(ctx, "web"), api.CodeRefused)
	if _, err := reader.Service(ctx, "web"); err != nil {
		t.Errorf("read: %v", err)
	}

	for _, c := range []struct {
		name string
		sp   api.ServiceSpec
		code string
	}{
		{"name with --", api.ServiceSpec{Name: "a--b"}, api.CodeInvalidRequest},
		{"name upper", api.ServiceSpec{Name: "Web"}, api.CodeInvalidRequest},
		{"name trailing dash", api.ServiceSpec{Name: "web-"}, api.CodeInvalidRequest},
		{"too many", api.ServiceSpec{Name: "x", Replicas: MaxServiceReplicas + 1}, api.CodeInvalidRequest},
		{"both checks", api.ServiceSpec{Name: "x", Port: 1, Health: &api.ServiceHealth{HTTP: "/", Command: []string{"true"}}}, api.CodeInvalidRequest},
		{"http without port", api.ServiceSpec{Name: "x", Health: &api.ServiceHealth{HTTP: "/"}}, api.CodeInvalidRequest},
		{"path with a newline", api.ServiceSpec{Name: "x", Port: 1, Health: &api.ServiceHealth{HTTP: "/a\r\nX-Evil: 1"}}, api.CodeInvalidRequest},
		{"path with a space", api.ServiceSpec{Name: "x", Port: 1, Health: &api.ServiceHealth{HTTP: "/a b"}}, api.CodeInvalidRequest},
		{"public without port", api.ServiceSpec{Name: "x", Public: true}, api.CodeInvalidRequest},
		{"reserved env", api.ServiceSpec{Name: "x", Env: map[string]string{"LD_PRELOAD": "/x.so"}}, api.CodeRefused},
		{"bad spread", api.ServiceSpec{Name: "x", Placement: api.ServicePlacement{Spread: "zone"}}, api.CodeInvalidRequest},
		{"bad network", api.ServiceSpec{Name: "x", Network: &api.NetworkPolicy{Mode: "wide"}}, api.CodeInvalidRequest},
		{"secrets", api.ServiceSpec{Name: "x", Secrets: []string{"GITHUB_TOKEN"}}, api.CodeUnsupported},
	} {
		_, err := alice.DeployService(ctx, c.sp)
		if !api.IsCode(err, c.code) {
			t.Errorf("%s: %v; want %s", c.name, err, c.code)
		}
	}
	// A tenant that is not a DNS label cannot have public services.
	odd := e.client("dan", "Acme.Corp", ScopeCreate)
	_, err = odd.DeployService(ctx, webSpec(0))
	wantCode(t, err, api.CodeInvalidRequest)
	priv := webSpec(0)
	priv.Public = false
	if _, err := odd.DeployService(ctx, priv); err != nil {
		t.Errorf("a private service in an odd tenant: %v", err)
	}
	// Unknown fields are refused.
	resp = e.raw(http.MethodPost, "/v1/services", e.key("alice"), `{"name":"y","replicas":1,"replica":2}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: %d", resp.StatusCode)
	}
	// A PUT may not rename.
	sp := webSpec(1)
	sp.Name = "other"
	resp = e.raw(http.MethodPut, "/v1/services/web", e.key("alice"), mustJSON(t, sp))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("rename: %d", resp.StatusCode)
	}
}

func mustJSON(t *testing.T, v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// key issues a full key for user and returns its secret.
func (tg *testGateway) key(user string) string {
	secret, _, err := tg.store.CreateKey(user, "", []string{ScopeRead, ScopeCreate, ScopeDelete})
	if err != nil {
		tg.t.Fatal(err)
	}
	return secret
}

func (tg *testGateway) adminKey() string {
	secret, _, err := tg.store.CreateKey("root", "", []string{ScopeAdmin})
	if err != nil {
		tg.t.Fatal(err)
	}
	return secret
}

func TestServiceResumesAfterARestart(t *testing.T) {
	app := newFakeApp(t)
	nodes := []*testNode{startAppNode(t, "a", app), startAppNode(t, "b", app)}
	path := filepath.Join(t.TempDir(), "state.json")
	tg := startGatewayAt(t, path, nodes...)
	secret := tg.key("alice")
	alice := api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client())
	ctx := ctxT(t)
	sp := webSpec(2)
	sp.Env = map[string]string{"APP": "v1"}
	if _, err := alice.DeployService(ctx, sp); err != nil {
		t.Fatal(err)
	}
	before := ids(waitService(t, alice, "web", "ready", ready(2)))
	tg.stop()

	tg2 := startGatewayAt(t, path, nodes...)
	alice = api.NewClientWithHTTP(tg2.ts.URL, secret, tg2.ts.Client())
	s := waitService(t, alice, "web", "ready again", ready(2))
	for id := range ids(s) {
		if !before[id] {
			t.Errorf("replica %s is new; a restarted gateway should resume the ones it had", id)
		}
	}
	if n := nodes[0].count() + nodes[1].count(); n != 2 {
		t.Errorf("%d sandboxes after the restart", n)
	}
	if s.EnvNames[0] != "APP" || s.Serving != 1 {
		t.Errorf("resumed as %+v", s)
	}
	// It keeps working: a scale after the restart is honoured.
	if _, err := alice.ScaleService(ctx, "web", 3); err != nil {
		t.Fatal(err)
	}
	waitService(t, alice, "web", "scaled", ready(3))
}

func TestServiceRestartResumesARollout(t *testing.T) {
	app := newFakeApp(t)
	nodes := []*testNode{startAppNode(t, "a", app)}
	path := filepath.Join(t.TempDir(), "state.json")
	tg := startGatewayAt(t, path, nodes...)
	secret := tg.key("alice")
	alice := api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client())
	ctx := ctxT(t)
	sp := webSpec(2)
	if _, err := alice.DeployService(ctx, sp); err != nil {
		t.Fatal(err)
	}
	waitService(t, alice, "web", "ready", ready(2))
	// Stop the gateway the moment the rollout is recorded, before it ends.
	tg.g.Close()
	sp.Env = map[string]string{"APP": "v2"}
	resp := tg.raw(http.MethodPut, "/v1/services/web", secret, mustJSON(t, sp))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT: %d", resp.StatusCode)
	}
	tg.ts.Close()
	tg.store.Close()
	tg.ts = nil

	tg2 := startGatewayAt(t, path, nodes...)
	alice = api.NewClientWithHTTP(tg2.ts.URL, secret, tg2.ts.Client())
	waitService(t, alice, "web", "rolled out", func(s api.Service) bool {
		if !ready(2)(s) || s.Rollout == nil || s.Rollout.State != api.RolloutDone {
			return false
		}
		for _, r := range s.Replicas {
			if r.Revision != 2 {
				return false
			}
		}
		return true
	})
}

// A service's secrets are opened from the secret store for each replica it
// makes, as a job's are for each run: in the replica's environment, and in
// no record, response or state file. A spec naming one the tenant does not
// have is refused, and a secret removed later stops new replicas rather than
// starting one without it.
func TestServiceSecretsReachReplicasOnly(t *testing.T) {
	e := startServicesWith(t, withKey, "n1")
	ctx := ctxT(t)
	alice := e.client("alice", "", ScopeRead, ScopeCreate, ScopeDelete, ScopeSecretsWrite)
	const secret, plain = "svc-secret-81c4e", "svc-plain-3a9d0"

	sp := webSpec(2)
	sp.Env = map[string]string{"PLAIN": plain}
	sp.Secrets = []string{"API_TOKEN"}
	_, err := alice.DeployService(ctx, sp)
	wantCode(t, err, api.CodeInvalidRequest) // no such secret yet
	if err := alice.SetSecret(ctx, "API_TOKEN", secret); err != nil {
		t.Fatal(err)
	}
	both := sp
	both.Env = map[string]string{"API_TOKEN": "x"}
	_, err = alice.DeployService(ctx, both)
	wantCode(t, err, api.CodeInvalidRequest)
	twice := sp
	twice.Secrets = []string{"API_TOKEN", "API_TOKEN"}
	_, err = alice.DeployService(ctx, twice)
	wantCode(t, err, api.CodeInvalidRequest)
	reserved := sp
	reserved.Secrets = []string{"LD_PRELOAD"}
	_, err = alice.DeployService(ctx, reserved)
	wantCode(t, err, api.CodeInvalidRequest)

	created, err := alice.DeployService(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	s := waitService(t, alice, "web", "ready", ready(2))
	e.app.mu.Lock()
	for _, r := range s.Replicas {
		env := e.app.env[r.Sandbox]
		if env["API_TOKEN"] != secret || env["PLAIN"] != plain {
			t.Errorf("replica %s's environment lacks the secret or the env: %v", r.Sandbox, env)
		}
	}
	e.app.mu.Unlock()
	for what, v := range map[string]any{"the create's answer": created, "GET": s} {
		if raw, _ := json.Marshal(v); strings.Contains(string(raw), secret) || strings.Contains(string(raw), plain) {
			t.Errorf("%s holds a value: %s", what, raw)
		}
	}
	if state, _ := os.ReadFile(e.path); strings.Contains(string(state), secret) {
		t.Error("the state file holds the secret's value")
	}

	// Removed, it stops the next replica, and the service says why.
	if err := alice.DeleteSecret(ctx, "API_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.ScaleService(ctx, "web", 3); err != nil {
		t.Fatal(err)
	}
	s = waitService(t, alice, "web", "refusing a replica", func(s api.Service) bool { return strings.Contains(s.Error, "API_TOKEN") })
	if len(s.Replicas) != 2 {
		t.Fatalf("%d replicas with the secret gone", len(s.Replicas))
	}
}
