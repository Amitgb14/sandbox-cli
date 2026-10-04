package api

import (
	"context"
	"net/http"
	"net/url"
)

// Organisations on a gateway (types.go: Org, OrgHeader). A plain sandboxd
// answers each of these 404.

// Orgs lists the organisations the caller may act in: its key's own tenant
// and every organisation its user is a member of.
func (c *Client) Orgs(ctx context.Context) ([]Org, error) {
	var out OrgList
	err := c.json(ctx, http.MethodGet, "/v1/orgs", nil, &out)
	return out.Orgs, err
}

// CreateOrg creates an organisation, with the caller as its owner. It needs
// the org:create scope.
func (c *Client) CreateOrg(ctx context.Context, name string) (Org, error) {
	var out Org
	return out, c.json(ctx, http.MethodPost, "/v1/orgs", CreateOrgRequest{Name: name}, &out)
}

func orgPath(name string) string { return "/v1/orgs/" + url.PathEscape(name) }

// OrgMembers lists an organisation's members; any member may.
func (c *Client) OrgMembers(ctx context.Context, org string) ([]OrgMember, error) {
	var out OrgMemberList
	err := c.json(ctx, http.MethodGet, orgPath(org)+"/members", nil, &out)
	return out.Members, err
}

// SetOrgMember adds a member, or changes one's role; an owner may.
func (c *Client) SetOrgMember(ctx context.Context, org string, req OrgMemberRequest) (OrgMember, error) {
	var out OrgMember
	return out, c.json(ctx, http.MethodPost, orgPath(org)+"/members", req, &out)
}

// RemoveOrgMember removes a member; an owner may. tenant is the member's own
// tenant, "" for the caller's.
func (c *Client) RemoveOrgMember(ctx context.Context, org, user, tenant string) error {
	var q url.Values
	if tenant != "" {
		q = url.Values{"tenant": {tenant}}
	}
	resp, err := c.do(ctx, http.MethodDelete, orgPath(org)+"/members/"+url.PathEscape(user), q, nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// AdminOrgs lists every organisation; an admin key may.
func (c *Client) AdminOrgs(ctx context.Context) ([]AdminOrg, error) {
	var out AdminOrgList
	err := c.json(ctx, http.MethodGet, "/v1/admin/orgs", nil, &out)
	return out.Orgs, err
}
