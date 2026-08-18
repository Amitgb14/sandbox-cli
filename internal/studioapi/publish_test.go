package studioapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// An agent's dev server can be reached, which is what publishing is for.
//
// It is also the one launch option that opens a way *in*, so what it opens is
// worth pinning: a bare port binds loopback on the daemon's host — not every
// interface, which is where sandbox-cli differs from `docker -p` — and under an
// allowlist the firewall's default-deny inbound chain gains a carve-out for
// exactly that port, without which the published port would not answer at all.
func TestALaunchCanPublishAPort(t *testing.T) {
	s, fr := newTestServer(t)
	s.Session.Cfg.Network.Mode = "allowlist"

	rec := doJSON(t, s, http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent: "claude", Prompt: "run the dev server", Publish: []string{"8000"},
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/runs with publish = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(fr.started) != 1 {
		t.Fatalf("started %d containers", len(fr.started))
	}
	spec := fr.started[0]

	var published string
	for _, p := range spec.Ports {
		if strings.Contains(p, "8000") {
			published = p
		}
	}
	if published == "" {
		t.Fatalf("the port never reached the container spec: %v", spec.Ports)
	}
	if !strings.HasPrefix(published, "127.0.0.1:") {
		t.Errorf("published as %q, want a loopback bind — a bare port must not be served to the network", published)
	}
	// And the firewall is told, or the port is open on the host and closed in the
	// container: reachable, and answering nothing.
	if got := spec.Env["SANDBOX_INGRESS_PORTS"]; !strings.Contains(got, "8000") {
		t.Errorf("SANDBOX_INGRESS_PORTS = %q, want the published port carved out", got)
	}
}

// A malformed spec is refused before a container exists, by the same normaliser
// the CLI uses — one place decides what a port spec means.
func TestABadPortSpecIsRefused(t *testing.T) {
	s, fr := newTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent: "claude", Prompt: "hello", Publish: []string{"not-a-port"},
	}, nil)
	// 400, not 502: a typo in a port is the caller's, and reporting it as a bad
	// gateway sends somebody to look at the daemon.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /v1/runs with a bad port = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if len(fr.started) != 0 {
		t.Error("a container was started for a refused request")
	}
}

// The normaliser is shared, not reimplemented: an explicit address still says
// what it says.
func TestAnExplicitAddressIsKept(t *testing.T) {
	got, err := sandbox.NormalizePublish([]string{"0.0.0.0:8000:8000"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "0.0.0.0:") {
		t.Errorf("normalised to %v, want the address the caller wrote", got)
	}
}

// The two refusals that are the daemon's configuration rather than the caller's
// mistake, answered before a container is attempted.
//
// Both were arriving through Session.Start as a 502 — "the daemon is broken" for
// a well-formed request that was deliberately declined, which is exactly the
// confusion the malformed-port precheck above was added to remove. A refusal is
// only useful if it says which side has to change.
func TestPublishingIsRefusedWhereTheDaemonWouldRefuseIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*Server)
		says string
	}{
		{
			name: "prod",
			set:  func(s *Server) { s.Session.Cfg.Profile = config.ProfileProd },
			says: "prod profile",
		},
		{
			name: "no network",
			set:  func(s *Server) { s.Session.Cfg.Network.Mode = "none" },
			says: "no network to publish from",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, fr := newTestServer(t)
			tc.set(s)

			rec := doJSON(t, s, http.MethodPost, "/v1/runs", RunCreateRequest{
				Agent: "claude", Prompt: "run the dev server", Publish: []string{"8000"},
			}, nil)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("= %d, want 422 rather than a 502 from the launch: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.says) {
				t.Errorf("the refusal does not say why: %s", rec.Body.String())
			}
			if len(fr.started) != 0 {
				t.Error("a container was started for a refused request")
			}
		})
	}
}
