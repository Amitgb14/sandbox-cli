package gateway

import (
	"context"
	"encoding/json"
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
	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// allCaps is every capability the fake can pretend to have, as the
// conformance suite's own default run uses.
var allCaps = []string{api.CapNetworkPolicyUpdate, api.CapEgressAllowlist, api.CapSuspend, api.CapMemorySnapshot, api.CapVolumes}

// testNode is a sandboxd stand-in: the real server over the fake backend,
// with GET /v1/node and POST /v1/node/cordon answered here. The real
// sandboxd's own node endpoints are another change; this branch must not
// depend on them, and a test that controls the status can make a node
// cordoned, unhealthy or misnamed at will.
type testNode struct {
	t        *testing.T
	name     string
	token    string
	be       *fake.Backend
	h        http.Handler
	ts       *httptest.Server
	tokenDir string

	capacity api.NodeResources
	cordoned atomic.Bool
	down     atomic.Bool            // /v1/node answers 500
	reportAs atomic.Pointer[string] // the node name it reports; "" is a standalone sandboxd
	requests atomic.Int64

	mu       sync.Mutex
	badAuths []string // Authorization headers that were not the node token
	paths    []string // every request path the node was sent
}

func startNode(t *testing.T, name string, caps ...string) *testNode {
	t.Helper()
	return startNodeWith(t, name, nil, caps...)
}

// startNodeWith is startNode with the fake backend wrapped — in a backend
// that can dial a guest port, for a tunnel followed end to end.
func startNodeWith(t *testing.T, name string, wrap func(*fake.Backend) backend.Backend, caps ...string) *testNode {
	t.Helper()
	tn := &testNode{t: t, name: name, token: "node-token-" + name + "-0123456789",
		capacity: api.NodeResources{CPUs: 64, MemoryMB: 64 << 10, DiskMB: 1 << 20}}
	empty := ""
	tn.reportAs.Store(&empty)
	tn.be = fake.New(caps...)
	pol := spec.DefaultPolicyFor(capSet(caps...))
	// So a snapshot schedule runs within a test, not five minutes after it.
	pol.Limits.MinSnapshotEverySecs = 1
	var be backend.Backend = tn.be
	if wrap != nil {
		be = wrap(tn.be)
	}
	s := &server.Server{Backend: be, Policy: pol, Token: tn.token,
		Audit: audit.NewLog(filepath.Join(t.TempDir(), "events.jsonl"))}
	tn.h = s.Handler()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/node", tn.status)
	mux.HandleFunc("POST /v1/node/cordon", tn.cordon)
	mux.Handle("/", tn.h)
	tn.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tn.requests.Add(1)
		tn.mu.Lock()
		tn.paths = append(tn.paths, r.URL.Path)
		tn.mu.Unlock()
		if got := r.Header.Get("Authorization"); got != "Bearer "+tn.token {
			tn.mu.Lock()
			tn.badAuths = append(tn.badAuths, got)
			tn.mu.Unlock()
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(tn.ts.Close)
	tn.tokenDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(tn.tokenDir, "token"), []byte(tn.token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		tn.mu.Lock()
		defer tn.mu.Unlock()
		for _, a := range tn.badAuths {
			t.Errorf("node %s was sent a credential that is not its token: %q", name, a)
		}
	})
	return tn
}

func (tn *testNode) config() NodeConfig {
	return NodeConfig{Name: tn.name, Endpoint: tn.ts.URL, TokenFile: filepath.Join(tn.tokenDir, "token")}
}

// inner calls the wrapped server directly, as the node itself.
func (tn *testNode) inner(path string, out any) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "127.0.0.1"
	req.Header.Set("Authorization", "Bearer "+tn.token)
	rec := httptest.NewRecorder()
	tn.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		tn.t.Errorf("node %s %s: %d %s", tn.name, path, rec.Code, rec.Body.String())
		return
	}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		tn.t.Error(err)
	}
}

func (tn *testNode) status(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+tn.token {
		writeErr(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing or wrong bearer token")
		return
	}
	if tn.down.Load() {
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "down")
		return
	}
	var caps api.Capabilities
	tn.inner("/v1/capabilities", &caps)
	var list api.SandboxList
	tn.inner("/v1/sandboxes", &list)
	st := api.NodeStatus{Node: *tn.reportAs.Load(), Version: "test", Capabilities: caps,
		Capacity: tn.capacity, Free: tn.capacity, Cordoned: tn.cordoned.Load()}
	for _, sb := range list.Sandboxes {
		if sb.State == api.StateTerminated {
			continue
		}
		st.Running++
		st.Free.CPUs -= sb.CPUs
		st.Free.MemoryMB -= sb.MemoryMB
		st.Free.DiskMB -= sb.DiskMB
	}
	writeJSON(w, http.StatusOK, st)
}

func (tn *testNode) cordon(w http.ResponseWriter, r *http.Request) {
	var req api.CordonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	tn.cordoned.Store(req.Cordoned)
	w.WriteHeader(http.StatusNoContent)
}

// sawPath reports whether the node was sent a request whose path contains s.
func (tn *testNode) sawPath(s string) bool {
	tn.mu.Lock()
	defer tn.mu.Unlock()
	for _, p := range tn.paths {
		if strings.Contains(p, s) {
			return true
		}
	}
	return false
}

// count is how many live sandboxes the node's backend holds.
func (tn *testNode) count() int { return tn.be.Count() }

type testGateway struct {
	t     *testing.T
	g     *Gateway
	ts    *httptest.Server
	store *FileStore
	nodes []*testNode
	admin *api.Client
}

func startGateway(t *testing.T, mod func(*Config), nodes ...*testNode) *testGateway {
	t.Helper()
	st, err := OpenFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := Config{Store: st, PollInterval: 100 * time.Millisecond, FailAfter: 2, Logf: t.Logf}
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
	t.Cleanup(g.Close)
	ts := httptest.NewServer(g.Handler())
	t.Cleanup(ts.Close)
	tg := &testGateway{t: t, g: g, ts: ts, store: st, nodes: nodes}
	tg.admin = tg.client("root", "", ScopeAdmin)
	return tg
}

// client issues a key and returns a client holding it.
func (tg *testGateway) client(user, tenant string, scopes ...string) *api.Client {
	tg.t.Helper()
	secret, _, err := tg.store.CreateKey(user, tenant, scopes)
	if err != nil {
		tg.t.Fatal(err)
	}
	return api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client())
}

func (tg *testGateway) user(name string) *api.Client {
	return tg.client(name, "", ScopeRead, ScopeCreate, ScopeDelete, ScopeSSH)
}

// raw sends one request with the given key and returns the response.
func (tg *testGateway) raw(method, path, key, body string, hdr ...string) *http.Response {
	tg.t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req, err := http.NewRequest(method, tg.ts.URL+path, rd)
	if err != nil {
		tg.t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := tg.ts.Client().Do(req)
	if err != nil {
		tg.t.Fatal(err)
	}
	return resp
}

func capSet(caps ...string) map[string]bool {
	m := map[string]bool{}
	for _, c := range caps {
		m[c] = true
	}
	return m
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if !api.IsCode(err, code) {
		t.Fatalf("error = %v; want %s", err, code)
	}
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
