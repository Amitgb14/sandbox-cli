package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Registry logins: an organization's user name and token for a registry,
// kept on a gateway, sealed, and added to the organization's creates of an
// image from that registry (CreateSandboxRequest.RegistryLogin). Gateway
// only.

// RegistryLoginRequest is the body of PUT /v1/registry-logins/{registry}.
// The password is never returned.
type RegistryLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// RegistryLoginInfo is a registry login without its password.
type RegistryLoginInfo struct {
	Registry string    `json:"registry"`
	Username string    `json:"username"`
	Updated  time.Time `json:"updated_at"`
}

// RegistryLoginList is GET /v1/registry-logins.
type RegistryLoginList struct {
	Logins []RegistryLoginInfo `json:"logins"`
}

// SetRegistryLogin sets the organization's login for registry (host[:port]).
func (c *Client) SetRegistryLogin(ctx context.Context, registry, username, password string) error {
	return c.json(ctx, http.MethodPut, "/v1/registry-logins/"+url.PathEscape(registry), RegistryLoginRequest{Username: username, Password: password}, nil)
}

// RegistryLogins lists the organization's registry logins, without passwords.
func (c *Client) RegistryLogins(ctx context.Context) ([]RegistryLoginInfo, error) {
	var out RegistryLoginList
	err := c.json(ctx, http.MethodGet, "/v1/registry-logins", nil, &out)
	return out.Logins, err
}

// DeleteRegistryLogin removes the organization's login for registry.
func (c *Client) DeleteRegistryLogin(ctx context.Context, registry string) error {
	return c.json(ctx, http.MethodDelete, "/v1/registry-logins/"+url.PathEscape(registry), nil, nil)
}
