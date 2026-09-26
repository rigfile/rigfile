package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitHub is the sign-in provider. The interface exists so tests can run against a fake GitHub.
type GitHub interface {
	AuthorizeURL(state, redirectURI string) string
	Exchange(ctx context.Context, code, redirectURI string) (accessToken string, err error)
	User(ctx context.Context, accessToken string) (GitHubUser, error)
}

// GitHubHTTP talks to github.com and api.github.com (or configured stand-ins). It asks for no OAuth scope: the public
// profile (numeric id, login, name, avatar) is all the registry needs.
type GitHubHTTP struct {
	ClientID, ClientSecret string
	WebBase, APIBase       string
	HTTP                   *http.Client
}

func (g *GitHubHTTP) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// AuthorizeURL implements GitHub.
func (g *GitHubHTTP) AuthorizeURL(state, redirectURI string) string {
	q := url.Values{"client_id": {g.ClientID}, "redirect_uri": {redirectURI}, "state": {state}, "allow_signup": {"true"}}
	return strings.TrimRight(g.WebBase, "/") + "/login/oauth/authorize?" + q.Encode()
}

// Exchange implements GitHub.
func (g *GitHubHTTP) Exchange(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{"client_id": {g.ClientID}, "client_secret": {g.ClientSecret}, "code": {code}, "redirect_uri": {redirectURI}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.WebBase, "/")+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := g.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Access string `json:"access_token"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		return "", errors.New("github: the sign-in code could not be exchanged")
	}
	if out.Access == "" {
		return "", fmt.Errorf("github: sign-in refused (%s)", out.Error)
	}
	return out.Access, nil
}

// User implements GitHub.
func (g *GitHubHTTP) User(ctx context.Context, token string) (GitHubUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(g.APIBase, "/")+"/user", nil)
	if err != nil {
		return GitHubUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := g.client().Do(req)
	if err != nil {
		return GitHubUser{}, fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out) != nil || out.ID == 0 || out.Login == "" {
		return GitHubUser{}, errors.New("github: could not read the account")
	}
	return GitHubUser{ID: out.ID, Login: out.Login, Name: out.Name, AvatarURL: out.AvatarURL}, nil
}
