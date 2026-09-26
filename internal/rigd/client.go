package rigd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client talks to a running broker.
type Client struct {
	API   string // host:port
	Token string
	HTTP  *http.Client
}

// ErrNotRunning means no broker answers; callers fall back to Level 1 (or fail closed, docs/rigd.md §7).
var ErrNotRunning = errors.New("the rigfile broker is not running")

// ClientFromDir reads the discovery files a running broker wrote.
func ClientFromDir(dir string) (*Client, error) {
	raw, err := os.ReadFile(filepath.Join(dir, InfoFile))
	if err != nil {
		return nil, ErrNotRunning
	}
	var info Info
	if json.Unmarshal(raw, &info) != nil || info.API == "" {
		return nil, ErrNotRunning
	}
	tok, err := os.ReadFile(filepath.Join(dir, TokenFile))
	if err != nil {
		return nil, ErrNotRunning
	}
	return &Client{API: info.API, Token: strings.TrimSpace(string(tok))}, nil
}

func (c *Client) do(method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://"+c.API+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext}}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w (%v)", ErrNotRunning, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct{ Error string }
		_ = json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// APIError is a refusal from the broker (as opposed to it not being there).
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return "broker refused: " + e.Message }

// Status asks the broker about itself.
func (c *Client) Status() (*StatusReply, error) {
	var r StatusReply
	if err := c.do("GET", "/v1/status", nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Open creates a session.
func (c *Client) Open(spec SessionSpec) (*OpenReply, error) {
	var r OpenReply
	if err := c.do("POST", "/v1/sessions", spec, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Close ends a session.
func (c *Client) Close(id string) error { return c.do("DELETE", "/v1/sessions/"+id, nil, nil) }
