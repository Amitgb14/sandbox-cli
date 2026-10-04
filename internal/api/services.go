package api

import (
	"context"
	"net/http"
	"net/url"
)

func svc(name string) string { return "/v1/services/" + url.PathEscape(name) }

// DeployService creates a service (POST /v1/services).
func (c *Client) DeployService(ctx context.Context, spec ServiceSpec) (Service, error) {
	var out Service
	return out, c.json(ctx, http.MethodPost, "/v1/services", spec, &out)
}

// UpdateService replaces a service's spec (PUT /v1/services/{name}). A
// change to what its sandboxes are rolls out one replica at a time.
func (c *Client) UpdateService(ctx context.Context, spec ServiceSpec) (Service, error) {
	var out Service
	return out, c.json(ctx, http.MethodPut, svc(spec.Name), spec, &out)
}

// Services lists the caller's services.
func (c *Client) Services(ctx context.Context) ([]Service, error) {
	var out ServiceList
	err := c.json(ctx, http.MethodGet, "/v1/services", nil, &out)
	return out.Services, err
}

// Service returns one service with its replicas.
func (c *Client) Service(ctx context.Context, name string) (Service, error) {
	var out Service
	return out, c.json(ctx, http.MethodGet, svc(name), nil, &out)
}

// ScaleService sets how many replicas a service keeps.
func (c *Client) ScaleService(ctx context.Context, name string, replicas int) (Service, error) {
	var out Service
	return out, c.json(ctx, http.MethodPost, svc(name)+"/scale", ScaleServiceRequest{Replicas: replicas}, &out)
}

// DeleteService deletes a service and terminates its replicas.
func (c *Client) DeleteService(ctx context.Context, name string) error {
	return c.json(ctx, http.MethodDelete, svc(name), nil, nil)
}
