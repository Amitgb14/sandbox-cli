package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The organisation a browser selects reaches the endpoint on every path
// Studio's server takes — the proxy, its own calls, a launch — as a
// selection only: the endpoint's token is still the context's, never the
// browser's.
func TestStudioPassesTheSelectedOrgOn(t *testing.T) {
	var mu sync.Mutex
	type seen struct{ org, auth string }
	got := map[string]seen{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.URL.Path] = seen{r.Header.Get(api.OrgHeader), r.Header.Get("Authorization")}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sandboxes":[]}`))
	}))
	defer up.Close()
	c, err := api.NewClient(up.URL, "context-token")
	if err != nil {
		t.Fatal(err)
	}
	var launched LaunchRequest
	s := &Server{Client: c.WithOrg("from-context"), Context: "t", Token: testToken,
		Launch: func(_ context.Context, req LaunchRequest) (LaunchResult, error) {
			launched = req
			return LaunchResult{Sandbox: "sbx_x"}, nil
		}}
	st := httptest.NewServer(s.Handler())
	defer st.Close()

	send := func(method, path string, body []byte) {
		req, _ := http.NewRequest(method, st.URL+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set(api.OrgHeader, "acme")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	send("GET", "/api/v1/sandboxes", nil)
	send("GET", "/api/agents/state", nil)
	body, _ := json.Marshal(LaunchRequest{Command: []string{"true"}, Org: "ignored"})
	send("POST", "/api/runs", body)

	mu.Lock()
	defer mu.Unlock()
	if g := got["/v1/sandboxes"]; g.org != "acme" || g.auth != "Bearer context-token" {
		t.Errorf("upstream saw %+v; want the browser's org and the context's token", g)
	}
	if launched.Org != "acme" || launched.Command[0] != "true" {
		t.Errorf("launch: %+v", launched)
	}

	// The WebSocket form: the org query parameter, for a browser cannot give
	// a WebSocket headers; on anything else the parameter means nothing.
	ws := httptest.NewRequest("GET", "/api/ws/attach?sandbox=s&org=acme", nil)
	ws.Header.Set("Connection", "Upgrade")
	ws.Header.Set("Upgrade", "websocket")
	if o := orgOf(ws); o != "acme" {
		t.Errorf("websocket org = %q", o)
	}
	if o := orgOf(httptest.NewRequest("GET", "/api/agents/state?org=acme", nil)); o != "" {
		t.Errorf("a query org on a plain request = %q", o)
	}

	// /api/info says which organisation the context selects.
	r, info := call(t, st.URL, "GET", "/api/info", testToken, "", nil)
	if r.StatusCode != http.StatusOK || info["org"] != "from-context" {
		t.Errorf("info: %d %v", r.StatusCode, info)
	}
}
