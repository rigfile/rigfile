package rigd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Proxy is the egress proxy (docs/rigd.md §5). Handler serves CONNECT requests from children that were given
// HTTPS_PROXY by a session; everything else is refused.
type Proxy struct {
	CA       *CA
	Sessions *SessionStore
	Audit    Audit

	// MaxBody is the largest request body that is scanned for surrogates (default 1 MiB). Larger or non-text bodies pass
	// through unchanged: a surrogate inside one is never swapped, so it fails closed at the API.
	MaxBody int64

	// Dial opens the upstream connection. The default refuses addresses a name should never resolve to (loopback,
	// private, link-local) unless the host was written as an IP literal; tests replace it.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// UpstreamTLS configures verification of the real server (default: the system trust store). Tests only.
	UpstreamTLS *tls.Config

	once sync.Once
	tr   *http.Transport
}

func (p *Proxy) init() {
	p.once.Do(func() {
		dial := p.Dial
		if dial == nil {
			dial = safeDial
		}
		p.tr = &http.Transport{
			DialContext: dial, TLSClientConfig: p.UpstreamTLS, DisableCompression: true, ForceAttemptHTTP2: true,
			MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 60 * time.Second,
		}
		if p.MaxBody == 0 {
			p.MaxBody = 1 << 20
		}
	})
}

// safeDial connects like net.Dialer but refuses to reach loopback, private, link-local and unspecified addresses when the
// destination was a NAME: an allowlisted host name that resolves into the user's own network would turn the proxy into a
// way to reach internal services. An IP literal in the allowlist is an explicit choice and is allowed.
func safeDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	literal := net.ParseIP(host) != nil
	d := &net.Dialer{Timeout: 15 * time.Second}
	if !literal {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			h, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(h); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsLinkLocalMulticast()) {
				return fmt.Errorf("rigd: %s resolves to a non-public address (%s); refusing", host, h)
			}
			return nil
		}
	}
	return d.DialContext(ctx, network, addr)
}

// Handler is the proxy's http.Handler.
func (p *Proxy) Handler() http.Handler {
	p.init()
	return http.HandlerFunc(p.serve)
}

func (p *Proxy) log(e Event) {
	if p.Audit != nil {
		e.Time = time.Now().UTC()
		p.Audit.Log(e)
	}
}

func basicAuth(h string) (user, pass string, ok bool) {
	const pfx = "Basic "
	if len(h) < len(pfx) || !strings.EqualFold(h[:len(pfx)], pfx) {
		return "", "", false
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(h[len(pfx):]))
	if err != nil {
		return "", "", false
	}
	u, pw, found := strings.Cut(string(b), ":")
	return u, pw, found
}

func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	if ip := net.ParseIP(hostOnly(r.RemoteAddr)); ip == nil || !ip.IsLoopback() {
		http.Error(w, "loopback only", http.StatusForbidden)
		return
	}
	user, pass, ok := basicAuth(r.Header.Get("Proxy-Authorization"))
	var sess *Session
	if ok {
		sess, ok = p.Sessions.Authenticate(user, pass)
	}
	if !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="rigd"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method != http.MethodConnect {
		p.log(Event{Session: sess.ID, Server: sess.Server, Method: r.Method, Host: r.Host, Decision: "blocked", Reason: "only CONNECT is supported: plain HTTP would carry secrets unprotected", Status: 405})
		http.Error(w, "only HTTPS through CONNECT is supported", http.StatusMethodNotAllowed)
		return
	}
	host, port := splitTarget(r.Host)
	if host == "" {
		http.Error(w, "bad target", http.StatusBadRequest)
		return
	}
	if !sess.Allowed(host, port) {
		p.log(Event{Session: sess.ID, Server: sess.Server, Method: "CONNECT", Host: hostPort(host, port), Decision: "blocked", Reason: "host not allowed by the server's network.allow", Status: 403})
		http.Error(w, "rigd: "+host+" is not in this server's network.allow", http.StatusForbidden)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot hijack", http.StatusInternalServerError)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")

	leaf, err := p.CA.LeafFor(host)
	if err != nil {
		return
	}
	tconn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*leaf}, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
	_ = tconn.SetDeadline(time.Now().Add(20 * time.Second))
	if err := tconn.Handshake(); err != nil {
		p.log(Event{Session: sess.ID, Server: sess.Server, Method: "CONNECT", Host: hostPort(host, port), Decision: "blocked", Reason: "TLS handshake failed: the client does not trust the session CA"})
		return
	}
	_ = tconn.SetDeadline(time.Time{})
	h := &mitm{p: p, sess: sess, host: host, port: port}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
	_ = srv.Serve(&oneConn{c: tconn, done: make(chan struct{})})
}

// oneConn is a listener that yields a single connection, then reports closed once that connection is done.
type oneConn struct {
	c    net.Conn
	once sync.Once
	done chan struct{}
}

func (l *oneConn) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = &notifyConn{Conn: l.c, closed: l.done} })
	if c != nil {
		return c, nil
	}
	<-l.done
	return nil, io.EOF
}

func (l *oneConn) Close() error   { return nil }
func (l *oneConn) Addr() net.Addr { return l.c.LocalAddr() }

type notifyConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *notifyConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// mitm handles the requests inside one intercepted connection.
type mitm struct {
	p    *Proxy
	sess *Session
	host string
	port int
}

var hopHeaders = []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Accept-Encoding"}

func textLike(ct string) bool {
	ct = strings.ToLower(ct)
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)
	switch {
	case ct == "", strings.HasPrefix(ct, "text/"), strings.Contains(ct, "json"), strings.Contains(ct, "xml"), strings.Contains(ct, "javascript"), ct == "application/x-www-form-urlencoded":
		return true
	}
	return false
}

// found lists the bindings whose surrogate appears in any of the strings.
func (s *Session) found(strs ...string) []*binding {
	var out []*binding
	for _, b := range s.bindings {
		for _, x := range strs {
			if strings.Contains(x, b.surrogate) {
				out = append(out, b)
				break
			}
		}
	}
	return out
}

func refs(bs []*binding) []string {
	var out []string
	seen := map[string]bool{}
	for _, b := range bs {
		if !seen[b.ref] {
			seen[b.ref] = true
			out = append(out, b.ref)
		}
	}
	sort.Strings(out)
	return out
}

// redactSurrogates makes a path safe to log: any surrogate becomes a marker.
func (s *Session) redactSurrogates(x string) string {
	for _, b := range s.bindings {
		x = strings.ReplaceAll(x, b.surrogate, "<surrogate:"+b.ref+">")
	}
	return x
}

func (m *mitm) event(r *http.Request, status int, decision, reason string, rs []string) {
	m.p.log(Event{Session: m.sess.ID, Server: m.sess.Server, Method: r.Method, Host: hostPort(m.host, m.port), Path: m.sess.redactSurrogates(r.URL.Path), Status: status, Decision: decision, Reason: reason, Secrets: rs})
}

func (m *mitm) block(w http.ResponseWriter, r *http.Request, status int, msg, reason string, rs []string) {
	m.event(r, status, "blocked", reason, rs)
	http.Error(w, "rigd: "+msg, status)
}

func (m *mitm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s := m.sess
	if _, live := m.p.Sessions.Get(s.ID); !live {
		m.block(w, r, http.StatusForbidden, "the session has ended", "session ended", nil)
		return
	}
	// a request for a different host than the tunnel was opened to is domain fronting
	if h, p := splitTarget(r.Host); h != m.host || p != m.port {
		m.block(w, r, http.StatusMisdirectedRequest, "the Host header does not match the tunnel", "host header mismatch", nil)
		return
	}
	if r.Header.Get("Upgrade") != "" || strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		m.block(w, r, http.StatusNotImplemented, "protocol upgrades (WebSocket) are not supported through the broker", "upgrade refused", nil)
		return
	}

	// 1. find surrogates in the request line and headers
	var hv []string
	for k, vs := range r.Header {
		hv = append(hv, k)
		hv = append(hv, vs...)
	}
	found := s.found(append(hv, r.URL.Path, r.URL.RawQuery)...)

	// 2. the body, when it is small text (the only body that is scanned)
	var body []byte
	var bodyReader io.Reader = r.Body
	bodyScanned := false
	if r.Body != nil && r.Body != http.NoBody && textLike(r.Header.Get("Content-Type")) && (r.ContentLength < 0 || r.ContentLength <= m.p.MaxBody) {
		b, err := io.ReadAll(io.LimitReader(r.Body, m.p.MaxBody+1))
		if err != nil {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		if int64(len(b)) <= m.p.MaxBody {
			body, bodyScanned = b, true
			found = append(found, s.found(string(b))...)
		} else {
			bodyReader = io.MultiReader(bytes.NewReader(b), r.Body) // too big to scan: pass through unchanged
		}
	}
	found = dedupe(found)

	// 3. a surrogate may only travel to the host its secret is bound to
	for _, b := range found {
		if !MatchAny(b.hosts, m.host, m.port) {
			m.block(w, r, http.StatusForbidden, "a secret for another service was sent to "+m.host, "surrogate "+b.ref+" not bound to "+hostPort(m.host, m.port), refs(found))
			return
		}
	}

	// 4. swap
	pairsReq := make([]pair, 0, len(found))
	for _, b := range found {
		pairsReq = append(pairsReq, pair{[]byte(b.surrogate), b.real})
	}
	swap := func(x string) string {
		for _, pr := range pairsReq {
			x = strings.ReplaceAll(x, string(pr.from), string(pr.to))
		}
		return x
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.URL = &url.URL{Scheme: "https", Host: hostPort(m.host, m.port), Path: swap(r.URL.Path), RawQuery: swap(r.URL.RawQuery)}
	if r.URL.RawPath != "" {
		out.URL.RawPath = swap(r.URL.RawPath)
	}
	out.Host = r.Host
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	for k, vs := range out.Header {
		for i, v := range vs {
			vs[i] = swap(v)
		}
		out.Header[k] = vs
	}
	switch {
	case bodyScanned:
		nb := []byte(swap(string(body)))
		out.Body = io.NopCloser(bytes.NewReader(nb))
		out.ContentLength = int64(len(nb))
		out.Header.Del("Content-Length")
	case bodyReader != nil && r.Body != nil && r.Body != http.NoBody:
		out.Body = io.NopCloser(bodyReader)
	}

	// 5. forward
	resp, err := m.p.tr.RoundTrip(out)
	if err != nil {
		m.event(r, http.StatusBadGateway, "blocked", "upstream error: "+shorten(err.Error()), refs(found))
		http.Error(w, "rigd: could not reach "+m.host, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 6. responses never carry the real value back to the child
	var back []pair
	for _, b := range s.bindings {
		back = append(back, pair{b.real, []byte(b.surrogate)})
	}
	for k, vs := range resp.Header {
		if isHop(k) {
			continue
		}
		for _, v := range vs {
			for _, pr := range back {
				v = strings.ReplaceAll(v, string(pr.from), string(pr.to))
			}
			w.Header().Add(k, v)
		}
	}
	var rd io.Reader = resp.Body
	if textLike(resp.Header.Get("Content-Type")) || strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		rd = newStreamReplacer(resp.Body, back)
		w.Header().Del("Content-Length") // the length may change
	}
	w.WriteHeader(resp.StatusCode)
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 16<<10)
	for {
		n, err := rd.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			break
		}
	}
	decision := "allowed"
	if len(found) > 0 {
		decision = "swapped"
	}
	m.event(r, resp.StatusCode, decision, "", refs(found))
}

func isHop(k string) bool {
	for _, h := range hopHeaders {
		if strings.EqualFold(h, k) {
			return true
		}
	}
	return false
}

func dedupe(bs []*binding) []*binding {
	seen := map[*binding]bool{}
	var out []*binding
	for _, b := range bs {
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	return out
}

func shorten(s string) string {
	if len(s) > 120 {
		return s[:120]
	}
	return s
}

func hostOnly(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

func hostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(port)
}

// splitTarget parses host[:port] (default 443), lower-cased and IDNA-normalised, without brackets.
func splitTarget(t string) (string, int) {
	host, portStr, err := net.SplitHostPort(t)
	port := 443
	if err != nil {
		host = strings.Trim(t, "[]")
	} else if n, perr := strconv.Atoi(portStr); perr == nil && n > 0 && n < 65536 {
		port = n
	} else {
		return "", 0
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), port
	}
	n, nerr := normalizeHost(host)
	if nerr != nil {
		return "", 0
	}
	return n, port
}
