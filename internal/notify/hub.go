// Package notify fans sequence-number notifications out to connected streams. It is in-memory,
// so it serves a single server process; multiple instances would need PG LISTEN/NOTIFY.
package notify

import (
	"sync"

	"github.com/google/uuid"
)

// Hub tracks subscribers per user.
type Hub struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[*Subscription]struct{}
}

// Subscription receives the latest seq. Intermediate values are coalesced.
type Subscription struct {
	C    <-chan int64
	c    chan int64
	hub  *Hub
	user uuid.UUID
}

// NewHub creates an empty hub.
func NewHub() *Hub { return &Hub{subs: map[uuid.UUID]map[*Subscription]struct{}{}} }

// Subscribe registers a listener for user. Call Close when done.
func (h *Hub) Subscribe(user uuid.UUID) *Subscription {
	c := make(chan int64, 1)
	s := &Subscription{C: c, c: c, hub: h, user: user}
	h.mu.Lock()
	if h.subs[user] == nil {
		h.subs[user] = map[*Subscription]struct{}{}
	}
	h.subs[user][s] = struct{}{}
	h.mu.Unlock()
	return s
}

// Close unregisters the subscription.
func (s *Subscription) Close() {
	h := s.hub
	h.mu.Lock()
	delete(h.subs[s.user], s)
	if len(h.subs[s.user]) == 0 {
		delete(h.subs, s.user)
	}
	h.mu.Unlock()
}

// Publish tells every subscriber of user that seq is now the latest. A slow subscriber only ever
// holds the newest value.
func (h *Hub) Publish(user uuid.UUID, seq int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs[user] {
		select {
		case <-s.c:
		default:
		}
		select {
		case s.c <- seq:
		default:
		}
	}
}

// Count returns the number of subscribers for user (for tests and metrics).
func (h *Hub) Count(user uuid.UUID) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[user])
}
