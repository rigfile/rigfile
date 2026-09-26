// Package login runs the guided sign-ins a rig asks for (docs/sharing.md §8). Rigfile never sees a vendor password:
// it opens the vendor's own sign-in page, or runs the vendor's own login command, or asks for an API key with a hidden
// prompt. Tokens go to the secret store and nowhere else: not to a config file, a log, or state.json.
package login

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth describes one provider's endpoints. ClientID is a public client id (no secret): both flows are the public-client
// variants (PKCE, device code).
type OAuth struct {
	AuthURL   string // authorization endpoint (loopback flow)
	DeviceURL string // device authorization endpoint (device flow)
	TokenURL  string
	ClientID  string
	Scopes    []string
}

// Token is what a flow returns. It is handed to the caller once and never logged.
type Token struct {
	Access, Refresh string
	ExpiresIn       int
}

// Client carries the seams the flows need; the zero value is usable.
type Client struct {
	HTTP  *http.Client
	Open  func(url string) error        // opens the browser; an error means "print the URL instead"
	Show  func(format string, a ...any) // user-facing messages
	Sleep func(time.Duration)           // device-flow polling delay
	Now   func() time.Time
	// Timeout bounds a whole interactive flow (default 5 minutes).
	Timeout time.Duration
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) show(f string, a ...any) {
	if c.Show != nil {
		c.Show(f, a...)
	}
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 5 * time.Minute
}

// endpointOK: HTTPS, or plain HTTP to a loopback address (a local test server).
func endpointOK(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("login: %q is not a valid endpoint", raw)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("login: %s must be https", u.Host)
}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// LoopbackPKCE runs the authorization-code flow with PKCE: a one-shot listener on 127.0.0.1, a random `state`, and the
// code exchanged with the verifier. Only the first valid callback is accepted.
func (c *Client) LoopbackPKCE(ctx context.Context, o OAuth) (*Token, error) {
	if err := endpointOK(o.AuthURL); err != nil {
		return nil, err
	}
	if err := endpointOK(o.TokenURL); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	verifier, err := randomURLSafe(48)
	if err != nil {
		return nil, err
	}
	stateVal, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("login: cannot listen on the loopback interface: %w", err)
	}
	redirect := "http://" + ln.Addr().String() + "/callback"
	type result struct {
		code string
		err  error
	}
	got := make(chan result, 1)
	mux := http.NewServeMux()
	var once bool
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if once || r.Method != http.MethodGet || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(stateVal)) != 1 {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return // a wrong or repeated callback is ignored; the flow keeps waiting for the real one
		}
		once = true
		if e := q.Get("error"); e != "" {
			http.Error(w, "sign-in was not completed", http.StatusBadRequest)
			got <- result{err: fmt.Errorf("login: the provider refused the sign-in (%s)", e)}
			return
		}
		fmt.Fprintln(w, "You can close this tab and return to the terminal.")
		got <- result{code: q.Get("code")}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	q := url.Values{
		"response_type": {"code"}, "client_id": {o.ClientID}, "redirect_uri": {redirect}, "state": {stateVal},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	if len(o.Scopes) > 0 {
		q.Set("scope", strings.Join(o.Scopes, " "))
	}
	authURL := o.AuthURL + "?" + q.Encode()
	if strings.Contains(o.AuthURL, "?") {
		authURL = o.AuthURL + "&" + q.Encode()
	}
	if c.Open == nil || c.Open(authURL) != nil {
		c.show("Open this address in a browser to sign in:\n  %s\n", authURL)
	} else {
		c.show("A browser window should open. If it does not, open:\n  %s\n", authURL)
	}
	var code string
	select {
	case r := <-got:
		if r.err != nil {
			return nil, r.err
		}
		code = r.code
	case <-ctx.Done():
		return nil, errors.New("login: timed out waiting for the browser sign-in")
	}
	if code == "" {
		return nil, errors.New("login: the provider returned no authorization code")
	}
	return c.exchange(ctx, o.TokenURL, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {o.ClientID}, "code_verifier": {verifier},
	})
}

func (c *Client) exchange(ctx context.Context, tokenURL string, form url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("login: token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tr struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		ExpiresIn int    `json:"expires_in"`
		Error     string `json:"error"`
	}
	_ = json.Unmarshal(body, &tr)
	if resp.StatusCode != http.StatusOK || tr.Access == "" {
		// the error code is safe to show; the body might contain more, so it is not
		return nil, &TokenError{Code: tr.Error, Status: resp.StatusCode}
	}
	return &Token{Access: tr.Access, Refresh: tr.Refresh, ExpiresIn: tr.ExpiresIn}, nil
}

// TokenError is a refusal from the token endpoint (only the OAuth error code, never the response body).
type TokenError struct {
	Code   string
	Status int
}

func (e *TokenError) Error() string {
	if e.Code != "" {
		return "login: the provider answered " + e.Code
	}
	return fmt.Sprintf("login: the provider answered HTTP %d", e.Status)
}

// DeviceFlow runs RFC 8628: show a code and an address, poll until the user approves elsewhere. It is what a machine
// reached over SSH, or without a browser, uses.
func (c *Client) DeviceFlow(ctx context.Context, o OAuth) (*Token, error) {
	if err := endpointOK(o.DeviceURL); err != nil {
		return nil, err
	}
	if err := endpointOK(o.TokenURL); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	form := url.Values{"client_id": {o.ClientID}}
	if len(o.Scopes) > 0 {
		form.Set("scope", strings.Join(o.Scopes, " "))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.DeviceURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fmt.Errorf("login: device authorization failed: %w", err)
	}
	var dr struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Complete        string `json:"verification_uri_complete"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &dr) != nil || dr.DeviceCode == "" || dr.UserCode == "" {
		return nil, fmt.Errorf("login: the provider did not start a device sign-in (HTTP %d)", resp.StatusCode)
	}
	if err := endpointOK(dr.VerificationURI); err != nil {
		return nil, err
	}
	c.show("On any device with a browser, open:\n  %s\nand enter the code:  %s\n\nWaiting for you to approve...\n", dr.VerificationURI, dr.UserCode)
	interval := time.Duration(dr.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	for {
		select {
		case <-ctx.Done():
			return nil, errors.New("login: timed out waiting for the approval")
		default:
		}
		sleep(interval)
		tok, err := c.exchange(ctx, o.TokenURL, url.Values{
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {dr.DeviceCode}, "client_id": {o.ClientID},
		})
		if err == nil {
			return tok, nil
		}
		var te *TokenError
		if !errors.As(err, &te) {
			return nil, err
		}
		switch te.Code {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return nil, errors.New("login: the sign-in was denied")
		case "expired_token":
			return nil, errors.New("login: the code expired; run the login again")
		default:
			return nil, err
		}
	}
}
