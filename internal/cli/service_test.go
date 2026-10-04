package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// serviceStandIn is a gateway's /v1/services, enough for the CLI: it holds
// specs by name and answers as a gateway would.
type serviceStandIn struct {
	mu   sync.Mutex
	svcs map[string]api.Service
	seen []string
}

func (s *serviceStandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, r.Method+" "+r.URL.Path)
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	name := strings.TrimPrefix(r.URL.Path, "/v1/services/")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/services", r.Method == http.MethodPut:
		var sp api.ServiceSpec
		if err := json.NewDecoder(r.Body).Decode(&sp); err != nil {
			reply(400, api.ErrorBody{Error: api.ErrorDetail{Code: api.CodeInvalidRequest, Message: err.Error()}})
			return
		}
		old, exists := s.svcs[sp.Name]
		if r.Method == http.MethodPost && exists {
			reply(409, api.ErrorBody{Error: api.ErrorDetail{Code: api.CodeConflict, Message: "exists"}})
			return
		}
		svc := api.Service{Spec: sp, Revision: 1, Serving: 1, Desired: sp.Replicas, URL: "https://" + sp.Name + ".apps.example"}
		for k := range sp.Env {
			svc.EnvNames = append(svc.EnvNames, k)
		}
		code := 201
		if exists {
			svc.Revision, svc.Serving, code = old.Revision+1, old.Serving, 200
			svc.Rollout = &api.ServiceRollout{State: api.RolloutInProgress, From: old.Serving, To: svc.Revision}
		}
		s.svcs[sp.Name] = svc
		reply(code, svc)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/services":
		list := api.ServiceList{Services: []api.Service{}}
		for _, v := range s.svcs {
			list.Services = append(list.Services, v)
		}
		reply(200, list)
	case r.Method == http.MethodPost && strings.HasSuffix(name, "/scale"):
		var req api.ScaleServiceRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		v := s.svcs[strings.TrimSuffix(name, "/scale")]
		v.Desired = req.Replicas
		s.svcs[v.Spec.Name] = v
		reply(200, v)
	case r.Method == http.MethodGet:
		v, ok := s.svcs[name]
		if !ok {
			reply(404, api.ErrorBody{Error: api.ErrorDetail{Code: api.CodeNotFound, Message: "no such service"}})
			return
		}
		v.Replicas = []api.ServiceReplica{{Sandbox: "sbx_0123456789abcdef", Node: "n1", Revision: 1, State: api.ReplicaHealthy, Healthy: true, LastError: "\x1b[31mred"}}
		reply(200, v)
	case r.Method == http.MethodDelete:
		delete(s.svcs, name)
		w.WriteHeader(204)
	default:
		reply(404, api.ErrorBody{Error: api.ErrorDetail{Code: api.CodeNotFound, Message: "no such endpoint"}})
	}
}

func TestServiceCommands(t *testing.T) {
	gw := &serviceStandIn{svcs: map[string]api.Service{}}
	ts := httptest.NewServer(gw)
	defer ts.Close()
	useEndpoint(t, ts.URL)
	dir := t.TempDir()
	file := filepath.Join(dir, "service.yaml")
	write := func(s string) {
		if err := os.WriteFile(file, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`name: web
image: ghcr.io/you/web:1
command: [./serve, --port, "8080"]
replicas: 3
resources: { cpus: 1, memory_mb: 1024 }
port: 8080
health: { http: /healthz, every_secs: 10, failures: 3 }
env: { MODE: prod }
placement: { spread: node }
public: true
`)
	out, err := runCLI(t, "service", "deploy", "-f", file)
	if err != nil || !strings.Contains(out, "created web, revision 1") || !strings.Contains(out, "url: https://web.apps.example") {
		t.Fatalf("deploy: %v\n%s", err, out)
	}
	sp := gw.svcs["web"].Spec
	if sp.Replicas != 3 || sp.Port != 8080 || sp.Health.HTTP != "/healthz" || sp.Env["MODE"] != "prod" ||
		sp.Placement.Spread != "node" || !sp.Public || sp.Resources.MemoryMB != 1024 || len(sp.Command) != 3 {
		t.Errorf("sent %+v", sp)
	}
	// Deploying again updates it.
	out, err = runCLI(t, "service", "deploy", "-f", file)
	if err != nil || !strings.Contains(out, "updated web, revision 2") || !strings.Contains(out, "rolling out revision 2 over revision 1") {
		t.Fatalf("redeploy: %v\n%s", err, out)
	}
	if gw.seen[len(gw.seen)-1] != "PUT /v1/services/web" {
		t.Errorf("requests: %v", gw.seen)
	}

	out, err = runCLI(t, "service", "ls")
	if err != nil || !strings.Contains(out, "web") || !strings.Contains(out, "0/3") || !strings.Contains(out, "in_progress") {
		t.Errorf("ls: %v\n%s", err, out)
	}
	out, err = runCLI(t, "service", "get", "web")
	if err != nil || !strings.Contains(out, "sbx_0123456789abcdef") || !strings.Contains(out, "rollout:   in_progress, 1 -> 2") || !strings.Contains(out, "env:       MODE") {
		t.Errorf("get: %v\n%s", err, out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("get printed an escape sequence from the server: %q", out)
	}
	out, err = runCLI(t, "service", "scale", "web", "5")
	if err != nil || !strings.Contains(out, "5 replicas wanted") {
		t.Errorf("scale: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "service", "scale", "web", "-1"); err == nil {
		t.Error("a negative scale was sent")
	}
	if _, err := runCLI(t, "service", "rm", "web"); err != nil || len(gw.svcs) != 0 {
		t.Errorf("rm: %v %v", err, gw.svcs)
	}
	if _, err := runCLI(t, "service", "get", "web"); !api.IsCode(err, api.CodeNotFound) {
		t.Errorf("get after rm: %v", err)
	}
}

func TestServiceFileRefusesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"misspelt key":        "name: web\npublc: true\n",
		"misspelt nested key": "name: web\nhealth: { htp: /healthz }\n",
		"no name":             "replicas: 2\n",
		"empty":               "",
	} {
		f := filepath.Join(dir, "s.yaml")
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readServiceFile(f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	f := filepath.Join(dir, "ok.yaml")
	_ = os.WriteFile(f, []byte("name: web\nreplicas: 2\n"), 0o600)
	if sp, err := readServiceFile(f); err != nil || sp.Replicas != 2 {
		t.Errorf("%+v %v", sp, err)
	}
}
