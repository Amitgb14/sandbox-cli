package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// orgStandIn is a gateway that knows organisations "acme" (alice owns it)
// and alice's own tenant "t1", and records the X-Sandbox-Org of every
// request.
type orgStandIn struct {
	mu      sync.Mutex
	orgs    []string // the header of each request, by path
	paths   []string
	created []string
	members []api.OrgMemberRequest
}

func (g *orgStandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	org := r.Header.Get(api.OrgHeader)
	g.orgs = append(g.orgs, org)
	g.paths = append(g.paths, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	if org != "" && org != "t1" && org != "acme" {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such organization"}}`))
		return
	}
	cur := org
	if cur == "" {
		cur = "t1"
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/whoami":
		_ = json.NewEncoder(w).Encode(api.Whoami{User: "alice", Tenant: "t1", KeyID: "key_1", Scopes: []string{"sandbox:read"}, Org: cur})
	case r.Method == "GET" && r.URL.Path == "/v1/orgs":
		_ = json.NewEncoder(w).Encode(api.OrgList{Orgs: []api.Org{
			{Name: "t1", Role: "member", Current: cur == "t1"}, {Name: "acme", Role: "owner", Current: cur == "acme"}}})
	case r.Method == "POST" && r.URL.Path == "/v1/orgs":
		var req api.CreateOrgRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		g.created = append(g.created, req.Name)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.Org{Name: req.Name, Role: "owner"})
	case r.Method == "GET" && r.URL.Path == "/v1/orgs/acme/members":
		_ = json.NewEncoder(w).Encode(api.OrgMemberList{Members: []api.OrgMember{{User: "alice", Tenant: "t1", Role: "owner"}}})
	case r.Method == "POST" && r.URL.Path == "/v1/orgs/acme/members":
		var req api.OrgMemberRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		g.members = append(g.members, req)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.OrgMember{User: req.User, Role: req.Role})
	case r.Method == "DELETE" && r.URL.Path == "/v1/orgs/acme/members/bob":
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "GET" && r.URL.Path == "/v1/sandboxes":
		_ = json.NewEncoder(w).Encode(api.SandboxList{Sandboxes: []api.Sandbox{}})
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such organization"}}`))
	}
}

// lastOrg is the header the last request to path carried.
func (g *orgStandIn) lastOrg(path string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := len(g.paths) - 1; i >= 0; i-- {
		if g.paths[i] == path {
			return g.orgs[i], true
		}
	}
	return "", false
}

func TestOrgSelectionReachesEveryRequest(t *testing.T) {
	g := &orgStandIn{}
	gw := httptest.NewServer(g)
	defer gw.Close()
	useEndpoint(t, gw.URL)
	t.Setenv("SANDBOX_ORG", "")

	if _, err := runCLI(t, "ls"); err != nil {
		t.Fatal(err)
	}
	if o, _ := g.lastOrg("GET /v1/sandboxes"); o != "" {
		t.Errorf("no organisation selected, yet sent %q", o)
	}
	if out, err := runCLI(t, "org", "use", "acme"); err != nil || !strings.Contains(out, "acme") {
		t.Fatalf("org use: %q %v", out, err)
	}
	cf, _ := loadContexts()
	if cf.Contexts["test"].Org != "acme" {
		t.Fatalf("the context's org is %q", cf.Contexts["test"].Org)
	}
	if _, err := runCLI(t, "ls"); err != nil {
		t.Fatal(err)
	}
	if o, _ := g.lastOrg("GET /v1/sandboxes"); o != "acme" {
		t.Errorf("after org use, ls sent %q", o)
	}
	// --org over SANDBOX_ORG over the context's.
	t.Setenv("SANDBOX_ORG", "t1")
	_, _ = runCLI(t, "ls")
	if o, _ := g.lastOrg("GET /v1/sandboxes"); o != "t1" {
		t.Errorf("SANDBOX_ORG: sent %q", o)
	}
	_, _ = runCLI(t, "--org", "acme", "ls")
	if o, _ := g.lastOrg("GET /v1/sandboxes"); o != "acme" {
		t.Errorf("--org: sent %q", o)
	}
	t.Setenv("SANDBOX_ORG", "")

	out, err := runCLI(t, "whoami")
	if err != nil || !strings.Contains(out, "org") || !strings.Contains(out, "acme") {
		t.Errorf("whoami: %q %v", out, err)
	}
	// One the key may not select is refused, and the context is left alone.
	if _, err := runCLI(t, "org", "use", "nope"); err == nil || !strings.Contains(err.Error(), "no such organization") {
		t.Errorf("org use nope: %v", err)
	}
	if cf, _ := loadContexts(); cf.Contexts["test"].Org != "acme" {
		t.Errorf("a refused org use changed the context to %q", cf.Contexts["test"].Org)
	}
	if _, err := runCLI(t, "--org", "nope", "whoami"); err == nil || !strings.Contains(err.Error(), "among yours") {
		t.Errorf("whoami in an org the key may not select: %v", err)
	}
	// The key's own tenant needs no header.
	if _, err := runCLI(t, "org", "use", "t1"); err != nil {
		t.Fatal(err)
	}
	if cf, _ := loadContexts(); cf.Contexts["test"].Org != "" {
		t.Errorf("org use of the key's own tenant kept %q", cf.Contexts["test"].Org)
	}
}

func TestOrgCommands(t *testing.T) {
	g := &orgStandIn{}
	gw := httptest.NewServer(g)
	defer gw.Close()
	useEndpoint(t, gw.URL)
	t.Setenv("SANDBOX_ORG", "")

	out, err := runCLI(t, "org", "ls")
	if err != nil || !strings.Contains(out, "acme") || !strings.Contains(out, "owner") {
		t.Errorf("org ls: %q %v", out, err)
	}
	if _, err := runCLI(t, "org", "create", "newco"); err != nil || len(g.created) != 1 || g.created[0] != "newco" {
		t.Errorf("org create: %v %v", g.created, err)
	}
	if _, err := runCLI(t, "--org", "acme", "org", "members", "add", "bob", "--role", "owner", "--tenant", "team-b"); err != nil {
		t.Fatal(err)
	}
	if len(g.members) != 1 || g.members[0] != (api.OrgMemberRequest{User: "bob", Tenant: "team-b", Role: "owner"}) {
		t.Errorf("members add sent %+v", g.members)
	}
	if out, err := runCLI(t, "--org", "acme", "org", "members"); err != nil || !strings.Contains(out, "alice") {
		t.Errorf("members: %q %v", out, err)
	}
	if _, err := runCLI(t, "--org", "acme", "org", "members", "rm", "bob"); err != nil {
		t.Errorf("members rm: %v", err)
	}
	// The key's own tenant has no member list.
	if _, err := runCLI(t, "org", "members"); err == nil || !strings.Contains(err.Error(), "own tenant") {
		t.Errorf("members of the key's own tenant: %v", err)
	}
}

func TestOrgCommandsOnAPlainSandboxd(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such endpoint"}}`))
	}))
	defer plain.Close()
	useEndpoint(t, plain.URL)
	for _, args := range [][]string{{"org", "ls"}, {"org", "create", "x"}, {"org", "use", "x"}, {"org", "members"}} {
		if _, err := runCLI(t, args...); err == nil || !strings.Contains(err.Error(), "plain sandboxd") {
			t.Errorf("%v: %v, want a plain-sandboxd refusal", args, err)
		}
	}
}
