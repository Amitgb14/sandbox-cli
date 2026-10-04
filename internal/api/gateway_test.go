package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// recorded is one request the stand-in gateway saw.
type recorded struct {
	method, path, auth string
	body               map[string]any
}

// gatewayStub answers every request with reply (as JSON, status 200) and
// records what it was sent.
func gatewayStub(t *testing.T, reply any) (*Client, *[]recorded) {
	t.Helper()
	var seen []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recorded{method: r.Method, path: r.URL.EscapedPath(), auth: r.Header.Get("Authorization")}
		data, _ := io.ReadAll(r.Body)
		if len(data) > 0 {
			if err := json.Unmarshal(data, &rec.body); err != nil {
				t.Errorf("%s %s: body is not JSON: %q", r.Method, r.URL.Path, data)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("%s %s: Content-Type %q", r.Method, r.URL.Path, ct)
			}
		}
		seen = append(seen, rec)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, "gw-key")
	if err != nil {
		t.Fatal(err)
	}
	return c, &seen
}

func TestGatewayMethodsSendWhatTheEndpointsTake(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		call   func(*Client) error
		method string
		path   string
		body   map[string]any // nil: no body
	}{
		{"whoami", func(c *Client) error { _, err := c.Whoami(ctx); return err }, "GET", "/v1/whoami", nil},
		{"ssh info", func(c *Client) error { _, err := c.SSHInfo(ctx); return err }, "GET", "/v1/ssh", nil},
		{"add ssh key", func(c *Client) error { _, err := c.AddSSHKey(ctx, "ssh-ed25519 AAAA me", ""); return err },
			"POST", "/v1/ssh-keys", map[string]any{"key": "ssh-ed25519 AAAA me"}},
		{"add ssh key for one sandbox", func(c *Client) error { _, err := c.AddSSHKey(ctx, "ssh-ed25519 AAAA", "demo"); return err },
			"POST", "/v1/ssh-keys", map[string]any{"key": "ssh-ed25519 AAAA", "sandbox": "demo"}},
		{"ssh keys", func(c *Client) error { _, err := c.SSHKeys(ctx); return err }, "GET", "/v1/ssh-keys", nil},
		{"remove ssh key", func(c *Client) error { return c.RemoveSSHKey(ctx, "k/1") }, "DELETE", "/v1/ssh-keys/k%2F1", nil},
		{"ssh access", func(c *Client) error { _, err := c.SSHAccess(ctx, "demo", 900); return err },
			"POST", "/v1/sandboxes/demo/ssh-access", map[string]any{"ttl_secs": float64(900)}},
		{"ssh access with the default ttl", func(c *Client) error { _, err := c.SSHAccess(ctx, "demo", 0); return err },
			"POST", "/v1/sandboxes/demo/ssh-access", map[string]any{}},
		{"create key", func(c *Client) error {
			_, err := c.CreateKey(ctx, CreateKeyRequest{User: "ana", Tenant: "t1", Scopes: []string{"sandboxes"}})
			return err
		}, "POST", "/v1/admin/keys", map[string]any{"user": "ana", "tenant": "t1", "scopes": []any{"sandboxes"}}},
		{"keys", func(c *Client) error { _, err := c.Keys(ctx); return err }, "GET", "/v1/admin/keys", nil},
		{"revoke key", func(c *Client) error { return c.RevokeKey(ctx, "key_1") }, "DELETE", "/v1/admin/keys/key_1", nil},
		{"nodes", func(c *Client) error { _, err := c.GatewayNodes(ctx); return err }, "GET", "/v1/admin/nodes", nil},
		{"add node", func(c *Client) error {
			_, err := c.AddNode(ctx, NodeSpec{Name: "n1", Endpoint: "https://n1:7070", TokenFile: "/etc/t"})
			return err
		}, "POST", "/v1/admin/nodes", map[string]any{"name": "n1", "endpoint": "https://n1:7070", "token_file": "/etc/t"}},
		{"remove node", func(c *Client) error { return c.RemoveNode(ctx, "n1") }, "DELETE", "/v1/admin/nodes/n1", nil},
		{"cordon", func(c *Client) error { _, err := c.CordonNode(ctx, "n1", true); return err },
			"POST", "/v1/admin/nodes/n1/cordon", map[string]any{"cordoned": true}},
		{"uncordon", func(c *Client) error { _, err := c.CordonNode(ctx, "n1", false); return err },
			"POST", "/v1/admin/nodes/n1/cordon", map[string]any{"cordoned": false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, seen := gatewayStub(t, map[string]any{})
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			if len(*seen) != 1 {
				t.Fatalf("%d requests, want 1", len(*seen))
			}
			got := (*seen)[0]
			if got.method != tc.method || got.path != tc.path {
				t.Errorf("sent %s %s, want %s %s", got.method, got.path, tc.method, tc.path)
			}
			if got.auth != "Bearer gw-key" {
				t.Errorf("Authorization %q", got.auth)
			}
			if !reflect.DeepEqual(got.body, tc.body) {
				t.Errorf("body %v, want %v", got.body, tc.body)
			}
		})
	}
}

func TestGatewayRepliesDecode(t *testing.T) {
	ctx := context.Background()
	c, _ := gatewayStub(t, map[string]any{
		"user": "ana", "tenant": "t1", "key_id": "key_1", "scopes": []string{"sandboxes"},
	})
	w, err := c.Whoami(ctx)
	if err != nil || w.User != "ana" || w.KeyID != "key_1" || !reflect.DeepEqual(w.Scopes, []string{"sandboxes"}) {
		t.Errorf("whoami = %+v, %v", w, err)
	}
	c, _ = gatewayStub(t, map[string]any{"keys": []map[string]any{{"id": "sk_1", "fingerprint": "SHA256:x"}}})
	keys, err := c.SSHKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].Fingerprint != "SHA256:x" {
		t.Errorf("ssh keys = %+v, %v", keys, err)
	}
	c, _ = gatewayStub(t, map[string]any{"nodes": []map[string]any{{"name": "n1", "healthy": true}}})
	nodes, err := c.GatewayNodes(ctx)
	if err != nil || len(nodes) != 1 || !nodes[0].Healthy {
		t.Errorf("nodes = %+v, %v", nodes, err)
	}
}

// IsGateway is true only for an answer, false only for not found, and an
// error for everything else: a refused key must not read as "a plain
// sandboxd", or the CLI would fall back and hide the real problem.
func TestIsGateway(t *testing.T) {
	for _, tc := range []struct {
		status  int
		want    bool
		wantErr bool
	}{
		{http.StatusOK, true, false},
		{http.StatusNotFound, false, false},
		{http.StatusUnauthorized, false, true},
		{http.StatusInternalServerError, false, true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/whoami" {
				t.Errorf("asked %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			if tc.status == http.StatusOK {
				_, _ = io.WriteString(w, `{"user":"ana"}`)
			} else {
				_, _ = io.WriteString(w, `{"error":{"code":"x","message":"y"}}`)
			}
		}))
		c, _ := NewClient(srv.URL, "")
		got, err := c.IsGateway(context.Background())
		srv.Close()
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("status %d: IsGateway = %v, %v", tc.status, got, err)
		}
	}
}
