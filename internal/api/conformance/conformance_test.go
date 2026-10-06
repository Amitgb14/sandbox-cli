package conformance

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// serveFake runs sandboxd's handler over the fake backend in-process, with an
// audit log when withAudit is set.
func serveFake(t *testing.T, pol spec.Policy, withAudit bool, caps ...string) *api.Client {
	t.Helper()
	// Usage sampled every second, so the metrics test waits seconds, not
	// the default ten-second intervals.
	s := &server.Server{Backend: fake.New(caps...), Policy: pol, Token: "conformance-token", MetricsInterval: time.Second}
	if withAudit {
		s.Audit = audit.NewLog(filepath.Join(t.TempDir(), "events.jsonl"))
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return api.NewClientWithHTTP(ts.URL, "conformance-token", ts.Client())
}

// The suite against the fake, with the default policy and every capability the
// fake can pretend to have.
func TestFakeDefaultPolicy(t *testing.T) {
	caps := []string{api.CapNetworkPolicyUpdate, api.CapEgressAllowlist, api.CapSuspend, api.CapMemorySnapshot, api.CapVolumes}
	Run(t, serveFake(t, spec.DefaultPolicyFor(capSet(caps...)), true, caps...))
}

// An endpoint that snapshots disks rather than whole machines, as the macOS
// backend does: forks get the files, boot afresh, and may choose resources.
func TestFakeDiskSnapshots(t *testing.T) {
	caps := []string{api.CapEgressAllowlist, api.CapDiskSnapshot}
	pol := spec.DefaultPolicyFor(capSet(caps...))
	// Short enough that the suite waits for a schedule to run.
	pol.Limits.MinSnapshotEverySecs = 1
	Run(t, serveFake(t, pol, false, caps...))
}

// An endpoint that cannot filter egress — a backend without host networking.
// Its policy is narrowed to none (spec.FitTo), and the suite's network tests
// must hold there too: every allowlist request refused, nothing served open.
func TestFakeNoEgress(t *testing.T) {
	pol, _ := spec.DefaultPolicy().FitTo(map[string]bool{})
	Run(t, serveFake(t, pol, false))
}

// The same suite against an endpoint that cannot update network policy and
// permits no names beyond its default — so the capability-gated and
// may_allow-gated branches of the suite run too, not only their happy paths.
func TestFakeNarrowPolicy(t *testing.T) {
	pol := spec.DefaultPolicyFor(capSet(api.CapEgressAllowlist))
	pol.Network.MayAllow = nil
	Run(t, serveFake(t, pol, false, api.CapEgressAllowlist))
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
	if ca := os.Getenv("SANDBOX_CONFORMANCE_CA"); ca != "" {
		pem, rerr := os.ReadFile(ca)
		if rerr != nil {
			t.Fatal(rerr)
		}
		c, err = api.NewClientWithCA(endpoint, os.Getenv("SANDBOX_CONFORMANCE_TOKEN"), pem)
	}
	if err != nil {
		t.Fatal(err)
	}
	Run(t, c)
}

// capSet is a capability list as the map a backend reports.
func capSet(caps ...string) map[string]bool {
	m := map[string]bool{}
	for _, c := range caps {
		m[c] = true
	}
	return m
}
