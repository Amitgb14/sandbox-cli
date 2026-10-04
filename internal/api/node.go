package api

import (
	"context"
	"net/http"
)

// Node is GET /v1/node: what this sandboxd reports about itself to a gateway
// in front of many — capacity, what is free, what is pooled and cached, and
// whether it is cordoned.
func (c *Client) Node(ctx context.Context) (NodeStatus, error) {
	var out NodeStatus
	return out, c.json(ctx, http.MethodGet, "/v1/node", nil, &out)
}

// Cordon is POST /v1/node/cordon: a cordoned node refuses new sandboxes with
// CodeUnavailable and leaves the ones it has alone. false uncordons it.
func (c *Client) Cordon(ctx context.Context, cordoned bool) (NodeStatus, error) {
	var out NodeStatus
	return out, c.json(ctx, http.MethodPost, "/v1/node/cordon", CordonRequest{Cordoned: cordoned}, &out)
}
