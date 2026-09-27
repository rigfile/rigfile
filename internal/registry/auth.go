package registry

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Server) authRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", s.login)
	mux.HandleFunc("GET /auth/callback", s.callback)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /device", s.deviceForm)
	mux.HandleFunc("POST /device", s.deviceDecide)
	mux.HandleFunc("POST /v1/device/code", s.deviceCode)
	mux.HandleFunc("POST /v1/device/token", s.deviceToken)
	mux.HandleFunc("GET /v1/me", s.me)
	mux.HandleFunc("DELETE /v1/tokens/current", s.revokeCurrent)
}

func (s *Server) callbackURL() string { return s.Cfg.Origin() + "/auth/callback" }

// login starts the GitHub OAuth flow: a random state bound to a short-lived cookie, plus where to go afterwards.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "login", 30, 10) {
		return
	}
	state := randomToken(24)
	next := url.QueryEscape(safeNext(r.URL.Query().Get("next")))
	s.setCookie(w, oauthCookie, state+"."+next, 10*time.Minute)
	http.Redirect(w, r, s.GH.AuthorizeURL(state, s.callbackURL()), http.StatusFound)
}

// callback finishes the flow. The state must match the cookie (constant time) and is single use.
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "login", 30, 10) {
		return
	}
	c, err := r.Cookie(s.cookieName(oauthCookie))
	s.setCookie(w, oauthCookie, "", 0) // single use, whatever happens next
	if err != nil {
		s.message(w, r, http.StatusBadRequest, "Sign-in failed", "The sign-in did not start on this browser. Try again.")
		return
	}
	state, nextEnc, _ := strings.Cut(c.Value, ".")
	q := r.URL.Query()
	if state == "" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 || q.Get("code") == "" {
		s.message(w, r, http.StatusBadRequest, "Sign-in failed", "The sign-in could not be verified. Try again.")
		return
	}
	tok, err := s.GH.Exchange(r.Context(), q.Get("code"), s.callbackURL())
	if err != nil {
		s.Log.Warn("github exchange failed", "err", err)
		s.message(w, r, http.StatusBadGateway, "Sign-in failed", "GitHub did not accept the sign-in. Try again.")
		return
	}
	gu, err := s.GH.User(r.Context(), tok)
	if err != nil {
		s.Log.Warn("github user failed", "err", err)
		s.message(w, r, http.StatusBadGateway, "Sign-in failed", "Could not read your GitHub account. Try again.")
		return
	}
	u, err := s.Store.UpsertUser(r.Context(), gu, s.Cfg.IsAdmin(gu.Login))
	if err != nil {
		if err == ErrDisabled {
			s.message(w, r, http.StatusForbidden, "Account disabled", "This account has been disabled. See the takedown and abuse policy.")
			return
		}
		if errors.Is(err, ErrLoginReserved) {
			s.message(w, r, http.StatusForbidden, "Name reserved", "This GitHub name was used here by another account that has since been renamed, and it stays reserved for that account so nobody can publish under a name others trust. If this is your name, contact the operator.")
			return
		}
		s.Log.Warn("cannot record user", "login", gu.Login, "err", err)
		s.message(w, r, http.StatusBadRequest, "Sign-in failed", "This GitHub account name cannot be used here yet (letters, digits and hyphens only).")
		return
	}
	cookie, _, err := s.Store.CreateSession(r.Context(), u.ID, s.Cfg.SessionTTL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, sessionCookie, cookie, s.Cfg.SessionTTL)
	s.Store.Audit(r.Context(), u, "login", "web", nil)
	next, _ := url.QueryUnescape(nextEnc)
	http.Redirect(w, r, safeNext(next), http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if s.webPost(w, r) == nil {
		return
	}
	if c, err := r.Cookie(s.cookieName(sessionCookie)); err == nil {
		_ = s.Store.DeleteSession(r.Context(), c.Value)
	}
	s.setCookie(w, sessionCookie, "", 0)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---- device flow: the pages ----------------------------------------------------------------------------------------

func (s *Server) deviceForm(w http.ResponseWriter, r *http.Request) {
	code := NormalizeUserCode(r.URL.Query().Get("user_code"))
	u, csrf := s.webUser(r)
	if u == nil {
		next := "/device"
		if code != "" {
			next += "?user_code=" + url.QueryEscape(code)
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusFound)
		return
	}
	if r.URL.Query().Get("user_code") == "" {
		s.render(w, r, http.StatusOK, "device_enter.html", Page{Title: "Sign in the CLI", User: u, CSRF: csrf})
		return
	}
	if code == "" || !s.limit(w, r, "devicecode", 20, 10) || !s.Store.DeviceExists(r.Context(), code) {
		s.render(w, r, http.StatusNotFound, "device_enter.html", Page{Title: "Sign in the CLI", User: u, CSRF: csrf, Error: "That code is not valid or has expired. Run `rigfile login` again."})
		return
	}
	s.render(w, r, http.StatusOK, "device.html", Page{Title: "Sign in the CLI", User: u, CSRF: csrf, Code: code})
}

func (s *Server) deviceDecide(w http.ResponseWriter, r *http.Request) {
	u := s.webPost(w, r)
	if u == nil {
		return
	}
	if !s.limit(w, r, "devicecode", 20, 10) {
		return
	}
	code := NormalizeUserCode(r.PostFormValue("user_code"))
	approve := r.PostFormValue("decision") == "approve"
	if code == "" || s.Store.DecideDevice(r.Context(), code, u.ID, approve) != nil {
		s.message(w, r, http.StatusNotFound, "Code not valid", "That code is not valid or has expired. Run `rigfile login` again.")
		return
	}
	s.Store.Audit(r.Context(), u, map[bool]string{true: "device.approve", false: "device.deny"}[approve], "cli", nil)
	if approve {
		s.message(w, r, http.StatusOK, "CLI signed in", "You can return to your terminal.")
		return
	}
	s.message(w, r, http.StatusOK, "Sign-in denied", "The CLI was not signed in.")
}

// ---- device flow: the API (RFC 8628) ------------------------------------------------------------------------------

const cliClientID = "rigfile-cli"

func (s *Server) deviceCode(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "devicestart", 10, 5) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	_ = r.ParseForm()
	if id := r.PostFormValue("client_id"); id != cliClientID {
		apiError(w, http.StatusBadRequest, "unknown client_id")
		return
	}
	const ttl = 15 * time.Minute
	interval := s.Cfg.DeviceInterval
	if interval < 1 {
		interval = 5
	}
	dc, uc, err := s.Store.CreateDevice(r.Context(), ttl, interval)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "could not start a sign-in")
		return
	}
	uri := s.Cfg.PublicURL + "/device"
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code": dc, "user_code": uc, "verification_uri": uri, "verification_uri_complete": uri + "?user_code=" + url.QueryEscape(uc),
		"expires_in": int(ttl.Seconds()), "interval": interval,
	})
}

func (s *Server) deviceToken(w http.ResponseWriter, r *http.Request) {
	if !s.limit(w, r, "devicepoll", 120, 30) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	_ = r.ParseForm()
	if r.PostFormValue("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.PostFormValue("client_id") != cliClientID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	p, err := s.Store.PollDevice(r.Context(), r.PostFormValue("device_code"), s.Cfg.TokenTTL)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "try again")
		return
	}
	if p.Error != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": p.Error, "interval": p.Interval})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": p.Token, "token_type": "Bearer", "expires_in": int(s.Cfg.TokenTTL.Seconds())})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"login": u.Login, "name": u.Name, "admin": u.IsAdmin})
}

func (s *Server) revokeCurrent(w http.ResponseWriter, r *http.Request) {
	u := s.requireToken(w, r)
	if u == nil {
		return
	}
	_ = s.Store.RevokeToken(r.Context(), bearer(r))
	s.Store.Audit(r.Context(), u, "token.revoke", "current", nil)
	w.WriteHeader(http.StatusNoContent)
}
