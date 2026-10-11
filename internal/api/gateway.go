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
	// Asked without an organisation: a 404 for one the key may not select
	// ("no such organization") would otherwise read as a plain sandboxd.
	_, err := c.WithOrg("").Whoami(ctx)
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

// SetKeyExpiry renews or shortens an API key (admin): it stops working
// secs from now, or never when secs is 0.
func (c *Client) SetKeyExpiry(ctx context.Context, id string, secs int) (KeyInfo, error) {
	var out KeyInfo
	return out, c.json(ctx, http.MethodPatch, "/v1/admin/keys/"+url.PathEscape(id), KeyExpiryRequest{ExpiresInSecs: secs}, &out)
}

// Policies lists the gateway's user policies (admin).
func (c *Client) Policies(ctx context.Context) ([]UserPolicy, error) {
	var out UserPolicyList
	err := c.json(ctx, http.MethodGet, "/v1/admin/policies", nil, &out)
	return out.Policies, err
}

// PutPolicy creates or replaces a user policy (admin). A change holds for
// sandboxes created after it; running ones keep the lifetime they have.
func (c *Client) PutPolicy(ctx context.Context, p UserPolicy) (UserPolicy, error) {
	var out UserPolicy
	return out, c.json(ctx, http.MethodPut, "/v1/admin/policies/"+url.PathEscape(p.Name), p, &out)
}

// DeletePolicy removes a user policy no user holds (admin).
func (c *Client) DeletePolicy(ctx context.Context, name string) error {
	return c.json(ctx, http.MethodDelete, "/v1/admin/policies/"+url.PathEscape(name), nil, nil)
}

// AssignPolicy assigns a policy to a user, or clears it (admin).
func (c *Client) AssignPolicy(ctx context.Context, req AssignPolicyRequest) error {
	return c.json(ctx, http.MethodPut, "/v1/admin/user-policy", req, nil)
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

// FleetImages lists the images built on a gateway's nodes that take new
// sandboxes: what a sandbox can start from without a pull. Any key that may
// list sandboxes.
func (c *Client) FleetImages(ctx context.Context) ([]Image, error) {
	var out ImageList
	err := c.json(ctx, http.MethodGet, "/v1/fleet/images", nil, &out)
	return out.Images, err
}

// CreateInvite makes a sign-up link for an email address (admin). The token
// is returned once.
func (c *Client) CreateInvite(ctx context.Context, req CreateInviteRequest) (CreatedInvite, error) {
	var out CreatedInvite
	return out, c.json(ctx, http.MethodPost, "/v1/admin/invites", req, &out)
}

// Invites lists the invites not yet used or expired (admin).
func (c *Client) Invites(ctx context.Context) (InviteList, error) {
	var out InviteList
	err := c.json(ctx, http.MethodGet, "/v1/admin/invites", nil, &out)
	return out, err
}

// ResendInvite gives a pending invite a new link, valid two days from now,
// emailed when send (admin). The old link stops working.
func (c *Client) ResendInvite(ctx context.Context, id string, send bool) (CreatedInvite, error) {
	var out CreatedInvite
	return out, c.json(ctx, http.MethodPost, "/v1/admin/invites/"+url.PathEscape(id)+"/resend", ResetPasswordRequest{SendEmail: send}, &out)
}

// RevokeInvite withdraws an invite (admin).
func (c *Client) RevokeInvite(ctx context.Context, id string) error {
	return c.json(ctx, http.MethodDelete, "/v1/admin/invites/"+url.PathEscape(id), nil, nil)
}

// ResetPassword makes an invite that sets a new password for the account
// key id (admin), emailed when send. Until it is used the old password
// still works.
func (c *Client) ResetPassword(ctx context.Context, id string, send bool) (CreatedInvite, error) {
	var out CreatedInvite
	return out, c.json(ctx, http.MethodPost, "/v1/admin/keys/"+url.PathEscape(id)+"/reset", ResetPasswordRequest{SendEmail: send}, &out)
}

// InviteFor says what an invite is for. It needs no key: the token is the
// credential.
func (c *Client) InviteFor(ctx context.Context, token string) (InviteInfo, error) {
	var out InviteInfo
	return out, c.json(ctx, http.MethodPost, "/v1/auth/invite", InviteTokenRequest{Token: token}, &out)
}

// Register uses an invite: it sets the password and signs in. No key.
func (c *Client) Register(ctx context.Context, req RegisterRequest) (Login, error) {
	var out Login
	return out, c.json(ctx, http.MethodPost, "/v1/auth/register", req, &out)
}

// Login signs in with an email and password. No key.
func (c *Client) Login(ctx context.Context, req LoginRequest) (Login, error) {
	var out Login
	return out, c.json(ctx, http.MethodPost, "/v1/auth/login", req, &out)
}

// Logout ends the sign-in whose key c holds.
func (c *Client) Logout(ctx context.Context) error {
	return c.json(ctx, http.MethodPost, "/v1/auth/logout", nil, nil)
}

// ChangePassword changes the password of the account c's key belongs to.
func (c *Client) ChangePassword(ctx context.Context, req PasswordRequest) error {
	return c.json(ctx, http.MethodPost, "/v1/auth/password", req, nil)
}
