package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// failingCreate is the fake with a create that fails the way a real backend
// does: an error naming a host path, with text from elsewhere in it.
type failingCreate struct{ *fake.Backend }

const hostDetail = "linking vmlinux into the jail: link /var/lib/sandboxd/vmlinux /tmp/jail/root/vmlinux: invalid cross-device link"

func (failingCreate) Create(context.Context, backend.Spec) error {
	return errors.New(hostDetail + "\nsandboxd: forged line")
}

// An error the caller is told only "the backend failed" about is logged for
// the operator in full; before, it was in neither the answer nor the log.
// The caller still sees nothing of it, and it cannot forge a log line.
func TestAGenericBackendFailureIsLoggedForTheOperator(t *testing.T) {
	be := failingCreate{fake.New()}
	var logs []string
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), Token: "tok",
		Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}
	rec := do(t, s.Handler(), "POST", "/v1/sandboxes", "127.0.0.1", "", "tok", "application/json", []byte(`{}`))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "/var/lib") {
		t.Fatalf("the answer: %d %s; want 500 without the host path", rec.Code, rec.Body.String())
	}
	got := strings.Join(logs, "\n")
	if !strings.Contains(got, "POST /v1/sandboxes: the backend failed") || !strings.Contains(got, hostDetail) {
		t.Fatalf("the log: %q; want the method, path and the error", got)
	}
	for _, l := range logs {
		if strings.Contains(l, "\n") {
			t.Fatalf("a logged error broke the line: %q", l)
		}
	}
}

// An error the answer already explains is not logged again.
func TestAnExplainedBackendErrorIsNotLogged(t *testing.T) {
	var logs []string
	s := &Server{Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}
	rec := do(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.backendErr(w, r, fmt.Errorf("%w: sandbox", backend.ErrNotFound))
	}), "GET", "/v1/sandboxes/x", "127.0.0.1", "", "", "", nil)
	if rec.Code != http.StatusNotFound || len(logs) != 0 {
		t.Fatalf("%d, logs %q", rec.Code, logs)
	}
}
