package notify

import (
	"testing"

	"github.com/google/uuid"
)

func TestHubCoalescesAndIsolatesUsers(t *testing.T) {
	h := NewHub()
	a, b := uuid.New(), uuid.New()
	sa := h.Subscribe(a)
	sb := h.Subscribe(b)
	h.Publish(a, 1)
	h.Publish(a, 2)
	h.Publish(a, 3)
	if got := <-sa.C; got != 3 {
		t.Fatalf("want newest value 3, got %d", got)
	}
	select {
	case v := <-sb.C:
		t.Fatalf("user b must not be notified, got %d", v)
	default:
	}
	sa.Close()
	if h.Count(a) != 0 || h.Count(b) != 1 {
		t.Fatalf("counts wrong: %d %d", h.Count(a), h.Count(b))
	}
	h.Publish(a, 4) // no subscribers: must not panic or block
}
