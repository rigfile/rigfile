package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Level ranks a check result.
type Level string

const (
	OK   Level = "ok"
	Warn Level = "warn"
	Fail Level = "fail"
)

// Check is one line of `rigfile doctor` about a model.
type Check struct {
	Name   string
	Level  Level
	Detail string
}

// CheckOptions are the seams for tests.
type CheckOptions struct {
	HTTP  *http.Client
	Addrs func() []net.IP                                                   // this machine's non-loopback addresses; nil = the real ones
	Dial  func(ctx context.Context, network, addr string) (net.Conn, error) // nil = net.Dialer
}

func (o CheckOptions) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 120 * time.Second}
}

// LocalNonLoopback lists this machine's addresses other than loopback (what a wrongly bound server would answer on).
func LocalNonLoopback() []net.IP {
	var out []net.IP
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() && !ipn.IP.IsUnspecified() {
			out = append(out, ipn.IP)
		}
	}
	return out
}

// ListeningBeyondLoopback returns the non-loopback addresses on which the port accepts a connection: a model server that
// should be loopback-only but is reachable from the network.
func ListeningBeyondLoopback(ctx context.Context, port int, addrs []net.IP, dial func(ctx context.Context, network, addr string) (net.Conn, error)) []string {
	if dial == nil {
		d := &net.Dialer{Timeout: time.Second}
		dial = d.DialContext
	}
	var exposed []string
	for _, ip := range addrs {
		c, err := dial(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		if err == nil {
			c.Close()
			exposed = append(exposed, ip.String())
		}
	}
	return exposed
}

func healthPath(r Installed) string {
	if r.Engine == "ollama" {
		return "http://" + net.JoinHostPort(r.Host, strconv.Itoa(r.Port)) + "/api/tags"
	}
	return r.API + "/models"
}

// requestModel is the name to put in chat requests. mlx_lm.server treats "default_model" as the model it was started with
// (server.py: `self.body.get("model", "default_model")`); any other name would make it try to load that model.
func requestModel(r Installed) string {
	if r.Engine == "mlx-lm" {
		return "default_model"
	}
	return r.Model
}

func (o CheckOptions) post(ctx context.Context, url string, body any) (map[string]any, int, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out)
	return out, resp.StatusCode, nil
}

// CheckModel runs the four checks the plan asks for: the server is up, listens on loopback only, a chat completes, and a
// tool call is returned (else the model is "chat only" and routing must not send it agentic work).
func CheckModel(ctx context.Context, r Installed, o CheckOptions) []Check {
	var out []Check
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, healthPath(r), nil)
	hc := &http.Client{Timeout: 5 * time.Second}
	if o.HTTP != nil {
		hc = o.HTTP
	}
	resp, err := hc.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return []Check{{"server", Warn, "not answering on " + net.JoinHostPort(r.Host, strconv.Itoa(r.Port)) + " (start it: rigfile models serve " + r.Name + ")"}}
	}
	resp.Body.Close()
	out = append(out, Check{"server", OK, "answering on " + net.JoinHostPort(r.Host, strconv.Itoa(r.Port))})

	addrs := LocalNonLoopback()
	if o.Addrs != nil {
		addrs = o.Addrs()
	}
	if exposed := ListeningBeyondLoopback(ctx, r.Port, addrs, o.Dial); len(exposed) > 0 {
		out = append(out, Check{"loopback only", Fail, fmt.Sprintf("the server also answers on %v: it is reachable from the network. Restart it with --host 127.0.0.1 (rigfile models serve %s does)", exposed, r.Name)})
	} else {
		out = append(out, Check{"loopback only", OK, "not reachable on this machine's network addresses"})
	}

	chatURL := r.API + "/chat/completions"
	body, status, err := o.post(ctx, chatURL, map[string]any{"model": requestModel(r), "max_tokens": 16, "stream": false,
		"messages": []map[string]string{{"role": "user", "content": "Reply with the single word: ok"}}})
	switch {
	case err != nil:
		out = append(out, Check{"chat", Fail, "the request failed: " + err.Error()})
		return out
	case status != http.StatusOK || firstMessage(body) == nil:
		out = append(out, Check{"chat", Fail, fmt.Sprintf("the server answered %d without a chat message", status)})
		return out
	}
	out = append(out, Check{"chat", OK, "a chat completion returned"})

	body, status, err = o.post(ctx, chatURL, map[string]any{"model": requestModel(r), "max_tokens": 64, "stream": false,
		"messages": []map[string]string{{"role": "user", "content": "What is the current time? Use the get_time tool."}},
		"tools": []map[string]any{{"type": "function", "function": map[string]any{"name": "get_time", "description": "Returns the current time",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}}}}}})
	if err == nil && status == http.StatusOK {
		if m := firstMessage(body); m != nil {
			if tc, ok := m["tool_calls"].([]any); ok && len(tc) > 0 {
				return append(out, Check{"tool calls", OK, "the model returned a tool call"})
			}
		}
	}
	return append(out, Check{"tool calls", Warn, "no tool call came back on the smoke test: treat this model as CHAT ONLY (summaries, commit messages), not for agentic work"})
}

func firstMessage(body map[string]any) map[string]any {
	choices, _ := body["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	c, _ := choices[0].(map[string]any)
	m, _ := c["message"].(map[string]any)
	return m
}
