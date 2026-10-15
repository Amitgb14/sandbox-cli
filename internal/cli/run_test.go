package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A run whose sandbox cannot be deleted as it ends says so, naming it and how
// to kill it; one that is deleted says nothing.
func TestARunSaysWhenItsSandboxIsLeftRunning(t *testing.T) {
	refuse := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/sandboxes/sbx_1" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if refuse {
			// What a gateway answers a key without the scope, with a
			// terminal escape in it to show the message is printed safely.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"this API key does not have the sandbox:delete scope\u001b[2J"}}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := api.NewClientWithHTTP(srv.URL, "key", srv.Client())

	var out bytes.Buffer
	removeSandbox(c, "sbx_1", &out)
	got := out.String()
	for _, want := range []string{"could not remove sbx_1", "sandbox:delete scope", "sandbox-cli kill sbx_1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("an escape reached the terminal: %q", got)
	}

	refuse = false
	out.Reset()
	removeSandbox(c, "sbx_1", &out)
	if out.Len() != 0 {
		t.Errorf("a removed sandbox printed %q", out.String())
	}
}
