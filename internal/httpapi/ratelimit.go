package httpapi

import (
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Limiter is an in-memory token bucket keyed by an arbitrary string.
type Limiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	perSecond float64
	burst     float64
	now       func() time.Time
	nextSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter allows perMinute requests per minute with the given burst. perMinute <= 0 disables
// limiting.
func NewLimiter(perMinute, burst int) *Limiter {
	if burst < 1 {
		burst = 1
	}
	return &Limiter{buckets: map[string]*bucket{}, perSecond: float64(perMinute) / 60, burst: float64(burst), now: time.Now}
}

// Allow takes a token for key. When none is left it returns how long to wait.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l.perSecond <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.After(l.nextSweep) {
		for k, b := range l.buckets {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.nextSweep = now.Add(time.Minute)
	}
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.perSecond)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.perSecond * float64(time.Second))
}

// clientIP returns the caller's address. X-Forwarded-For is honoured only when the direct peer is
// a trusted proxy, and is read from the right so a spoofed left side is ignored.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if !isTrusted(peer, trusted) {
		return peer.String()
	}
	parts := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			continue
		}
		a = a.Unmap()
		if !isTrusted(a, trusted) || i == 0 {
			return a.String()
		}
	}
	return peer.String()
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
