package studioapi

import (
	"context"
	"net/http"
	"testing"
)

// A session is named by **id**, and the daemon finds the file. The rule is the
// same one the repository registry and the file browser keep, and it is what
// stops a transcript reader from becoming a way to read any file on the host.
func TestSessionReadersRefuseAnythingButAKnownID(t *testing.T) {
	s, _ := newTestServer(t)

	for _, ref := range []string{
		"not-a-real-session",
		"/etc/passwd",
		"../../../../etc/passwd",
		"~/.ssh/id_ed25519",
	} {
		t.Run(ref, func(t *testing.T) {
			for _, path := range []string{
				"/v1/agents/claude/sessions/" + ref,
				"/v1/agents/claude/sessions/" + ref + "/raw",
			} {
				rec := doJSON(t, s, http.MethodGet, path, nil, nil)
				if rec.Code == http.StatusOK {
					t.Fatalf("GET %s answered 200; a transcript reader that resolves anything but a known id is a file reader", path)
				}
				if body := rec.Body.String(); len(body) > 400 {
					t.Errorf("GET %s returned %d bytes for a refusal — is it serving content?", path, len(body))
				}
			}
		})
	}
}

// The listing's default is the resume picker's question and must stay narrow:
// a session that cannot be reopened is an action that fails. `scope=all` is the
// reading question. The two are different, and the difference is reported on
// every row rather than left for a client to infer.
func TestSessionListingDefaultsToResumableOnly(t *testing.T) {
	s, _ := newTestServer(t)

	var narrow, wide SessionListResponse
	if rec := doJSON(t, s, http.MethodGet, "/v1/agents/claude/sessions", nil, &narrow); rec.Code != http.StatusOK {
		t.Fatalf("default listing = %d", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodGet, "/v1/agents/claude/sessions?scope=all", nil, &wide); rec.Code != http.StatusOK {
		t.Fatalf("scope=all listing = %d", rec.Code)
	}
	for _, sess := range narrow.Sessions {
		if sess.Store != storeSandbox {
			t.Errorf("the default listing offered a %q session; it feeds a resume picker, and only the sandbox-owned store can be reopened", sess.Store)
		}
		if !sess.Resumable {
			t.Errorf("session %s in the default listing is not resumable", sess.ID)
		}
	}
	if len(wide.Sessions) < len(narrow.Sessions) {
		t.Errorf("scope=all returned %d sessions, fewer than the default's %d", len(wide.Sessions), len(narrow.Sessions))
	}
}

// Studio does half of routing, and the half it does not do must not be implied.
//
// The probe runs before a launch; the retry cannot, because this API launches
// detached and returns as soon as the container exists — nothing is left to see
// an exit code. These cases pin the contract without touching the network:
// opencode has no provider host, so it is chosen unprobed, which is also the
// rule that keeps a provider-agnostic agent from being skipped as "down".
func TestRouteAgentChoosesWithoutProbingWhatItCannotProbe(t *testing.T) {
	s, _ := newTestServer(t)

	t.Run("one agent is not a chain and is never routed", func(t *testing.T) {
		got, from, why, err := s.routeAgent(context.Background(), RunCreateRequest{Agent: "opencode"})
		if err != nil {
			t.Fatal(err)
		}
		if got != "opencode" || from != "" || why != "" {
			t.Errorf("chose %q routedFrom %q (%q); a single agent must run as itself with nothing to explain", got, from, why)
		}
	})

	t.Run("an unprobeable primary is used rather than skipped", func(t *testing.T) {
		// opencode is provider-agnostic — its EnvAllow spans four vendors because
		// the user picks — so there is no host whose health means anything about
		// it. Unknown is not down.
		got, from, _, err := s.routeAgent(context.Background(),
			RunCreateRequest{Agent: "opencode", Fallback: []string{"gemini"}})
		if err != nil {
			t.Fatal(err)
		}
		if got != "opencode" || from != "" {
			t.Errorf("chose %q (from %q); an agent with nothing to probe must not be treated as unavailable", got, from)
		}
	})

	t.Run("a fallback with no verified headless mode is refused", func(t *testing.T) {
		// A Studio run is detached, so an agent that stops to ask permission
		// hangs with nobody to answer — and it would hang in the fallback slot,
		// where nobody is looking at all.
		//
		// `goose` rather than `cline`: cline was the example here until it got a
		// descriptor, and an example that quietly becomes supported turns this
		// into a test that passes for the wrong reason. Any adapter without a
		// verified headless argv will do; when goose gains one, move this to the
		// next.
		if _, _, _, err := s.routeAgent(context.Background(),
			RunCreateRequest{Agent: "opencode", Fallback: []string{"goose"}}); err == nil {
			t.Error("an agent with no verified headless argv was accepted as a fallback for a detached run")
		}
	})
}
