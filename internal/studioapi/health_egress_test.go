package studioapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// /health answers without a token on purpose — it is the one thing a client
// lacking one can still ask. That exemption is why the *resolved* allowlist must
// not ride along on it: on a daemon reachable beyond loopback, anything that can
// open a socket would be able to enumerate the internal hostnames this machine
// talks to. The count is not sensitive and is what a screen renders.
func TestHealthDoesNotPublishTheAllowlistToAnyone(t *testing.T) {
	s, _ := newTestServer(t)
	s.Token = "test-token"
	s.Session.Cfg.Network.Mode = "allowlist"
	s.Session.Cfg.Network.Allow = []string{"internal.example.com", "vault.corp"}

	read := func(auth bool) EgressPosture {
		t.Helper()
		req := newTestRequest(t, http.MethodGet, healthPath, nil)
		if auth {
			req.Header.Set("Authorization", "Bearer "+s.Token)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", healthPath, rec.Code, rec.Body.String())
		}
		var out HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Egress
	}

	anon := read(false)
	if anon.Mode != "allowlist" {
		t.Errorf("mode = %q, want allowlist — the posture itself is not a secret", anon.Mode)
	}
	if anon.Domains == 0 {
		t.Error("no domain count for an unauthenticated caller: a count discloses nothing and a screen needs it")
	}
	if len(anon.Allow) != 0 {
		t.Errorf("an unauthenticated caller was given the domain names: %v", anon.Allow)
	}

	authed := read(true)
	if len(authed.Allow) == 0 {
		t.Error("an authenticated caller was not given the names it is entitled to")
	}
}
