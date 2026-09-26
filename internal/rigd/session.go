package rigd

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// SurrogatePrefix marks a surrogate value: recognisable so the proxy can find it, meaningless everywhere else.
const SurrogatePrefix = "rgs_sur_"

// MinSecretLen is the shortest real secret the broker will substitute: replacing (and scrubbing) a very short value could
// corrupt unrelated text.
const MinSecretLen = 8

// SecretSpec is one secret a server needs.
type SecretSpec struct {
	Env   string   // environment variable the child reads
	Ref   string   // the secret store reference (never sent to the child)
	Hosts []string // where the real value may be sent (host patterns)
}

// SessionSpec is what `rigfile exec` asks for.
type SessionSpec struct {
	Server  string
	Secrets []SecretSpec
	Allow   []string // the server's network.allow patterns
}

// binding ties a surrogate to a real value and the hosts it may reach.
type binding struct {
	ref       string
	surrogate string
	real      []byte
	hosts     []Pattern
}

// Session is one launch of one server.
type Session struct {
	ID       string
	Server   string
	User     string // proxy credentials for this session
	Password string
	Expires  time.Time

	allow    []Pattern
	bindings []*binding
	bySur    map[string]*binding
}

// Surrogates returns env name -> surrogate for the child.
func (s *Session) Surrogates(spec SessionSpec) map[string]string {
	out := map[string]string{}
	for i, sp := range spec.Secrets {
		out[sp.Env] = s.bindings[i].surrogate
	}
	return out
}

// Allowed reports whether a CONNECT target is in the server's allowlist.
func (s *Session) Allowed(host string, port int) bool { return MatchAny(s.allow, host, port) }

// SessionStore holds live sessions.
type SessionStore struct {
	mu      sync.Mutex
	byID    map[string]*Session
	byUser  map[string]*Session
	now     func() time.Time
	TTL     time.Duration
	MaxLive int
}

// NewSessionStore makes an empty store (now nil = time.Now).
func NewSessionStore(now func() time.Time) *SessionStore {
	if now == nil {
		now = time.Now
	}
	return &SessionStore{byID: map[string]*Session{}, byUser: map[string]*Session{}, now: now, TTL: 24 * time.Hour, MaxLive: 256}
}

func randStr(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("rigd: no randomness: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Resolver reads a real secret from the secret store.
type Resolver func(ref string) ([]byte, error)

// New creates a session: it parses the patterns, reads the real values through resolve, and mints surrogates. It refuses a
// spec that gives the broker nothing to enforce.
func (st *SessionStore) New(spec SessionSpec, resolve Resolver) (*Session, error) {
	if spec.Server == "" {
		return nil, errors.New("rigd: a session needs a server name")
	}
	allow, err := ParsePatterns(spec.Allow)
	if err != nil {
		return nil, err
	}
	if len(allow) == 0 {
		return nil, fmt.Errorf("rigd: server %q declares no network.allow, so there is nothing to enforce", spec.Server)
	}
	s := &Session{ID: randStr(12), Server: spec.Server, User: "s-" + randStr(9), Password: randStr(24), allow: allow, bySur: map[string]*binding{}}
	seenEnv := map[string]bool{}
	for _, sp := range spec.Secrets {
		if seenEnv[sp.Env] {
			return nil, fmt.Errorf("rigd: %s is given twice", sp.Env)
		}
		seenEnv[sp.Env] = true
		hosts, err := ParsePatterns(sp.Hosts)
		if err != nil {
			return nil, err
		}
		if len(hosts) == 0 {
			return nil, fmt.Errorf("rigd: secret %s has no bound hosts (secrets.%s.hosts), so it cannot be substituted safely", sp.Ref, sp.Ref)
		}
		real, err := resolve(sp.Ref)
		if err != nil {
			return nil, fmt.Errorf("rigd: reading secret %s: %w", sp.Ref, err)
		}
		if len(real) < MinSecretLen {
			return nil, fmt.Errorf("rigd: secret %s is too short (%d bytes) to substitute safely", sp.Ref, len(real))
		}
		b := &binding{ref: sp.Ref, surrogate: SurrogatePrefix + randStr(24), real: append([]byte(nil), real...), hosts: hosts}
		s.bindings = append(s.bindings, b)
		s.bySur[b.surrogate] = b
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sweepLocked()
	if len(st.byID) >= st.MaxLive {
		return nil, errors.New("rigd: too many live sessions")
	}
	s.Expires = st.now().Add(st.TTL)
	st.byID[s.ID] = s
	st.byUser[s.User] = s
	return s, nil
}

func (st *SessionStore) sweepLocked() {
	now := st.now()
	for id, s := range st.byID {
		if !now.Before(s.Expires) {
			st.dropLocked(id, s)
		}
	}
}

func (st *SessionStore) dropLocked(id string, s *Session) {
	for _, b := range s.bindings {
		for i := range b.real {
			b.real[i] = 0 // best effort: do not leave the value lying in a dead session
		}
	}
	delete(st.byID, id)
	delete(st.byUser, s.User)
}

// Close ends a session and wipes its values.
func (st *SessionStore) Close(id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.byID[id]
	if ok {
		st.dropLocked(id, s)
	}
	return ok
}

// Get finds a live session by id.
func (st *SessionStore) Get(id string) (*Session, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.byID[id]
	if !ok || !st.now().Before(s.Expires) {
		return nil, false
	}
	return s, true
}

// Authenticate finds the session for proxy credentials, comparing the password in constant time.
func (st *SessionStore) Authenticate(user, password string) (*Session, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.byUser[user]
	if !ok || !st.now().Before(s.Expires) {
		return nil, false
	}
	if subtle.ConstantTimeCompare([]byte(s.Password), []byte(password)) != 1 {
		return nil, false
	}
	return s, true
}

// Live is the number of live sessions.
func (st *SessionStore) Live() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sweepLocked()
	return len(st.byID)
}

// ProxyURL is the value for HTTPS_PROXY: the session's credentials on the proxy's loopback address.
func (s *Session) ProxyURL(addr string) string {
	return "http://" + s.User + ":" + s.Password + "@" + addr
}

// hasSurrogate is a cheap check used by the proxy before doing any work.
func hasSurrogate(s string) bool { return strings.Contains(s, SurrogatePrefix) }
