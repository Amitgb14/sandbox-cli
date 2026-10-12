package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

const loginPassword = "ghp_not-a-real-token"

func createWithLogin(t *testing.T, s *Server, l api.RegistryLogin) (int, string) {
	t.Helper()
	body, _ := json.Marshal(api.CreateSandboxRequest{Image: "ghcr.io/team/app:1", RegistryLogin: &l})
	rec := do(t, s.Handler(), "POST", "/v1/sandboxes", "127.0.0.1", "", "tok", "application/json", body)
	return rec.Code, rec.Body.String()
}

// A create's registry login reaches the backend's pull and nothing else:
// not the answer, the listing or the audit log.
func TestARegistryLoginReachesTheBackendOnly(t *testing.T) {
	be := fake.New(api.CapRegistryLogin)
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), Token: "tok", Audit: audit.NewLog(logPath)}
	code, out := createWithLogin(t, s, api.RegistryLogin{Registry: "ghcr.io", Username: "amit", Password: loginPassword})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, out)
	}
	var sbx api.Sandbox
	if err := json.Unmarshal([]byte(out), &sbx); err != nil {
		t.Fatal(err)
	}
	got, ok := be.RegistryLogin(sbx.ID)
	if want := (backend.RegistryLogin{Registry: "ghcr.io", Username: "amit", Password: loginPassword}); !ok || got != want {
		t.Fatalf("the backend got %+v, %v; want %+v", got, ok, want)
	}
	list := do(t, s.Handler(), "GET", "/v1/sandboxes", "127.0.0.1", "", "tok", "", nil).Body.String()
	logged, _ := os.ReadFile(logPath)
	for what, text := range map[string]string{"the answer": out, "the listing": list, "the audit log": string(logged)} {
		if strings.Contains(text, loginPassword) {
			t.Errorf("%s holds the password: %s", what, text)
		}
	}
}

// A backend that cannot pull with a login refuses a create that carries
// one, rather than pulling without it.
func TestARegistryLoginIsRefusedWhereItCannotBeUsed(t *testing.T) {
	be := fake.New()
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), Token: "tok"}
	code, out := createWithLogin(t, s, api.RegistryLogin{Registry: "ghcr.io", Username: "amit", Password: loginPassword})
	if code != http.StatusNotImplemented {
		t.Fatalf("create: %d %s; want 501", code, out)
	}
	if be.Count() != 0 {
		t.Fatal("a refused create made a sandbox")
	}
}

func TestARegistryLoginIsChecked(t *testing.T) {
	be := fake.New(api.CapRegistryLogin)
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), Token: "tok"}
	for name, l := range map[string]api.RegistryLogin{
		"no registry":        {Username: "amit", Password: loginPassword},
		"a path":             {Registry: "ghcr.io/team", Username: "amit", Password: loginPassword},
		"no user":            {Registry: "ghcr.io", Password: loginPassword},
		"a colon in user":    {Registry: "ghcr.io", Username: "a:b", Password: loginPassword},
		"no password":        {Registry: "ghcr.io", Username: "amit"},
		"a line in password": {Registry: "ghcr.io", Username: "amit", Password: loginPassword + "\nX-Evil: 1"},
	} {
		code, out := createWithLogin(t, s, l)
		if code != http.StatusBadRequest {
			t.Errorf("%s: %d %s; want 400", name, code, out)
		}
		if strings.Contains(out, loginPassword) {
			t.Errorf("%s: the refusal holds the password: %s", name, out)
		}
	}
	if be.Count() != 0 {
		t.Fatal("a refused create made a sandbox")
	}
}
