package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Agent logins: a user's own login to an agent (its OAuth files), kept on a
// gateway, sealed, for that user alone, and copied into the sandboxes they
// ask for it in. Gateway only.

// KeepAgentLoginRequest is the body of POST /v1/sandboxes/{ref}/agent-login.
type KeepAgentLoginRequest struct {
	Agent string `json:"agent"`
}

// KeptAgentLogin answers it: whether a saved login was copied in.
type KeptAgentLogin struct {
	Restored bool `json:"restored"`
}

// AgentLoginInfo is a kept login: which agent, and which of its files,
// never their contents.
type AgentLoginInfo struct {
	Agent   string    `json:"agent"`
	Files   []string  `json:"files"`
	Updated time.Time `json:"updated_at"`
}

// AgentLoginList is GET /v1/agent-logins.
type AgentLoginList struct {
	Logins []AgentLoginInfo `json:"logins"`
}

// KeepAgentLogin copies the caller's saved login for agent into the sandbox,
// and has the gateway save it from there as it changes, and once more when
// the sandbox is deleted.
func (c *Client) KeepAgentLogin(ctx context.Context, ref, agent string) (KeptAgentLogin, error) {
	var out KeptAgentLogin
	err := c.json(ctx, http.MethodPost, sbx(ref)+"/agent-login", KeepAgentLoginRequest{Agent: agent}, &out)
	return out, err
}

// AgentLogins lists the caller's kept logins.
func (c *Client) AgentLogins(ctx context.Context) ([]AgentLoginInfo, error) {
	var out AgentLoginList
	err := c.json(ctx, http.MethodGet, "/v1/agent-logins", nil, &out)
	return out.Logins, err
}

// ForgetAgentLogin removes the caller's kept login for agent.
func (c *Client) ForgetAgentLogin(ctx context.Context, agent string) error {
	return c.json(ctx, http.MethodDelete, "/v1/agent-logins/"+url.PathEscape(agent), nil, nil)
}

// ErrFileTooLarge is ReadFileLimit's answer for a file over its limit.
var ErrFileTooLarge = &Error{Status: http.StatusRequestEntityTooLarge, Code: CodeInvalidRequest, Message: "file too large"}

// ReadFileLimit is ReadFile for a file whose contents the caller holds in
// memory and a guest decides the size of: at most max bytes are read.
func (c *Client) ReadFileLimit(ctx context.Context, ref, path string, max int64) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, sbx(ref)+"/files", url.Values{"path": {path}}, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, ErrFileTooLarge
	}
	return data, nil
}
