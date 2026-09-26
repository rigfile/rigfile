package rigd

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
)

// Pattern is a host pattern from the manifest (schema $defs/hostPattern): an exact host, or "*.domain" (one or more labels
// in front of the domain, never the bare domain), each optionally with a port (default 443).
type Pattern struct {
	host     string // lower-case ASCII; for wildcards, the domain after "*."
	wildcard bool
	ip       net.IP
	port     int
}

// ParsePattern parses and validates one pattern.
func ParsePattern(s string) (Pattern, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t/\\@?#") {
		return Pattern{}, fmt.Errorf("rigd: %q is not a host pattern", s)
	}
	p := Pattern{port: 443}
	host := s
	if strings.HasPrefix(s, "[") { // [::1]:8443
		end := strings.Index(s, "]")
		if end < 0 {
			return Pattern{}, fmt.Errorf("rigd: %q is not a host pattern", s)
		}
		host = s[1:end]
		if rest := s[end+1:]; rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return Pattern{}, fmt.Errorf("rigd: %q is not a host pattern", s)
			}
			port, err := strconv.Atoi(rest[1:])
			if err != nil || port < 1 || port > 65535 {
				return Pattern{}, fmt.Errorf("rigd: %q has an invalid port", s)
			}
			p.port = port
		}
	} else if i := strings.LastIndex(s, ":"); i >= 0 && strings.Count(s, ":") == 1 {
		port, err := strconv.Atoi(s[i+1:])
		if err != nil || port < 1 || port > 65535 {
			return Pattern{}, fmt.Errorf("rigd: %q has an invalid port", s)
		}
		host, p.port = s[:i], port
	}
	if ip := net.ParseIP(host); ip != nil {
		p.ip = ip
		return p, nil
	}
	if strings.HasPrefix(host, "*.") {
		p.wildcard = true
		host = host[2:]
	}
	if strings.Contains(host, "*") {
		return Pattern{}, fmt.Errorf("rigd: %q: a wildcard is only allowed as the whole first label", s)
	}
	norm, err := normalizeHost(host)
	if err != nil {
		return Pattern{}, fmt.Errorf("rigd: %q: %v", s, err)
	}
	if p.wildcard && strings.Count(norm, ".") < 1 {
		return Pattern{}, fmt.Errorf("rigd: %q: a wildcard needs at least a registrable domain after it (*.example.com, not *.com)", s)
	}
	if !p.wildcard && norm == "" {
		return Pattern{}, fmt.Errorf("rigd: %q is not a host pattern", s)
	}
	p.host = norm
	return p, nil
}

// normalizeHost lower-cases, removes a trailing dot and converts to ASCII (IDNA).
func normalizeHost(h string) (string, error) {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if h == "" {
		return "", fmt.Errorf("empty host")
	}
	a, err := idna.Lookup.ToASCII(h)
	if err != nil {
		return "", fmt.Errorf("invalid host name")
	}
	if len(a) > 253 || strings.HasPrefix(a, ".") || strings.Contains(a, "..") {
		return "", fmt.Errorf("invalid host name")
	}
	return a, nil
}

// Match reports whether a connect target (host, port) is covered by the pattern.
func (p Pattern) Match(host string, port int) bool {
	if port != p.port {
		return false
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return p.ip != nil && p.ip.Equal(ip)
	}
	if p.ip != nil {
		return false
	}
	h, err := normalizeHost(host)
	if err != nil {
		return false
	}
	if !p.wildcard {
		return h == p.host
	}
	return len(h) > len(p.host)+1 && strings.HasSuffix(h, "."+p.host)
}

// String renders the pattern in canonical form.
func (p Pattern) String() string {
	var h string
	switch {
	case p.ip != nil:
		h = p.ip.String()
	case p.wildcard:
		h = "*." + p.host
	default:
		h = p.host
	}
	if p.port != 443 {
		return h + ":" + strconv.Itoa(p.port)
	}
	return h
}

// ParsePatterns parses a list, failing on the first bad entry.
func ParsePatterns(ss []string) ([]Pattern, error) {
	out := make([]Pattern, 0, len(ss))
	for _, s := range ss {
		p, err := ParsePattern(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// MatchAny reports whether any pattern covers the target.
func MatchAny(ps []Pattern, host string, port int) bool {
	for _, p := range ps {
		if p.Match(host, port) {
			return true
		}
	}
	return false
}
