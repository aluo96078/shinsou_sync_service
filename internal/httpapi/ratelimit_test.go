package httpapi

import (
	"net/http"
	"net/netip"
	"testing"
	"time"
)

func TestLimiterRefills(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter(60, 2)
	l.now = func() time.Time { return now }
	for i := range 2 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d should pass", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait <= 0 || wait > 2*time.Second {
		t.Fatalf("third request should be limited, got ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("other keys are independent")
	}
	now = now.Add(1100 * time.Millisecond)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("token should have refilled")
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	mk := func(remote, xff string) *http.Request {
		r, _ := http.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.9:5000", "1.2.3.4", "203.0.113.9"}, // untrusted peer: header ignored
		{"10.1.1.1:5000", "1.2.3.4", "1.2.3.4"},
		{"10.1.1.1:5000", "6.6.6.6, 1.2.3.4, 10.2.2.2", "1.2.3.4"}, // spoofed left side ignored
		{"10.1.1.1:5000", "", "10.1.1.1"},
	}
	for _, c := range cases {
		if got := clientIP(mk(c.remote, c.xff), trusted); got != c.want {
			t.Errorf("clientIP(%s, %q) = %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}
