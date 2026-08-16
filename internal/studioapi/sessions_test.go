package studioapi

import (
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
