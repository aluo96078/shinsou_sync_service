package mail

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
)

// fakeSMTP is a minimal plaintext SMTP server that records what it receives.
type fakeSMTP struct {
	ln   net.Listener
	mu   sync.Mutex
	got  []string // DATA payloads
	from []string
	rcpt []string
	auth []string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			w("250-fake")
			w("250 AUTH PLAIN")
		case strings.HasPrefix(cmd, "AUTH"):
			f.mu.Lock()
			f.auth = append(f.auth, line)
			f.mu.Unlock()
			w("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			f.mu.Lock()
			f.from = append(f.from, line)
			f.mu.Unlock()
			w("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			f.mu.Lock()
			f.rcpt = append(f.rcpt, line)
			f.mu.Unlock()
			w("250 ok")
		case cmd == "DATA":
			w("354 go")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			f.mu.Lock()
			f.got = append(f.got, sb.String())
			f.mu.Unlock()
			w("250 queued")
		case cmd == "QUIT":
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func TestSMTPSenderDelivers(t *testing.T) {
	f := newFakeSMTP(t)
	s, err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: f.port(), From: "Shinsou <no-reply@example.com>",
		TLS: config.SMTPNone, Username: "user", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Send(context.Background(), Message{To: "alice@example.com", Subject: "驗證 / Verify", Body: "line one\nhttps://x/verify#token=abc\n"})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) != 1 || len(f.auth) != 1 || !strings.Contains(f.from[0], "no-reply@example.com") || !strings.Contains(f.rcpt[0], "alice@example.com") {
		t.Fatalf("unexpected conversation: %+v", f)
	}
	msg := f.got[0]
	for _, want := range []string{"From: ", "To: alice@example.com", "Subject: =?utf-8?q?", "Content-Type: text/plain; charset=utf-8", "Content-Transfer-Encoding: base64", "Message-Id: ", "Date: "} {
		if !strings.Contains(strings.ToLower(msg), strings.ToLower(want)) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestSMTPSenderRejectsHeaderInjection(t *testing.T) {
	f := newFakeSMTP(t)
	s, err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: f.port(), From: "no-reply@example.com", TLS: config.SMTPNone})
	if err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]Message{
		"newline in recipient": {To: "a@example.com\r\nBcc: evil@example.com", Subject: "s", Body: "b"},
		"newline in subject":   {To: "a@example.com", Subject: "s\r\nBcc: evil@example.com", Body: "b"},
		"display name":         {To: "Alice <a@example.com>", Subject: "s", Body: "b"},
		"not an address":       {To: "nope", Subject: "s", Body: "b"},
	} {
		if err := s.Send(context.Background(), m); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) != 0 || len(f.rcpt) != 0 {
		t.Fatalf("nothing may reach the server: %+v", f)
	}
	if _, err := NewSMTPSender(SMTPConfig{Host: "h", Port: 25, From: "a@example.com\r\nBcc: x@example.com"}); err == nil {
		t.Fatal("a From with a line break must be rejected")
	}
}

func TestSMTPSenderTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // accepts and then says nothing
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(2 * time.Second)
		}
	}()
	s, err := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, From: "a@example.com",
		TLS: config.SMTPNone, IOTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := s.Send(context.Background(), Message{To: "b@example.com", Subject: "s", Body: "b"}); err == nil {
		t.Fatal("expected a timeout")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

type recorder struct {
	mu   sync.Mutex
	msgs []Message
	gate chan struct{}
}

func (r *recorder) Send(_ context.Context, m Message) error {
	if r.gate != nil {
		<-r.gate
	}
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	r.mu.Unlock()
	return nil
}

func TestQueueDeliversAndDropsWhenFull(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	rec := &recorder{gate: make(chan struct{})}
	q := NewQueue(rec, true, 2, 1, log)
	defer q.Close()
	accepted := 0
	for i := range 10 {
		if q.Enqueue(Message{To: "a@example.com", Subject: strconv.Itoa(i)}) {
			accepted++
		}
	}
	// One message is held by the blocked worker and two wait in the buffer.
	if accepted < 2 || accepted > 3 {
		t.Fatalf("accepted %d messages", accepted)
	}
	close(rec.gate)
	q.Wait()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.msgs) != accepted {
		t.Fatalf("delivered %d, accepted %d", len(rec.msgs), accepted)
	}
}

func TestDisabledQueueAcceptsNothing(t *testing.T) {
	q := NewQueue(&recorder{}, false, 4, 1, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	defer q.Close()
	if q.Enabled() || q.Enqueue(Message{To: "a@example.com"}) {
		t.Fatal("a disabled queue must not accept mail")
	}
	q.Wait()
}

func TestTemplatesPutTokenInFragment(t *testing.T) {
	for _, m := range []Message{
		VerifyEmail("Inst", "https://sync.example.com", "a@example.com", "TOK"),
		ResetPassword("Inst", "https://sync.example.com", "a@example.com", "TOK"),
	} {
		if !strings.Contains(m.Body, "#token=TOK") || strings.Contains(m.Body, "?token=") {
			t.Errorf("token must be in the fragment:\n%s", m.Body)
		}
	}
}
