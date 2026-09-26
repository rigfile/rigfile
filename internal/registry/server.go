package registry

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/digitaldreamer3462/rigfile/internal/registry/blob"
)

// Server is the registry HTTP service.
type Server struct {
	Cfg   Config
	Store *Store
	Blobs blob.Store
	GH    GitHub
	Log   *slog.Logger
	Lim   *Limiter
	tmpl  *template.Template
}

// NewServer wires the service together.
func NewServer(cfg Config, st *Store, blobs blob.Store, gh GitHub, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	return &Server{Cfg: cfg, Store: st, Blobs: blobs, GH: gh, Log: log, Lim: NewLimiter(st.Now), tmpl: loadTemplates()}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// Handler returns the full HTTP handler with the middleware chain applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.Handle("GET /static/", staticHandler())
	s.authRoutes(mux)
	s.apiRoutes(mux)
	s.pageRoutes(mux)
	return s.recoverer(s.headers(s.logged(mux)))
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.DB.PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "database unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- middleware -----------------------------------------------------------------------------------------------------

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic", "path", r.URL.Path, "value", v)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// headers sets the security headers on every response (docs/registry.md §7).
func (s *Server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self' https://avatars.githubusercontent.com; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if s.Cfg.Secure() {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }

// logged logs method, path (never the query string), status and duration: no tokens, cookies or bodies.
func (s *Server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		s.Log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

// limit answers 429 when key is over its budget.
func (s *Server) limit(w http.ResponseWriter, r *http.Request, name string, perMinute, burst int) bool {
	if s.Lim.Allow(name+"|"+clientIP(r, s.Cfg.TrustProxy), perMinute, burst) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests; slow down"})
	return false
}

// ---- helpers --------------------------------------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// bearer extracts the API token from the Authorization header.
func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// tokenUser authenticates an API request by bearer token; nil means anonymous. A presented but invalid token is an
// error (so a stale token is noticed instead of silently downgraded to anonymous).
func (s *Server) tokenUser(r *http.Request) (*User, bool) {
	tok := bearer(r)
	if tok == "" {
		return nil, true
	}
	u, err := s.Store.TokenUser(r.Context(), tok)
	if err != nil {
		return nil, false
	}
	return u, true
}

// requireToken answers 401 unless the request carries a valid token.
func (s *Server) requireToken(w http.ResponseWriter, r *http.Request) *User {
	u, ok := s.tokenUser(r)
	if !ok || u == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="rigfile"`)
		apiError(w, http.StatusUnauthorized, "sign in with `rigfile login`")
		return nil
	}
	return u
}

const (
	sessionCookie = "rigfile_session"
	oauthCookie   = "rigfile_oauth"
)

func (s *Server) cookieName(base string) string {
	if s.Cfg.Secure() {
		return "__Host-" + base // requires Secure, Path=/ and no Domain: the browser enforces it
	}
	return base
}

func (s *Server) setCookie(w http.ResponseWriter, base, value string, ttl time.Duration) {
	c := &http.Cookie{Name: s.cookieName(base), Value: value, Path: "/", HttpOnly: true, Secure: s.Cfg.Secure(), SameSite: http.SameSiteLaxMode}
	if ttl > 0 {
		c.MaxAge = int(ttl.Seconds())
	} else {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// webUser resolves the session cookie: the signed-in user and the session's CSRF token, or nil.
func (s *Server) webUser(r *http.Request) (*User, string) {
	c, err := r.Cookie(s.cookieName(sessionCookie))
	if err != nil {
		return nil, ""
	}
	u, csrf, err := s.Store.SessionUser(r.Context(), c.Value)
	if err != nil {
		return nil, ""
	}
	return u, csrf
}

// sameOrigin is the Origin/Referer check for state-changing web requests.
func (s *Server) sameOrigin(r *http.Request) bool {
	want := s.Cfg.Origin()
	if o := r.Header.Get("Origin"); o != "" {
		return o == want
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		return err == nil && u.Scheme+"://"+u.Host == want
	}
	return false
}

// webPost checks everything a state-changing web request needs: a session, a same-origin request and the CSRF token
// (form field "csrf"). It returns the user, or writes the refusal and returns nil.
func (s *Server) webPost(w http.ResponseWriter, r *http.Request) *User {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	u, csrf := s.webUser(r)
	if u == nil {
		s.message(w, r, http.StatusUnauthorized, "Sign in first", "Your session has ended. Sign in again and repeat the action.")
		return nil
	}
	if !s.sameOrigin(r) {
		s.message(w, r, http.StatusForbidden, "Request refused", "The request did not come from this site.")
		return nil
	}
	if err := r.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(csrf)) != 1 {
		s.message(w, r, http.StatusForbidden, "Request refused", "The form expired. Go back, reload the page and try again.")
		return nil
	}
	return u
}

// safeNext keeps a post-login redirect on this site: a relative path only.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n\x00") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return next
}

func randomToken(n int) string { return base64.RawURLEncoding.EncodeToString(randomBytes(n)) }
