package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// WithOrg sends OrgHeader on every kind of request: JSON calls, a followed
// stream, and an upgrade (attach, tunnel). One that went without it would act
// in the key's own tenant instead, silently.
func TestWithOrgSendsTheHeaderOnEveryRequest(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get(OrgHeader)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	base, err := NewClient(srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	c := base.WithOrg("acme")
	if c.Org() != "acme" || base.Org() != "" {
		t.Fatalf("WithOrg changed the original: %q %q", c.Org(), base.Org())
	}
	ctx := context.Background()
	_, _ = c.Whoami(ctx)
	_ = c.FollowOutput(ctx, "s", 1, func(OutputEvent) error { return nil })
	_, _ = c.Tunnel(ctx, "s", 80) // not upgraded by the stub, but sent
	_, _ = base.Sandboxes(ctx)
	mu.Lock()
	defer mu.Unlock()
	for path, want := range map[string]string{
		"/v1/whoami":                         "acme",
		"/v1/sandboxes/s/processes/1/output": "acme",
		"/v1/sandboxes/s/tunnel":             "acme",
		"/v1/sandboxes":                      "",
	} {
		if got, ok := seen[path]; !ok || got != want {
			t.Errorf("%s: %s = %q (sent %v); want %q", path, OrgHeader, got, ok, want)
		}
	}
}
