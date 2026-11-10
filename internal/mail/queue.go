package mail

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Queue delivers messages in the background through a bounded buffer. When the buffer is full
// the message is dropped and logged: a mail server outage must never stall requests.
type Queue struct {
	sender  Sender
	enabled bool
	log     *slog.Logger
	timeout time.Duration

	ch     chan Message
	wg     sync.WaitGroup
	cancel context.CancelFunc
	ctx    context.Context

	mu      sync.Mutex
	cond    *sync.Cond
	pending int
	closed  bool
}

// QueueSize is the number of messages that can wait.
const QueueSize = 256

// NewQueue starts workers goroutines that deliver through sender. A queue that is not enabled
// (no SMTP configured) accepts nothing.
func NewQueue(sender Sender, enabled bool, size, workers int, log *slog.Logger) *Queue {
	if size < 1 {
		size = QueueSize
	}
	if workers < 1 {
		workers = 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := &Queue{sender: sender, enabled: enabled && sender != nil, log: log, timeout: 45 * time.Second,
		ch: make(chan Message, size), ctx: ctx, cancel: cancel}
	q.cond = sync.NewCond(&q.mu)
	if q.enabled {
		for range workers {
			q.wg.Add(1)
			go q.work()
		}
	}
	return q
}

// Enabled reports whether mail can be sent at all.
func (q *Queue) Enabled() bool { return q != nil && q.enabled }

// Enqueue schedules m and returns immediately. It reports whether the message was accepted.
func (q *Queue) Enqueue(m Message) bool {
	if !q.Enabled() {
		return false
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	select {
	case q.ch <- m:
		q.pending++
		q.mu.Unlock()
		return true
	default:
		q.mu.Unlock()
		q.log.Error("mail queue is full; message dropped")
		return false
	}
}

func (q *Queue) work() {
	defer q.wg.Done()
	for m := range q.ch {
		ctx, cancel := context.WithTimeout(q.ctx, q.timeout)
		if err := q.sender.Send(ctx, m); err != nil {
			q.log.Error("mail send failed", "err", err)
		}
		cancel()
		q.mu.Lock()
		q.pending--
		q.cond.Broadcast()
		q.mu.Unlock()
	}
}

// Wait blocks until every accepted message has been handled (sent or failed). Tests use it to
// observe the outbox deterministically.
func (q *Queue) Wait() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.pending > 0 {
		q.cond.Wait()
	}
}

// Close stops accepting messages and gives the workers a few seconds to finish.
func (q *Queue) Close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	close(q.ch)
	q.mu.Unlock()
	done := make(chan struct{})
	go func() { q.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		q.cancel()
		<-done
	}
	q.cancel()
}
