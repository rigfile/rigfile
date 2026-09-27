package regclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Collection is a curated list of rigs.
type Collection struct {
	Owner       string          `json:"owner"`
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Visibility  string          `json:"visibility"`
	URL         string          `json:"url"`
	Rigs        []CollectionRig `json:"rigs"`
}

// CollectionRig is one rig in a collection.
type CollectionRig struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Stars       int    `json:"stars"`
	Latest      string `json:"latest"`
	Note        string `json:"note"`
}

func collPath(owner, slug string) string {
	return "/v1/collections/" + url.PathEscape(owner) + "/" + url.PathEscape(slug)
}

func (c *Client) sendJSON(ctx context.Context, method, path string, in any, out any, want int) error {
	var body []byte
	if in != nil {
		body, _ = json.Marshal(in)
	}
	resp, err := c.do(ctx, method, path, body, "application/json")
	if err != nil {
		return err
	}
	if resp.StatusCode != want {
		return readAPIError(resp)
	}
	defer resp.Body.Close()
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// CreateCollection makes a collection owned by the signed-in user.
func (c *Client) CreateCollection(ctx context.Context, slug, title, description, visibility string) (*Collection, error) {
	var out Collection
	err := c.sendJSON(ctx, http.MethodPost, "/v1/collections", map[string]string{"slug": slug, "title": title, "description": description, "visibility": visibility}, &out, http.StatusCreated)
	return &out, err
}

// GetCollection reads a collection.
func (c *Client) GetCollection(ctx context.Context, owner, slug string) (*Collection, error) {
	var out Collection
	return &out, c.getJSON(ctx, collPath(owner, slug), &out)
}

// UserCollections lists a user's collections the caller may see.
func (c *Client) UserCollections(ctx context.Context, login string) ([]Collection, error) {
	var out struct {
		Collections []Collection `json:"collections"`
	}
	err := c.getJSON(ctx, "/v1/users/"+url.PathEscape(login)+"/collections", &out)
	return out.Collections, err
}

// AddToCollection adds a rig (owner/name) with an optional note.
func (c *Client) AddToCollection(ctx context.Context, owner, slug, rig, note string) error {
	return c.sendJSON(ctx, http.MethodPut, collPath(owner, slug)+"/items", map[string]string{"rig": rig, "note": note}, nil, http.StatusNoContent)
}

// RemoveFromCollection takes a rig out.
func (c *Client) RemoveFromCollection(ctx context.Context, owner, slug, rigOwner, rigName string) error {
	return c.sendJSON(ctx, http.MethodDelete, collPath(owner, slug)+"/items/"+url.PathEscape(rigOwner)+"/"+url.PathEscape(rigName), nil, nil, http.StatusNoContent)
}

// DeleteCollection removes a collection.
func (c *Client) DeleteCollection(ctx context.Context, owner, slug string) error {
	return c.sendJSON(ctx, http.MethodDelete, collPath(owner, slug), nil, nil, http.StatusNoContent)
}
