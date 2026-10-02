package conformance

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// serveFake runs sandboxd's handler over the fake backend in-process.
func serveFake(t *testing.T, pol spec.Policy, caps ...string) *api.Client {
	t.Helper()
	s := &server.Server{Backend: fake.New(caps...), Policy: pol, Token: "conformance-token"}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return api.NewClientWithHTTP(ts.URL, "conformance-token", ts.Client())
}

// The suite against the fake, with the default policy and every capability the
// fake can pretend to have.
func TestFakeDefaultPolicy(t *testing.T) {
	Run(t, serveFake(t, spec.DefaultPolicy(), api.CapNetworkPolicyUpdate))
}

// The same suite against an endpoint that cannot update network policy and
// permits no names beyond its default — so the capability-gated and
// may_allow-gated branches of the suite run too, not only their happy paths.
func TestFakeNarrowPolicy(t *testing.T) {
	pol := spec.DefaultPolicy()
	pol.Network.MayAllow = nil
	Run(t, serveFake(t, pol))
}

// TestEndpoint runs the suite against a real sandboxd, when one is named. It is
// how the maintainer checks a Mac or a KVM Linux host (docs/testing/end-to-end.md,
// row 11):
//
//	SANDBOX_CONFORMANCE_ENDPOINT=unix:///path/to/sandboxd.sock \
//	SANDBOX_CONFORMANCE_TOKEN=… go test ./internal/api/conformance -run TestEndpoint -v
func TestEndpoint(t *testing.T) {
	endpoint := os.Getenv("SANDBOX_CONFORMANCE_ENDPOINT")
	if endpoint == "" {
		t.Skip("SANDBOX_CONFORMANCE_ENDPOINT not set")
	}
	c, err := api.NewClient(endpoint, os.Getenv("SANDBOX_CONFORMANCE_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	Run(t, c)
}
