package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/httpapi"
	"github.com/aluo96078/shinsou_sync_service/internal/instance"
	"github.com/aluo96078/shinsou_sync_service/internal/mail"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

// fakeMailer records mail. When gate is set, Send blocks until it is closed.
type fakeMailer struct {
	mu   sync.Mutex
	msgs []mail.Message
	gate chan struct{}
}

func (f *fakeMailer) Send(_ context.Context, m mail.Message) error {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	f.msgs = append(f.msgs, m)
	f.mu.Unlock()
	return nil
}

// withMail turns SMTP on in the configuration (the fake sender does the sending).
func withMail(c *config.Config) {
	c.SMTPHost, c.SMTPPort, c.SMTPTLS = "smtp.test", 587, config.SMTPStartTLS
	c.SMTPFrom = "Shinsou <no-reply@sync.test>"
}

func withVerification(policy string) func(*config.Config) {
	return func(c *config.Config) { c.EmailVerification = policy }
}

// outbox waits for queued mail to be handled and returns everything sent so far.
func (e *env) outbox() []mail.Message {
	e.t.Helper()
	e.server.WaitMail()
	e.mailer.mu.Lock()
	defer e.mailer.mu.Unlock()
	return append([]mail.Message(nil), e.mailer.msgs...)
}

var tokenRe = regexp.MustCompile(`#token=([A-Za-z0-9_-]+)`)

// tokenOf extracts the mail token from a message body.
func tokenOf(t *testing.T, m mail.Message) string {
	t.Helper()
	g := tokenRe.FindStringSubmatch(m.Body)
	if g == nil {
		t.Fatalf("no token link in mail:\n%s", m.Body)
	}
	return g[1]
}

// lastMail returns the newest mail sent to addr, failing when there is none.
func (e *env) lastMail(addr string) mail.Message {
	e.t.Helper()
	msgs := e.outbox()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].To == addr {
			return msgs[i]
		}
	}
	e.t.Fatalf("no mail to %s (outbox has %d)", addr, len(msgs))
	return mail.Message{}
}

func (e *env) mailCount(addr string) int {
	n := 0
	for _, m := range e.outbox() {
		if m.To == addr {
			n++
		}
	}
	return n
}

// registerWith creates an account with an email address.
func (e *env) registerWith(username, email string) session {
	e.t.Helper()
	return expect[session](e, 201, "POST", "/api/v1/auth/register", "", map[string]any{
		"username": username, "password": "correct horse", "email": email, "device": deviceInfo("phone"),
	})
}

// registerAdmin creates the first administrator through the setup code.
func (e *env) registerAdmin(username string) session {
	e.t.Helper()
	code, err := instance.ResetSetupCode(context.Background(), e.pool)
	if err != nil {
		e.t.Fatal(err)
	}
	return expect[session](e, 201, "POST", "/api/v1/auth/register", "", map[string]any{
		"username": username, "password": "correct horse", "setupCode": code, "device": deviceInfo("laptop"),
	})
}

// promote makes an existing user an administrator through the command-line path.
func (e *env) promote(username string) {
	e.t.Helper()
	if err := instance.SetRole(context.Background(), e.pool, username, "admin"); err != nil {
		e.t.Fatal(err)
	}
}

// rawResp is a response with its cookies decoded.
type rawResp struct {
	Status  int
	Body    []byte
	Header  http.Header
	Cookies []*http.Cookie
}

func (r rawResp) cookie(name string) *http.Cookie {
	for _, c := range r.Cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (r rawResp) errCode() string {
	var ae apiError
	_ = json.Unmarshal(r.Body, &ae)
	return ae.Error.Code
}

// rawCall performs a request with explicit cookies and headers (no automatic cookie handling).
func (e *env) rawCall(method, path string, body any, cookies []*http.Cookie, headers ...string) rawResp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return rawResp{Status: resp.StatusCode, Body: raw, Header: resp.Header, Cookies: resp.Cookies()}
}

func mustJSON(t *testing.T, raw []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

type (
	syncsvcWork   = syncsvc.Work
	syncsvcRecord = syncsvc.PushRecord
)

func httptestServer(t *testing.T, srv *httpapi.Server) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(ts.Close)
	return ts
}
