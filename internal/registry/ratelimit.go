package registry

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Limiter is an in-memory token bucket per key. It is per process: with several instances each has its own budget (a
// shared limiter is an operations follow-up, docs/registry.md §8).
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter returns a limiter using the given clock (nil = time.Now).
func NewLimiter(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{buckets: map[string]*bucket{}, now: now}
}

// Allow takes one token from key's bucket, which refills at perMinute and holds at most burst.
func (l *Limiter) Allow(key string, perMinute, burst int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) > 50000 {
			for k, v := range l.buckets { // shed idle buckets so the map cannot grow without bound
				if now.Sub(v.last) > 10*time.Minute {
					delete(l.buckets, k)
				}
			}
		}
		b = &bucket{tokens: float64(burst), last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * float64(perMinute) / 60
	if b.tokens > float64(burst) {
		b.tokens = float64(burst)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// clientIP is the address to rate-limit on: the socket peer, or, behind a trusted reverse proxy, the last
// X-Forwarded-For entry (the one the proxy itself appended).
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
