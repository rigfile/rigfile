// Package localui is `rigfile ui`: a small web page on the loopback interface that shows what applying a rig would do and
// the checklist of secrets and logins it still needs, for people who do not live in a terminal.
//
// It is deliberately narrow. It shows the same plan text the command line shows, lets the person store the secrets the rig
// declares (and only those), and applies the rig after an explicit confirmation. It is guarded like the broker's API:
// loopback only, the Host header must be the listener's own address (DNS rebinding), a browser Origin must be the page's
// own, a random access token that is swapped for a SameSite=Strict cookie on first use (so it does not stay in the
// address bar or the history), and a CSRF token on every POST. There is no script and no inline style.
package localui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Need is one line of the checklist.
type Need struct {
	Kind        string // "secret" or "login"
	Ref         string
	Description string
	ObtainURL   string
	Method      string
	Status      string // secrets: "set", "not set" or "" when it cannot be told
}

// Backend is what the page needs from the rest of Rigfile. The command wires the real thing; tests wire fakes.
type Backend struct {
	Title string
	// Plan returns the review text (the same as `rigfile plan`).
	Plan func() (string, error)
	// Needs returns the checklist with current statuses.
	Needs func() ([]Need, error)
	// SetSecret stores a value for a ref that Needs lists. The value is never returned, logged or echoed.
	SetSecret func(ref string, value []byte) error
	// Apply applies the rig without asking again (the page asked) and returns the text the command line would print.
	Apply func() (string, error)
}

// Server serves the page.
type Server struct {
	B    Backend
	Idle time.Duration // stop after this long without a request (default 30 minutes)

	token string
	csrf  string
	addr  string
	ln    net.Listener
	srv   *http.Server
	tmpl  *template.Template
	mu    sync.Mutex
	last  time.Time
	done  chan struct{}
	once  sync.Once
}

//go:embed style.css
var css string

const cookieName = "rigfile_ui"

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("localui: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Start listens on a random loopback port and returns the URL to open (it carries the one-time access token).
func (s *Server) Start() (string, error) {
	var err error
	if s.ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
		return "", err
	}
	s.addr = s.ln.Addr().String()
	s.token = randHex(24)
	sum := sha256.Sum256([]byte("csrf:" + s.token))
	s.csrf = hex.EncodeToString(sum[:])
	s.tmpl = template.Must(template.New("").Parse(pageTemplates))
	s.last = time.Now()
	s.done = make(chan struct{})
	if s.Idle == 0 {
		s.Idle = 30 * time.Minute
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write([]byte(css))
	})
	mux.HandleFunc("POST /secret", s.secret)
	mux.HandleFunc("POST /apply", s.apply)
	mux.HandleFunc("POST /quit", s.quit)
	s.srv = &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(s.ln) }()
	go s.watchIdle()
	return "http://" + s.addr + "/?t=" + s.token, nil
}

// Wait blocks until the page was closed with the Quit button, the idle timeout ran out, or ctx ended; it then shuts down.
func (s *Server) Wait(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-s.done:
	}
	sc, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(sc)
}

func (s *Server) finish() { s.once.Do(func() { close(s.done) }) }

func (s *Server) watchIdle() {
	for {
		select {
		case <-s.done:
			return
		case <-time.After(time.Second):
		}
		s.mu.Lock()
		idle := time.Since(s.last)
		s.mu.Unlock()
		if idle > s.Idle {
			s.finish()
			return
		}
	}
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// guard is the only door: Host, Origin, Fetch-Metadata, then the cookie (or the one-time token on the landing request).
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		s.mu.Lock()
		s.last = time.Now()
		s.mu.Unlock()
		if r.Host != s.addr {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// A browser sends the literal string "null" as Origin, not a real origin, on a top-level navigational POST
		// (a plain <form method="post">, which is everything this page ever submits: there is no script to use
		// fetch) when the page's own Referrer-Policy is no-referrer, exactly as set below — this is expected,
		// spec-correct behaviour (confirmed live: real Chrome 153, Sec-Fetch-Site: same-origin, Host matching),
		// not a forged header. Treat it like an absent Origin and lean on Sec-Fetch-Site and the CSRF token below,
		// which a cross-site request cannot produce.
		if o := r.Header.Get("Origin"); o != "" && o != "null" && o != "http://"+s.addr {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/style.css" {
			next.ServeHTTP(w, r)
			return
		}
		// landing: swap the one-time token in the address for a cookie, then drop it from the URL
		if t := r.URL.Query().Get("t"); t != "" && r.Method == http.MethodGet && r.URL.Path == "/" {
			if !eq(t, s.token) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil || !eq(c.Value, s.token) {
			http.Error(w, "forbidden: open the address `rigfile ui` printed", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, maxSecret+4096)
			if err := r.ParseForm(); err != nil || !eq(r.PostFormValue("csrf"), s.csrf) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type view struct {
	Title   string
	CSRF    string
	Plan    string
	PlanErr string
	Needs   []Need
	Notice  string
	Result  string
	Failed  bool
}

func (s *Server) render(w http.ResponseWriter, name string, status int, v view) {
	v.Title, v.CSRF = s.B.Title, s.csrf
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = s.tmpl.ExecuteTemplate(w, name, v)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	var v view
	if plan, err := s.B.Plan(); err != nil {
		v.PlanErr = err.Error()
	} else {
		v.Plan = plan
	}
	if needs, err := s.B.Needs(); err == nil {
		v.Needs = needs
	}
	switch r.URL.Query().Get("done") {
	case "secret":
		v.Notice = "Saved. The value is stored in your secret store and is not shown again."
	}
	s.render(w, "home", http.StatusOK, v)
}

const maxSecret = 16 << 10

func (s *Server) secret(w http.ResponseWriter, r *http.Request) {
	ref := r.PostFormValue("ref")
	value := strings.TrimRight(r.PostFormValue("value"), "\r\n")
	defer func() { value = "" }()
	if value == "" || len(value) > maxSecret {
		s.render(w, "result", http.StatusBadRequest, view{Result: "Nothing was stored: the value was empty or too long.", Failed: true})
		return
	}
	// only what this rig declares can be set from the page
	needs, err := s.B.Needs()
	ok := false
	for _, n := range needs {
		if err == nil && n.Kind == "secret" && n.Ref == ref {
			ok = true
		}
	}
	if !ok {
		s.render(w, "result", http.StatusBadRequest, view{Result: "That secret is not one this rig asks for.", Failed: true})
		return
	}
	if err := s.B.SetSecret(ref, []byte(value)); err != nil {
		s.render(w, "result", http.StatusInternalServerError, view{Result: "Could not store the secret: " + redact(err.Error(), value), Failed: true})
		return
	}
	http.Redirect(w, r, "/?"+url.Values{"done": {"secret"}}.Encode(), http.StatusSeeOther)
}

// redact removes a value from text about to be shown, in case an error message quotes it.
func redact(msg, value string) string {
	if value == "" {
		return msg
	}
	return strings.ReplaceAll(msg, value, "[value]")
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	if r.PostFormValue("confirm") != "yes" {
		s.render(w, "result", http.StatusBadRequest, view{Result: "Tick the box to confirm you have read the plan.", Failed: true})
		return
	}
	out, err := s.B.Apply()
	v := view{Result: out}
	if err != nil {
		v.Failed = true
		v.Result += "\n" + err.Error()
	}
	s.render(w, "result", http.StatusOK, v)
}

func (s *Server) quit(w http.ResponseWriter, r *http.Request) {
	s.render(w, "result", http.StatusOK, view{Result: "You can close this tab. The page has stopped."})
	go func() { time.Sleep(200 * time.Millisecond); s.finish() }()
}
