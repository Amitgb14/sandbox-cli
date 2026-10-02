package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

func newServer(token string) *Server {
	return &Server{Backend: fake.New(), Policy: spec.DefaultPolicy(), Token: token}
}

func do(t *testing.T, h http.Handler, method, target, host, origin, token, ct string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// DNS rebinding: a page whose own name resolves to 127.0.0.1 satisfies the
// browser's same-origin policy, and its Host header is what gives it away.
func TestGuardRefusesANonLoopbackHost(t *testing.T) {
	h := newServer("tok").Handler()
	for host, want := range map[string]int{
		"127.0.0.1:7070":      http.StatusOK,
		"localhost":           http.StatusOK,
		"[::1]:7070":          http.StatusOK,
		"attacker.example":    http.StatusForbidden,
		"127.0.0.1.nip.io:80": http.StatusForbidden,
	} {
		if got := do(t, h, "GET", "/v1/capabilities", host, "", "tok", "", nil).Code; got != want {
			t.Errorf("Host %s: %d, want %d", host, got, want)
		}
	}
	s := newServer("tok")
	s.AllowedHosts = []string{"sandbox.internal"}
	if got := do(t, s.Handler(), "GET", "/v1/capabilities", "sandbox.internal", "", "tok", "", nil).Code; got != http.StatusOK {
		t.Errorf("a configured host was refused: %d", got)
	}
}

// CSRF: refusing to reflect an origin only stops a page reading the answer; the
// request itself must be refused, or a "simple" cross-origin POST creates a
// sandbox anyway.
func TestGuardRefusesAnUnlistedOrigin(t *testing.T) {
	s := newServer("")
	h := s.Handler()
	rec := do(t, h, "POST", "/v1/sandboxes", "127.0.0.1", "https://evil.example", "", "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin create: %d", rec.Code)
	}
	if n := len(s.sandboxes); n != 0 {
		t.Fatalf("a refused request still created %d sandbox(es)", n)
	}
	if got := do(t, h, "GET", "/v1/capabilities", "127.0.0.1:7070", "http://127.0.0.1:7070", "", "", nil).Code; got != http.StatusOK {
		t.Errorf("same-origin request refused: %d", got)
	}
}

func TestGuardRequiresTheToken(t *testing.T) {
	h := newServer("right").Handler()
	for token, want := range map[string]int{"": 401, "wrong": 401, "righ": 401, "right": 200} {
		if got := do(t, h, "GET", "/v1/capabilities", "127.0.0.1", "", token, "", nil).Code; got != want {
			t.Errorf("token %q: %d, want %d", token, got, want)
		}
	}
	// Health answers without one, so a client can learn it needs one.
	if got := do(t, h, "GET", healthPath, "127.0.0.1", "", "", "", nil).Code; got != 200 {
		t.Errorf("health without a token: %d", got)
	}
	// A token only in the query string is not accepted.
	if got := do(t, h, "GET", "/v1/capabilities?token=right", "127.0.0.1", "", "", "", nil).Code; got != 401 {
		t.Errorf("query-string token accepted: %d", got)
	}
}

func TestGuardRequiresTheContentType(t *testing.T) {
	h := newServer("").Handler()
	body := []byte(`{}`)
	if got := do(t, h, "POST", "/v1/sandboxes", "127.0.0.1", "", "", "text/plain", body).Code; got != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain JSON body: %d", got)
	}
	if got := do(t, h, "POST", "/v1/sandboxes", "127.0.0.1", "", "", "application/json", body).Code; got != http.StatusCreated {
		t.Errorf("JSON create: %d", got)
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	h := newServer("").Handler()
	rec := do(t, h, "POST", "/v1/sandboxes", "127.0.0.1", "", "", "application/json", []byte(`{"privileged":true}`))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), api.CodeInvalidRequest) {
		t.Errorf("unknown field: %d %s", rec.Code, rec.Body)
	}
}

// A process that prints without end must not grow the server without end, and
// the cut must be reported.
func TestOutputIsCappedAndTheCutReported(t *testing.T) {
	l := newOutputLog()
	w := l.writer("stdout")
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < 10; i++ {
		if n, err := w.Write(chunk); n != len(chunk) || err != nil {
			t.Fatalf("write %d: %d, %v", i, n, err)
		}
	}
	l.finish(0)
	out, _, truncated := l.collect()
	if len(out) != maxStreamBytes || !truncated {
		t.Fatalf("kept %d bytes, truncated=%v; want %d and true", len(out), truncated, maxStreamBytes)
	}
}

// A follower that arrives after exit still gets everything, then the exit code.
func TestFollowAfterExit(t *testing.T) {
	l := newOutputLog()
	_, _ = l.writer("stdout").Write([]byte("a"))
	_, _ = l.writer("stderr").Write([]byte("b"))
	l.finish(3)
	var got []api.OutputEvent
	if err := l.follow(context.Background(), func(ev api.OutputEvent) error { got = append(got, ev); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || string(got[0].Data) != "a" || got[1].Stream != "stderr" || *got[2].ExitCode != 3 {
		t.Fatalf("events: %+v", got)
	}
}
