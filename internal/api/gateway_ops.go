package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// The gateway's operator endpoints for running a fleet: draining a node,
// the sandboxes on nodes that stopped answering, and the audit record.
//
//	POST /v1/admin/nodes/{name}/drain  DrainRequest -> DrainResult  (admin)
//	GET  /v1/admin/lost                             -> LostList     (admin)
//	GET  /v1/admin/audit?since=RFC3339&limit=N      -> AuditList    (admin)

// DrainRequest is POST /v1/admin/nodes/{name}/drain. The node is cordoned
// either way; Terminate also ends every sandbox on it.
type DrainRequest struct {
	Terminate bool `json:"terminate"`
}

// DrainResult says what a drain did.
type DrainResult struct {
	Node     string `json:"node"`
	Cordoned bool   `json:"cordoned"`
	// Remaining is how many sandboxes on the node have not ended: all of
	// them without Terminate, those that failed to with it.
	Remaining  int            `json:"remaining"`
	Terminated []string       `json:"terminated"`
	Failed     []DrainFailure `json:"failed,omitempty"`
}

// DrainFailure is a sandbox a drain could not terminate.
type DrainFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// LostSandbox is a sandbox recorded on a node that has not answered for
// longer than the gateway's grace period. Its state is unknown: the node may
// come back with it running. It no longer counts against its tenant's quota.
type LostSandbox struct {
	ID       string  `json:"id"`
	User     string  `json:"user"`
	Tenant   string  `json:"tenant"`
	Node     string  `json:"node"`
	CPUs     float64 `json:"cpus,omitempty"`
	MemoryMB int     `json:"memory_mb,omitempty"`
	// NodeDownSince is when the gateway last saw the node answer, or when it
	// started if never; zero for a node no longer configured.
	NodeDownSince time.Time `json:"node_down_since,omitzero"`
}

// LostList is GET /v1/admin/lost.
type LostList struct {
	Sandboxes []LostSandbox `json:"sandboxes"`
}

// AuditEntry is one line of the gateway's audit record: an authenticated API
// request, an SSH login or session, or what revoking access ended. It names
// a credential by its id and an SSH key by its fingerprint; never a secret.
type AuditEntry struct {
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`   // "api", "ssh" or "job"
	Action string    `json:"action"` // "POST /v1/sandboxes", "ssh.login", "ssh.session", "ssh.revoked", "job.revoked"
	KeyID  string    `json:"key_id,omitempty"`
	User   string    `json:"user,omitempty"`
	Tenant string    `json:"tenant,omitempty"`
	// Remote is the client's address as the gateway saw it.
	Remote  string `json:"remote,omitempty"`
	Sandbox string `json:"sandbox,omitempty"`
	Node    string `json:"node,omitempty"`
	// Target is what an endpoint not about a sandbox acted on: a key id, a
	// node or volume name, a snapshot id, a job id.
	Target string `json:"target,omitempty"`
	Status int    `json:"status,omitempty"` // the HTTP status (api)
	Result string `json:"result,omitempty"` // "ok" or "refused" (ssh); "closed" (ssh.revoked), "cancelled" (job.revoked)
	// Fingerprint is the SSH key's (SHA256:…), or "token" for a login with a
	// short-lived token.
	Fingerprint string `json:"fingerprint,omitempty"`
	Session     string `json:"session,omitempty"` // shell, exec, sftp, direct-tcpip
}

// AuditList is GET /v1/admin/audit: the newest entries since a time, oldest
// first.
type AuditList struct {
	Entries []AuditEntry `json:"entries"`
	// Truncated is set when older entries since the time were left out.
	Truncated bool `json:"truncated,omitempty"`
}

// DrainNode cordons a node and, with terminate, ends every sandbox on it
// (admin).
func (c *Client) DrainNode(ctx context.Context, name string, terminate bool) (DrainResult, error) {
	var out DrainResult
	return out, c.json(ctx, http.MethodPost, "/v1/admin/nodes/"+url.PathEscape(name)+"/drain", DrainRequest{Terminate: terminate}, &out)
}

// LostSandboxes lists sandboxes on nodes past the gateway's grace period
// (admin).
func (c *Client) LostSandboxes(ctx context.Context) ([]LostSandbox, error) {
	var out LostList
	err := c.json(ctx, http.MethodGet, "/v1/admin/lost", nil, &out)
	return out.Sandboxes, err
}

// Audit returns the gateway's audit entries since a time, at most limit (0
// takes the gateway's default) of the newest (admin).
func (c *Client) Audit(ctx context.Context, since time.Time, limit int) (AuditList, error) {
	q := url.Values{}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339Nano))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/v1/admin/audit"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out AuditList
	return out, c.json(ctx, http.MethodGet, path, nil, &out)
}
