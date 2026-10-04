package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// adminStandIn answers the gateway admin endpoints the gateway commands call,
// and records what they sent.
type adminStandIn struct {
	mu   sync.Mutex
	seen []string
	body []string
}

func (a *adminStandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var b strings.Builder
	if r.Body != nil {
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		b.Write(buf[:n])
	}
	a.seen = append(a.seen, r.Method+" "+r.URL.Path)
	a.body = append(a.body, b.String())
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	switch r.Method + " " + r.URL.Path {
	case "GET /v1/whoami":
		_ = enc.Encode(api.Whoami{User: "root", Scopes: []string{"admin"}})
	case "GET /v1/admin/nodes":
		_ = enc.Encode(api.NodeList{Nodes: []api.NodeInfo{{Name: "n1", Healthy: true,
			Status: &api.NodeStatus{Version: "0.9.0", Running: 3, Cordoned: true}}}})
	case "POST /v1/admin/nodes/n1/cordon":
		_ = enc.Encode(api.NodeInfo{Name: "n1"})
	case "POST /v1/admin/nodes/n1/drain":
		var req api.DrainRequest
		_ = json.Unmarshal([]byte(b.String()), &req)
		res := api.DrainResult{Node: "n1", Cordoned: true, Remaining: 2, Terminated: []string{}}
		if req.Terminate {
			res = api.DrainResult{Node: "n1", Cordoned: true, Terminated: []string{"sbx_n1_0000000000000001"},
				Failed: []api.DrainFailure{{ID: "sbx_n1_0000000000000002", Error: "the node did not answer"}}, Remaining: 1}
		}
		_ = enc.Encode(res)
	case "GET /v1/admin/lost":
		_ = enc.Encode(api.LostList{Sandboxes: []api.LostSandbox{{ID: "sbx_n2_0000000000000003", User: "ana", Node: "n2"}}})
	case "GET /v1/admin/audit":
		if r.URL.Query().Get("limit") != "5" || r.URL.Query().Get("since") == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_request","message":"want limit and since"}}`))
			return
		}
		_ = enc.Encode(api.AuditList{Entries: []api.AuditEntry{
			{Time: time.Now(), Kind: "ssh", Action: "ssh.session", User: "ana", KeyID: "sk_1", Session: "shell", Sandbox: "sbx_x", Result: "ok"},
			{Time: time.Now(), Kind: "api", Action: "DELETE /v1/sandboxes/{ref}", User: "ana\x1b[31m", KeyID: "key_1", Status: 204},
		}})
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such endpoint"}}`))
	}
}

func TestGatewayAdminCommands(t *testing.T) {
	a := &adminStandIn{}
	gw := httptest.NewServer(a)
	defer gw.Close()
	useEndpoint(t, gw.URL)

	out, err := runCLI(t, "gateway", "nodes")
	if err != nil || !strings.Contains(out, "n1") || !strings.Contains(out, "0.9.0") || !strings.Contains(out, "true") {
		t.Fatalf("nodes: %q %v", out, err)
	}
	if out, err := runCLI(t, "gateway", "cordon", "n1"); err != nil || !strings.Contains(out, "cordoned") {
		t.Fatalf("cordon: %q %v", out, err)
	}
	if out, err := runCLI(t, "gateway", "uncordon", "n1"); err != nil || !strings.Contains(out, "uncordoned") {
		t.Fatalf("uncordon: %q %v", out, err)
	}
	if out, err := runCLI(t, "gateway", "drain", "n1"); err != nil || !strings.Contains(out, "2 sandboxes still running") {
		t.Fatalf("drain: %q %v", out, err)
	}
	out, err = runCLI(t, "gateway", "drain", "n1", "--terminate")
	if err == nil || !strings.Contains(out, "1 sandboxes terminated") || !strings.Contains(out, "sbx_n1_0000000000000002") {
		t.Fatalf("drain --terminate with a failure: %q %v", out, err)
	}
	if out, err := runCLI(t, "gateway", "lost"); err != nil || !strings.Contains(out, "sbx_n2_0000000000000003") || !strings.Contains(out, "(node removed)") {
		t.Fatalf("lost: %q %v", out, err)
	}
	out, err = runCLI(t, "gateway", "audit", "--since", "1h", "--limit", "5")
	if err != nil || !strings.Contains(out, "ssh.session shell") || !strings.Contains(out, "204") {
		t.Fatalf("audit: %q %v", out, err)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatalf("audit printed an escape sequence: %q", out)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	var cordons, drains []string
	for i, s := range a.seen {
		switch s {
		case "POST /v1/admin/nodes/n1/cordon":
			cordons = append(cordons, a.body[i])
		case "POST /v1/admin/nodes/n1/drain":
			drains = append(drains, a.body[i])
		}
	}
	if len(cordons) != 2 || !strings.Contains(cordons[0], `"cordoned":true`) || !strings.Contains(cordons[1], `"cordoned":false`) {
		t.Errorf("cordon bodies %q", cordons)
	}
	if len(drains) != 2 || !strings.Contains(drains[0], `"terminate":false`) || !strings.Contains(drains[1], `"terminate":true`) {
		t.Errorf("drain bodies %q", drains)
	}
}

func TestGatewayAdminCommandsOnAPlainSandboxd(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such endpoint"}}`))
	}))
	defer plain.Close()
	useEndpoint(t, plain.URL)
	_, err := runCLI(t, "gateway", "drain", "n1")
	if err == nil || !strings.Contains(err.Error(), "plain sandboxd") {
		t.Fatalf("%v", err)
	}
}
