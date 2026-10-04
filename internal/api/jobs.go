package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// Methods for jobs, agent runs and secrets (jobs_types.go). Gateway only.

// SetSecret sets one of the tenant's secrets. The value is never returned.
func (c *Client) SetSecret(ctx context.Context, name, value string) error {
	return c.json(ctx, http.MethodPut, "/v1/secrets/"+url.PathEscape(name), SecretRequest{Value: value}, nil)
}

// Secrets lists the tenant's secrets by name.
func (c *Client) Secrets(ctx context.Context) ([]SecretInfo, error) {
	var out SecretList
	err := c.json(ctx, http.MethodGet, "/v1/secrets", nil, &out)
	return out.Secrets, err
}

// DeleteSecret removes a secret.
func (c *Client) DeleteSecret(ctx context.Context, name string) error {
	return c.json(ctx, http.MethodDelete, "/v1/secrets/"+url.PathEscape(name), nil, nil)
}

// CreateJob starts a job.
func (c *Client) CreateJob(ctx context.Context, spec JobSpec) (Job, error) {
	var out Job
	return out, c.json(ctx, http.MethodPost, "/v1/jobs", spec, &out)
}

// AgentRun starts a one-run job of an agent.
func (c *Client) AgentRun(ctx context.Context, req AgentRunRequest) (Job, error) {
	var out Job
	return out, c.json(ctx, http.MethodPost, "/v1/agent-runs", req, &out)
}

// Jobs lists the caller's jobs, newest first, without their runs.
func (c *Client) Jobs(ctx context.Context) ([]Job, error) {
	var out JobList
	err := c.json(ctx, http.MethodGet, "/v1/jobs", nil, &out)
	return out.Jobs, err
}

func jobPath(id string) string { return "/v1/jobs/" + url.PathEscape(id) }

// Job returns one job with its runs.
func (c *Client) Job(ctx context.Context, id string) (Job, error) {
	var out Job
	return out, c.json(ctx, http.MethodGet, jobPath(id), nil, &out)
}

// CancelJob cancels a job: runs not started never are, and running ones'
// sandboxes are terminated. Cancelling a finished job changes nothing.
func (c *Client) CancelJob(ctx context.Context, id string) (Job, error) {
	var out Job
	return out, c.json(ctx, http.MethodDelete, jobPath(id), nil, &out)
}

// JobOutput returns what was kept of run n's output.
func (c *Client) JobOutput(ctx context.Context, id string, n int) (JobOutput, error) {
	var out JobOutput
	return out, c.json(ctx, http.MethodGet, jobPath(id)+"/runs/"+strconv.Itoa(n)+"/output", nil, &out)
}

// JobFile returns a file kept from run n.
func (c *Client) JobFile(ctx context.Context, id string, n int, path string) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, jobPath(id)+"/runs/"+strconv.Itoa(n)+"/files", url.Values{"path": {path}}, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
