package models

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type serverOpts struct {
	toolCalls bool
	chatCode  int
}

func chatServer(t *testing.T, o serverOpts) (*httptest.Server, *[]map[string]any) {
	var seen []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags", "/v1/models":
			w.Write([]byte(`{"models":[],"data":[]}`))
		case "/v1/chat/completions":
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			seen = append(seen, req)
			if o.chatCode != 0 {
				http.Error(w, "boom", o.chatCode)
				return
			}
			msg := map[string]any{"role": "assistant", "content": "ok"}
			if _, hasTools := req["tools"]; hasTools && o.toolCalls {
				msg["tool_calls"] = []any{map[string]any{"function": map[string]any{"name": "get_time"}}}
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func installedFor(srv *httptest.Server, engine string) Installed {
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	return Installed{Name: "local-coder", Engine: engine, Model: "qwen3:8b", Host: "127.0.0.1", Port: port, API: srv.URL + "/v1"}
}

func find(cs []Check, name string) Check {
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	return Check{Name: name, Level: "missing"}
}

func noAddrs() []net.IP { return nil }

func TestCheckModelAllGood(t *testing.T) {
	srv, seen := chatServer(t, serverOpts{toolCalls: true})
	cs := CheckModel(context.Background(), installedFor(srv, "ollama"), CheckOptions{Addrs: noAddrs})
	for _, n := range []string{"server", "loopback only", "chat", "tool calls"} {
		if c := find(cs, n); c.Level != OK {
			t.Errorf("%s: %+v", n, c)
		}
	}
	if (*seen)[0]["model"] != "qwen3:8b" {
		t.Fatalf("%v", (*seen)[0])
	}
}

func TestCheckModelChatOnlyAndFailures(t *testing.T) {
	srv, _ := chatServer(t, serverOpts{})
	cs := CheckModel(context.Background(), installedFor(srv, "ollama"), CheckOptions{Addrs: noAddrs})
	if c := find(cs, "tool calls"); c.Level != Warn || !strings.Contains(c.Detail, "CHAT ONLY") {
		t.Fatalf("%+v", c)
	}
	srv2, _ := chatServer(t, serverOpts{chatCode: 500})
	if c := find(CheckModel(context.Background(), installedFor(srv2, "ollama"), CheckOptions{Addrs: noAddrs}), "chat"); c.Level != Fail {
		t.Fatalf("%+v", c)
	}
	// a server that is not up is one warning, not a wall of failures
	r := installedFor(srv, "ollama")
	srv.Close()
	cs = CheckModel(context.Background(), r, CheckOptions{Addrs: noAddrs})
	if len(cs) != 1 || cs[0].Level != Warn || !strings.Contains(cs[0].Detail, "rigfile models serve local-coder") {
		t.Fatalf("%+v", cs)
	}
}

func TestMLXRequestsUseTheDefaultModel(t *testing.T) {
	srv, seen := chatServer(t, serverOpts{})
	CheckModel(context.Background(), installedFor(srv, "mlx-lm"), CheckOptions{Addrs: noAddrs})
	if len(*seen) == 0 || (*seen)[0]["model"] != "default_model" {
		t.Fatalf("mlx_lm.server would try to LOAD any other name: %v", *seen)
	}
}

func TestLoopbackOnlyCheckFlagsAnExposedServer(t *testing.T) {
	srv, _ := chatServer(t, serverOpts{toolCalls: true})
	r := installedFor(srv, "ollama")
	lan := net.ParseIP("192.168.1.20")
	dialed := []string{}
	// the machine has a LAN address on which the port ALSO answers
	cs := CheckModel(context.Background(), r, CheckOptions{Addrs: func() []net.IP { return []net.IP{lan} },
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			c1, c2 := net.Pipe()
			c2.Close()
			return c1, nil
		}})
	if c := find(cs, "loopback only"); c.Level != Fail || !strings.Contains(c.Detail, "192.168.1.20") || len(dialed) != 1 || dialed[0] != "192.168.1.20:"+strconv.Itoa(r.Port) {
		t.Fatalf("%+v %v", c, dialed)
	}
	// refused on the LAN address: fine
	cs = CheckModel(context.Background(), r, CheckOptions{Addrs: func() []net.IP { return []net.IP{lan} },
		Dial: func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("refused") }})
	if c := find(cs, "loopback only"); c.Level != OK {
		t.Fatalf("%+v", c)
	}
}

func TestDetectOllamaAndTheCapturedSection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[{"name":"qwen3:8b","digest":"sha256:500a1f067a9f0000000000000000000000000000000000000000000000000000"},{"name":"weird name!!","digest":""}]}`))
		case "/api/version":
			w.Write([]byte(`{"version":"0.34.4"}`))
		}
	}))
	defer srv.Close()
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	ds := DetectOllama(context.Background(), nil, port)
	if len(ds) != 2 || ds[0].Model != "qwen3:8b" || ds[0].Version != "0.34.4" || ds[0].Digest != "500a1f067a9f" {
		t.Fatalf("%+v", ds)
	}
	section, notes := ManifestSection(ds)
	for _, want := range []string{"models:\n", "  qwen3-8b:\n", "engine: ollama", `engine_version: "0.34.4"`, "model: qwen3:8b", "digest: 500a1f067a9f", "serve: {port: " + p + "}"} {
		if !strings.Contains(section, want) {
			t.Errorf("missing %q in\n%s", want, section)
		}
	}
	if len(notes) != 0 {
		t.Fatalf("%v", notes)
	}
	// nothing listening: nothing detected
	srv.Close()
	if len(DetectOllama(context.Background(), nil, port)) != 0 {
		t.Fatal("a closed port detects nothing")
	}
	if s, _ := ManifestSection(nil); s != "" {
		t.Fatal("no models, no section")
	}
}
