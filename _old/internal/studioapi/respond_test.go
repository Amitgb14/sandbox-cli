package studioapi

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 501 is a capability statement, not an incident: "this deployment has no
// history index", "no agent on PATH to refresh with". Clients are built to
// handle both — the history caller falls back to computing the same numbers
// from /v1/audit — so logging one per request turned a working setup into a
// page of what looked like errors. Every other 5xx still logs, because nobody
// is watching the response.
func TestNotImplementedIsAnsweredButNotLogged(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	rec := httptest.NewRecorder()
	writeError(rec, http.StatusNotImplemented, errStub("no history index"))
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no history index") {
		t.Errorf("the refusal must still reach the client: %s", rec.Body.String())
	}
	if buf.Len() != 0 {
		t.Errorf("501 was logged as a server fault: %s", buf.String())
	}

	buf.Reset()
	writeError(httptest.NewRecorder(), http.StatusBadGateway, errStub("engine unreachable"))
	if buf.Len() == 0 {
		t.Error("502 was not logged; only 501 is expected to be quiet")
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }
