package rigd

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/platform"
)

// File names inside the broker's directory (<state>/rigd).
const (
	TokenFile = "token"       // the API bearer token, private
	InfoFile  = "broker.json" // addresses and pid; holds nothing secret
	AuditFile = "audit.jsonl"
)

// Info tells a client where the running broker is.
type Info struct {
	API     string `json:"api"`   // host:port of the session API
	Proxy   string `json:"proxy"` // host:port of the egress proxy
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

// Broker is the long-running process: the session API, the egress proxy, the CA and the audit log.
type Broker struct {
	Dir     string   // <state>/rigd
	Resolve Resolver // reads real secrets from the secret store
	Version string
	Now     func() time.Time
	Audit   Audit // default: FileAudit in Dir
	// Policy returns what the last apply approved (default: state.json next to Dir). Sessions are built from it.
	Policy func() (Policies, error)

	// Tests only.
	Dial        func(ctx context.Context, network, addr string) (net.Conn, error)
	UpstreamTLS *tls.Config

	ca       *CA
	store    *SessionStore
	token    string
	api, prx net.Listener
	srvs     []*http.Server
	info     Info
}

// Start opens both loopback listeners, writes the token and info files, and serves in the background.
func (b *Broker) Start() error {
	if b.Resolve == nil {
		return errors.New("rigd: the broker needs a way to read secrets")
	}
	if b.Now == nil {
		b.Now = time.Now
	}
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		return err
	}
	if c, err := ClientFromDir(b.Dir); err == nil {
		if _, err := c.Status(); err == nil {
			return errors.New("rigd: a broker is already running (rigfile broker status)")
		}
	}
	var err error
	if b.ca, err = NewCA(b.Now); err != nil {
		return err
	}
	b.store = NewSessionStore(b.Now)
	b.token = randStr(32)
	if b.Audit == nil {
		b.Audit = &FileAudit{Path: filepath.Join(b.Dir, AuditFile)}
	}
	if b.api, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
		return err
	}
	if b.prx, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
		b.api.Close()
		return err
	}
	prox := &Proxy{CA: b.ca, Sessions: b.store, Audit: b.Audit, Dial: b.Dial, UpstreamTLS: b.UpstreamTLS}
	b.srvs = []*http.Server{
		{Handler: b.apiHandler(), ReadHeaderTimeout: 10 * time.Second},
		{Handler: prox.Handler(), ReadHeaderTimeout: 30 * time.Second},
	}
	go func() { _ = b.srvs[0].Serve(b.api) }()
	go func() { _ = b.srvs[1].Serve(b.prx) }()

	b.info = Info{API: b.api.Addr().String(), Proxy: b.prx.Addr().String(), PID: os.Getpid(), Version: b.Version}
	if err := platform.WritePrivate(filepath.Join(b.Dir, TokenFile), []byte(b.token)); err != nil {
		b.Close()
		return err
	}
	raw, _ := json.Marshal(b.info)
	if err := platform.WritePrivate(filepath.Join(b.Dir, InfoFile), raw); err != nil {
		b.Close()
		return err
	}
	return nil
}

// Info returns where the broker listens (after Start).
func (b *Broker) Info() Info { return b.info }

// Sessions is the live session count.
func (b *Broker) Sessions() int { return b.store.Live() }

// Run starts the broker and blocks until ctx ends.
func (b *Broker) Run(ctx context.Context) error {
	if err := b.Start(); err != nil {
		return err
	}
	<-ctx.Done()
	return b.Close()
}

// Close stops serving, wipes every session's values and removes the token and info files.
func (b *Broker) Close() error {
	for _, s := range b.srvs {
		_ = s.Close()
	}
	if b.store != nil {
		b.store.CloseAll()
	}
	if c, ok := b.Audit.(io.Closer); ok {
		_ = c.Close()
	}
	_ = os.Remove(filepath.Join(b.Dir, InfoFile))
	_ = os.Remove(filepath.Join(b.Dir, TokenFile))
	return nil
}

// OpenReply is the answer to a session request. It never contains a real secret.
type OpenReply struct {
	SessionID  string            `json:"session_id"`
	ProxyURL   string            `json:"proxy_url"`
	CAPEM      string            `json:"ca_pem"`
	Surrogates map[string]string `json:"surrogates"` // env name -> surrogate
	Expires    time.Time         `json:"expires"`
}

// StatusReply is the answer to a status request.
type StatusReply struct {
	Info
	Sessions int `json:"sessions"`
}

func (b *Broker) apiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, StatusReply{Info: b.info, Sessions: b.store.Live()})
	})
	mux.HandleFunc("POST /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		var req SessionRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields() // a request that carries hosts or an allowlist is refused, not ignored
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": "bad request"})
			return
		}
		load := b.Policy
		if load == nil {
			load = func() (Policies, error) { return LoadPolicy(stateDirOf(b.Dir)) }
		}
		pols, err := load()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "the approved policy could not be read"})
			return
		}
		spec, err := pols.Resolve(req)
		if err != nil {
			b.Audit.Log(Event{Time: b.Now().UTC(), Server: req.Server, Method: "session", Decision: "blocked", Reason: err.Error()})
			writeJSON(w, 403, map[string]string{"error": err.Error()})
			return
		}
		s, err := b.store.New(spec, b.Resolve)
		if err != nil {
			// the message names refs and env names, never values
			writeJSON(w, 422, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 201, OpenReply{SessionID: s.ID, ProxyURL: s.ProxyURL(b.info.Proxy), CAPEM: string(b.ca.PEM()), Surrogates: s.Surrogates(spec), Expires: s.Expires})
	})
	mux.HandleFunc("DELETE /v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		b.store.Close(r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})
	return b.guard(mux)
}

// guard is the API's only door: loopback Host (DNS rebinding), no browser Origin, bearer token.
func (b *Broker) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != b.info.API {
			writeJSON(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		if r.Header.Get("Origin") != "" {
			writeJSON(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(b.token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
