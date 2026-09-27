package regclient

import (
	"context"
	"net/http"
	"net/url"
)

// OrgMember is one person in an organisation (or one organisation of a person).
type OrgMember struct {
	Login string `json:"login"`
	Role  string `json:"role"`
}

func orgPath(org string) string { return "/v1/orgs/" + url.PathEscape(org) }

// CreateOrg makes an organisation; the caller becomes its owner.
func (c *Client) CreateOrg(ctx context.Context, login, name string) error {
	return c.sendJSON(ctx, http.MethodPost, "/v1/orgs", map[string]string{"login": login, "name": name}, nil, http.StatusCreated)
}

// MyOrgs lists the caller's organisations.
func (c *Client) MyOrgs(ctx context.Context) ([]OrgMember, error) {
	var out struct {
		Orgs []OrgMember `json:"orgs"`
	}
	err := c.getJSON(ctx, "/v1/me/orgs", &out)
	return out.Orgs, err
}

// OrgMembers lists an organisation's members (members only).
func (c *Client) OrgMembers(ctx context.Context, org string) ([]OrgMember, error) {
	var out struct {
		Members []OrgMember `json:"members"`
	}
	err := c.getJSON(ctx, orgPath(org)+"/members", &out)
	return out.Members, err
}

// SetOrgMember adds a member or changes their role.
func (c *Client) SetOrgMember(ctx context.Context, org, login, role string) error {
	return c.sendJSON(ctx, http.MethodPut, orgPath(org)+"/members/"+url.PathEscape(login), map[string]string{"role": role}, nil, http.StatusNoContent)
}

// RemoveOrgMember removes a member (or leaves, when login is the caller).
func (c *Client) RemoveOrgMember(ctx context.Context, org, login string) error {
	return c.sendJSON(ctx, http.MethodDelete, orgPath(org)+"/members/"+url.PathEscape(login), nil, nil, http.StatusNoContent)
}
