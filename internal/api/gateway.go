package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// Methods for the gateway-only endpoints (see the list near the end of
// types.go). A plain sandboxd answers each of them 404; IsGateway is how a
// caller tells the two apart before it relies on them.

// IsGateway reports whether the endpoint is a gateway in front of many nodes:
// true when GET /v1/whoami answers, false when it is not found there (a plain
// sandboxd), and an error for anything else — a refused credential or an
// endpoint that cannot be reached says nothing about which one it is.
func (c *Client) IsGateway(ctx context.Context) (bool, error) {
	_, err := c.Whoami(ctx)
	if err == nil {
		return true, nil
	}
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		return false, nil
	}
	return false, err
}

// Whoami returns the caller as the gateway sees its credential.
func (c *Client) Whoami(ctx context.Context) (Whoami, error) {
	var out Whoami
	return out, c.json(ctx, http.MethodGet, "/v1/whoami", nil, &out)
}

// SSHInfo returns where the gateway's SSH server listens and its host keys.
func (c *Client) SSHInfo(ctx context.Context) (SSHInfo, error) {
	var out SSHInfo
	return out, c.json(ctx, http.MethodGet, "/v1/ssh", nil, &out)
}

// AddSSHKey registers a public key (one authorized_keys line, no options) for
// SSH logins; sandbox, when set, limits it to that sandbox.
func (c *Client) AddSSHKey(ctx context.Context, key, sandbox string) (SSHKeyInfo, error) {
	var out SSHKeyInfo
	return out, c.json(ctx, http.MethodPost, "/v1/ssh-keys", SSHKeyRequest{Key: key, Sandbox: sandbox}, &out)
}

// SSHKeys lists the caller's registered SSH keys.
func (c *Client) SSHKeys(ctx context.Context) ([]SSHKeyInfo, error) {
	var out SSHKeyList
	err := c.json(ctx, http.MethodGet, "/v1/ssh-keys", nil, &out)
	return out.Keys, err
}

// RemoveSSHKey removes one of the caller's SSH keys.
func (c *Client) RemoveSSHKey(ctx context.Context, id string) error {
	return c.json(ctx, http.MethodDelete, "/v1/ssh-keys/"+url.PathEscape(id), nil, nil)
}

// SSHAccess issues a short-lived SSH login to one sandbox. ttlSecs 0 takes the
// gateway's default. The returned User is the whole credential until it
// expires: treat it as a secret.
func (c *Client) SSHAccess(ctx context.Context, ref string, ttlSecs int) (SSHAccess, error) {
	var out SSHAccess
	return out, c.json(ctx, http.MethodPost, sbx(ref)+"/ssh-access", SSHAccessRequest{TTLSecs: ttlSecs}, &out)
}

// CreateKey issues an API key (admin). The secret is returned once.
func (c *Client) CreateKey(ctx context.Context, req CreateKeyRequest) (CreatedKey, error) {
	var out CreatedKey
	return out, c.json(ctx, http.MethodPost, "/v1/admin/keys", req, &out)
}

// Keys lists the gateway's API keys, without their secrets (admin).
func (c *Client) Keys(ctx context.Context) ([]KeyInfo, error) {
	var out KeyList
	err := c.json(ctx, http.MethodGet, "/v1/admin/keys", nil, &out)
	return out.Keys, err
}

// RevokeKey revokes an API key (admin).
func (c *Client) RevokeKey(ctx context.Context, id string) error {
	return c.json(ctx, http.MethodDelete, "/v1/admin/keys/"+url.PathEscape(id), nil, nil)
}

// GatewayNodes lists the nodes behind the gateway (admin).
func (c *Client) GatewayNodes(ctx context.Context) ([]NodeInfo, error) {
	var out NodeList
	err := c.json(ctx, http.MethodGet, "/v1/admin/nodes", nil, &out)
	return out.Nodes, err
}

// AddNode puts a node behind the gateway (admin).
func (c *Client) AddNode(ctx context.Context, spec NodeSpec) (NodeInfo, error) {
	var out NodeInfo
	return out, c.json(ctx, http.MethodPost, "/v1/admin/nodes", spec, &out)
}

// RemoveNode takes a node out from behind the gateway (admin).
func (c *Client) RemoveNode(ctx context.Context, name string) error {
	return c.json(ctx, http.MethodDelete, "/v1/admin/nodes/"+url.PathEscape(name), nil, nil)
}

// CordonNode stops (or, with false, resumes) placing new sandboxes on a node
// (admin). What already runs there carries on.
func (c *Client) CordonNode(ctx context.Context, name string, cordoned bool) (NodeInfo, error) {
	var out NodeInfo
	return out, c.json(ctx, http.MethodPost, "/v1/admin/nodes/"+url.PathEscape(name)+"/cordon", CordonRequest{Cordoned: cordoned}, &out)
}
