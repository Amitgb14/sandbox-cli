package studioapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// `allow` is the one network field a request carries, and it may only ever
// narrow. On a daemon configured to reach nothing it does the opposite: BuildSpec
// reads a non-empty Allow as switching the allowlist on, which promotes the
// container off `--network none` onto the sandbox bridge. One domain in a request
// would hand a run networking the daemon was configured not to give it.
func TestARequestCannotTurnNetworkingBackOn(t *testing.T) {
	s, fr := newTestServer(t)
	s.Session.Cfg.Network.Mode = "none"

	rec := doJSON(t, s, http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent: "claude", Prompt: "hello", Allow: []string{"internal.example.com"},
	}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /v1/runs with allow on a none daemon = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if len(fr.started) != 0 {
		t.Error("a container was started for a refused request")
	}
	if body := rec.Body.String(); !strings.Contains(body, "never widen") {
		t.Errorf("the refusal does not say which direction is allowed: %s", body)
	}
}

// And the ordinary case still works: adding domains to an allowlist, or to an
// unrestricted daemon (where it *tightens* the run), is a narrowing.
func TestAddingDomainsIsAllowedWhereItNarrows(t *testing.T) {
	for _, mode := range []string{"allowlist", "default", ""} {
		s, _ := newTestServer(t)
		s.Session.Cfg.Network.Mode = mode
		s.Session.Cfg.Profile = config.ProfileDev

		rec := doJSON(t, s, http.MethodPost, "/v1/runs", RunCreateRequest{
			Agent: "claude", Prompt: "hello", Allow: []string{"internal.example.com"},
		}, nil)
		if rec.Code != http.StatusCreated {
			t.Errorf("mode %q: POST /v1/runs = %d, want 201: %s", mode, rec.Code, rec.Body.String())
		}
	}
}
