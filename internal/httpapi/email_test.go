package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/instance"
)

func mailEnv(t *testing.T, policy string, mutate ...func(*config.Config)) *env {
	t.Helper()
	fns := append([]func(*config.Config){withMail, withVerification(policy)}, mutate...)
	return newEnv(t, fns...)
}

func (e *env) verifyToken(token string) int {
	status, _ := e.call("POST", "/api/v1/auth/email/verify", "", map[string]any{"token": token})
	return status
}

func (e *env) ageTokens(sql string) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql); err != nil {
		e.t.Fatal(err)
	}
}

func TestRegisterSendsVerificationMailAndRequiresEmail(t *testing.T) {
	e := mailEnv(t, config.EmailRequired)
	info := expect[map[string]any](e, 200, "GET", "/.well-known/shinsou", "", nil)
	if info["features"].(map[string]any)["email"] != true || info["emailVerification"] != "required" {
		t.Fatalf("instance info: %v", info)
	}
	e.expectErr(422, "email_required", "POST", "/api/v1/auth/register", "", map[string]any{
		"username": "alice", "password": "correct horse", "device": deviceInfo("p")})
	e.expectErr(422, "email_required", "POST", "/api/v1/web/register", "", map[string]any{"username": "alice", "password": "correct horse"})

	a := e.registerWith("alice", "alice@example.com")
	m := e.lastMail("alice@example.com")
	tok := tokenOf(t, m)
	if !strings.Contains(m.Body, "https://sync.test/verify-email#token="+tok) || strings.Contains(m.Body, "?token=") {
		t.Fatalf("the link must carry the token in the fragment:\n%s", m.Body)
	}
	if !strings.Contains(m.Subject, "Verify") || !strings.Contains(m.Body, "Hello") || !strings.Contains(m.Body, "你好") {
		t.Fatalf("mail must be bilingual: %q\n%s", m.Subject, m.Body)
	}
	if strings.Contains(e.logs.String(), tok) {
		t.Fatal("tokens must never be logged")
	}
	me := expect[map[string]any](e, 200, "GET", "/api/v1/me", a.AccessToken, nil)
	if me["emailVerified"] != false {
		t.Fatalf("me = %v", me)
	}
}

func TestNoMailWhenPolicyDisabledOrNoAddress(t *testing.T) {
	e := mailEnv(t, config.EmailDisabled)
	e.registerWith("alice", "alice@example.com")
	e.register("bob")
	if n := len(e.outbox()); n != 0 {
		t.Fatalf("policy disabled: %d mails sent", n)
	}
	opt := mailEnv(t, config.EmailOptional)
	opt.register("bob") // no address, nothing to send
	opt.registerWith("carol", "carol@example.com")
	if n := len(opt.outbox()); n != 1 {
		t.Fatalf("policy optional: %d mails", n)
	}
	// Without SMTP nothing is queued even when the policy is not disabled.
	plain := newEnv(t, withVerification(config.EmailOptional))
	plain.registerWith("dave", "dave@example.com")
	if n := len(plain.outbox()); n != 0 {
		t.Fatalf("no smtp: %d mails", n)
	}
}

func TestVerificationGate(t *testing.T) {
	e := mailEnv(t, config.EmailRequired)
	u := e.registerWith("alice", "alice@example.com")
	tok := tokenOf(t, e.lastMail("alice@example.com"))

	gated := []struct{ method, path string }{
		{"POST", "/api/v1/sync/push"},
		{"GET", "/api/v1/sync/changes?since=0"},
		{"GET", "/api/v1/sync/stream"},
		{"GET", "/api/v1/library"},
		{"GET", "/api/v1/library/abc"},
		{"GET", "/api/v1/categories"},
		{"GET", "/api/v1/me/export"},
	}
	for _, g := range gated {
		e.expectErr(403, "email_unverified", g.method, g.path, u.AccessToken, nil)
		e.expectErr(403, "email_unverified", g.method, g.path, u.AccessToken, nil, "Accept", "text/event-stream")
	}
	// The WebSocket path of the stream is gated too, before the upgrade.
	e.expectErr(403, "email_unverified", "GET", "/api/v1/sync/stream", u.AccessToken, nil, "Connection", "Upgrade", "Upgrade", "websocket",
		"Sec-WebSocket-Version", "13", "Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	// Account management stays open.
	for _, path := range []string{"/api/v1/me", "/api/v1/me/usage", "/api/v1/devices"} {
		expect[map[string]any](e, 200, "GET", path, u.AccessToken, nil)
	}

	if e.verifyToken(tok) != 204 {
		t.Fatal("verify failed")
	}
	expect[map[string]any](e, 200, "GET", "/api/v1/library", u.AccessToken, nil)
	expect[map[string]any](e, 200, "GET", "/api/v1/sync/changes?since=0", u.AccessToken, nil)
	if e.verifyToken(tok) != 204 {
		t.Fatal("following the link twice is harmless")
	}
	me := expect[map[string]any](e, 200, "GET", "/api/v1/me", u.AccessToken, nil)
	if me["emailVerified"] != true {
		t.Fatalf("me = %v", me)
	}
}

func TestVerificationGateExemptions(t *testing.T) {
	e := mailEnv(t, config.EmailRequired)
	admin := e.registerAdminWithEmail("root", "root@example.com")
	expect[map[string]any](e, 200, "GET", "/api/v1/library", admin.AccessToken, nil) // administrators are exempt
	if n := e.mailCount("root@example.com"); n != 1 {
		t.Fatalf("the administrator gets a verification mail too: %d", n)
	}
	// The gate follows the policy immediately.
	u := e.registerWith("alice", "alice@example.com")
	e.expectErr(403, "email_unverified", "GET", "/api/v1/library", u.AccessToken, nil)
	expect[map[string]any](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"emailVerification": "optional"})
	expect[map[string]any](e, 200, "GET", "/api/v1/library", u.AccessToken, nil)
	expect[map[string]any](e, 200, "PATCH", "/api/v1/admin/settings", admin.AccessToken, map[string]any{"emailVerification": "required"})
	e.expectErr(403, "email_unverified", "GET", "/api/v1/library", u.AccessToken, nil)
	// A moderator is not exempt.
	if err := instance.SetRole(context.Background(), e.pool, "alice", "moderator"); err != nil {
		t.Fatal(err)
	}
	e.expectErr(403, "email_unverified", "GET", "/api/v1/library", u.AccessToken, nil)
}

func (e *env) registerAdminWithEmail(username, email string) session {
	e.t.Helper()
	code, err := instance.ResetSetupCode(context.Background(), e.pool)
	if err != nil {
		e.t.Fatal(err)
	}
	return expect[session](e, 201, "POST", "/api/v1/auth/register", "", map[string]any{
		"username": username, "password": "correct horse", "email": email, "setupCode": code, "device": deviceInfo("laptop")})
}

func TestVerificationGateOffByDefaultPolicies(t *testing.T) {
	for _, policy := range []string{config.EmailDisabled, config.EmailOptional} {
		e := mailEnv(t, policy)
		u := e.registerWith("alice", "alice@example.com")
		expect[map[string]any](e, 200, "GET", "/api/v1/library", u.AccessToken, nil)
	}
}

func TestInvalidVerificationTokens(t *testing.T) {
	e := mailEnv(t, config.EmailRequired)
	e.registerWith("alice", "alice@example.com")
	tok := tokenOf(t, e.lastMail("alice@example.com"))

	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/email/verify", "", map[string]any{"token": "nonsense"})
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/email/verify", "", map[string]any{"token": ""})

	// expired
	e.ageTokens(`update email_tokens set expires_at = now() - interval '1 minute'`)
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/email/verify", "", map[string]any{"token": tok})
	e.ageTokens(`update email_tokens set expires_at = now() + interval '1 hour'`)
	if e.verifyToken(tok) != 204 {
		t.Fatal("a valid token verifies")
	}

	// used, after the address changed again
	b := e.registerWith("bob", "bob@example.com")
	btok := tokenOf(t, e.lastMail("bob@example.com"))
	if e.verifyToken(btok) != 204 {
		t.Fatal("verify bob")
	}
	e.ageTokens(`update email_tokens set created_at = now() - interval '2 minutes'`)
	expect[map[string]any](e, 200, "PUT", "/api/v1/me/email", b.AccessToken, map[string]any{"email": "bob2@example.com", "currentPassword": "correct horse"})
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/email/verify", "", map[string]any{"token": btok})

	// a token minted for the old address does not verify the new one
	c := e.registerWith("carol", "carol@example.com")
	ctok := tokenOf(t, e.lastMail("carol@example.com"))
	e.ageTokens(`update users set email = 'carol-new@example.com' where username = 'carol'`)
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/email/verify", "", map[string]any{"token": ctok})
	me := expect[map[string]any](e, 200, "GET", "/api/v1/me", c.AccessToken, nil)
	if me["emailVerified"] != false {
		t.Fatalf("me = %v", me)
	}

	// a reset token is not a verification token
	e.verifyViaForgot(t)
}

func (e *env) verifyViaForgot(t *testing.T) {
	t.Helper()
	d := e.registerWith("dave", "dave@example.com")
	if e.verifyToken(tokenOf(t, e.lastMail("dave@example.com"))) != 204 {
		t.Fatal("verify dave")
	}
	_ = d
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "dave@example.com"})
	reset := tokenOf(t, e.lastMail("dave@example.com"))
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/email/verify", "", map[string]any{"token": reset})
}

func TestResendVerification(t *testing.T) {
	e := mailEnv(t, config.EmailOptional)
	u := e.registerWith("alice", "alice@example.com")
	// The registration mail counts: one mail per minute per account and purpose.
	ae := e.expectErr(429, "rate_limited", "POST", "/api/v1/me/email/verification", u.AccessToken, nil)
	if ae.Error.RetryAfterSeconds < 1 || ae.Error.RetryAfterSeconds > 60 {
		t.Fatalf("retryAfterSeconds = %d", ae.Error.RetryAfterSeconds)
	}
	resp, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/me/email/verification", nil)
	resp.Header.Set("Authorization", "Bearer "+u.AccessToken)
	r, err := http.DefaultClient.Do(resp)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("429 needs a Retry-After header")
	}
	if e.mailCount("alice@example.com") != 1 {
		t.Fatal("no mail while cooling down")
	}
	e.ageTokens(`update email_tokens set created_at = now() - interval '2 minutes'`)
	expect[map[string]any](e, 204, "POST", "/api/v1/me/email/verification", u.AccessToken, nil)
	if e.mailCount("alice@example.com") != 2 {
		t.Fatalf("resend must send a mail, have %d", e.mailCount("alice@example.com"))
	}
	if e.verifyToken(tokenOf(t, e.lastMail("alice@example.com"))) != 204 {
		t.Fatal("the new token verifies")
	}
	e.expectErr(409, "conflict", "POST", "/api/v1/me/email/verification", u.AccessToken, nil)

	noMail := e.register("bob")
	e.expectErr(422, "email_required", "POST", "/api/v1/me/email/verification", noMail.AccessToken, nil)

	plain := newEnv(t, withVerification(config.EmailOptional))
	p := plain.registerWith("carol", "carol@example.com")
	plain.expectErr(409, "email_unavailable", "POST", "/api/v1/me/email/verification", p.AccessToken, nil)
}

func TestChangeEmail(t *testing.T) {
	e := mailEnv(t, config.EmailRequired)
	u := e.registerWith("alice", "alice@example.com")
	e.registerWith("bob", "bob@example.com")
	oldTok := tokenOf(t, e.lastMail("alice@example.com"))
	if e.verifyToken(oldTok) != 204 {
		t.Fatal("verify")
	}
	e.ageTokens(`update email_tokens set created_at = now() - interval '2 minutes'`)
	put := func(email any, pw string) (int, []byte) {
		return e.call("PUT", "/api/v1/me/email", u.AccessToken, map[string]any{"email": email, "currentPassword": pw})
	}
	if st, _ := put("new@example.com", "wrong password"); st != 401 {
		t.Fatalf("wrong password: %d", st)
	}
	if st, _ := put("not-an-email", "correct horse"); st != 422 {
		t.Fatalf("bad address: %d", st)
	}
	if st, _ := put("BOB@example.com", "correct horse"); st != 409 {
		t.Fatalf("taken: %d", st)
	}
	if st, _ := put(nil, "correct horse"); st != 422 {
		t.Fatalf("removal under required: %d", st)
	}
	before := len(e.outbox())
	// No change: nothing happens.
	got := expect[map[string]any](e, 200, "PUT", "/api/v1/me/email", u.AccessToken, map[string]any{"email": "alice@example.com", "currentPassword": "correct horse"})
	if got["emailVerified"] != true || len(e.outbox()) != before {
		t.Fatalf("unchanged address must stay verified and send nothing: %v", got)
	}
	// A reset link mailed to the old address dies with the change.
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "alice@example.com"})
	resetTok := tokenOf(t, e.lastMail("alice@example.com"))
	before = len(e.outbox())
	// Change: back to unverified, mail to the new address only, old tokens dead.
	got = expect[map[string]any](e, 200, "PUT", "/api/v1/me/email", u.AccessToken, map[string]any{"email": "alice2@example.com", "currentPassword": "correct horse"})
	if got["email"] != "alice2@example.com" || got["emailVerified"] != false {
		t.Fatalf("after change: %v", got)
	}
	m := e.lastMail("alice2@example.com")
	if len(e.outbox()) != before+1 {
		t.Fatal("exactly one mail for the change")
	}
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/email/verify", "", map[string]any{"token": oldTok})
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": resetTok, "newPassword": "another horse 1"})
	// A change sends mail, so the one-mail-a-minute rule applies and nothing changes meanwhile.
	ae := e.expectErr(429, "rate_limited", "PUT", "/api/v1/me/email", u.AccessToken, map[string]any{"email": "alice3@example.com", "currentPassword": "correct horse"})
	if ae.Error.RetryAfterSeconds < 1 || ae.Error.RetryAfterSeconds > 60 || len(e.outbox()) != before+1 {
		t.Fatalf("cooldown: retryAfterSeconds = %d, mails = %d", ae.Error.RetryAfterSeconds, len(e.outbox())-before)
	}
	if me := expect[map[string]any](e, 200, "GET", "/api/v1/me", u.AccessToken, nil); me["email"] != "alice2@example.com" {
		t.Fatalf("a refused change must leave the address alone: %v", me)
	}
	if e.verifyToken(tokenOf(t, m)) != 204 {
		t.Fatal("verify the new address")
	}
	// Removal is allowed when verification is not required.
	expect[map[string]any](e, 200, "PATCH", "/api/v1/admin/settings", e.adminToken(), map[string]any{"emailVerification": "optional"})
	got = expect[map[string]any](e, 200, "PUT", "/api/v1/me/email", u.AccessToken, map[string]any{"email": nil, "currentPassword": "correct horse"})
	if got["email"] != nil || got["emailVerified"] != false {
		t.Fatalf("removal: %v", got)
	}
}

// adminToken creates (once per env) an administrator and returns its access token.
func (e *env) adminToken() string {
	e.t.Helper()
	if e.admin == "" {
		e.admin = e.registerAdminWithEmail("rootadmin", "rootadmin@example.com").AccessToken
	}
	return e.admin
}

func TestForgotPassword(t *testing.T) {
	e := mailEnv(t, config.EmailRequired)
	u := e.registerWith("alice", "alice@example.com")
	e.registerWith("bob", "bob@example.com")
	if e.verifyToken(tokenOf(t, e.lastMail("alice@example.com"))) != 204 {
		t.Fatal("verify alice")
	}
	base := len(e.outbox())
	forgot := func(email string) int {
		st, _ := e.call("POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": email})
		return st
	}
	// Same answer whether or not the address exists; mail only for verified, active accounts.
	for _, addr := range []string{"nobody@example.com", "bob@example.com"} {
		if forgot(addr) != 204 {
			t.Fatalf("%s: forgot must answer 204", addr)
		}
	}
	if len(e.outbox()) != base {
		t.Fatal("unknown and unverified addresses (policy required) get no mail")
	}
	e.expectErr(422, "validation_failed", "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "nope"})

	if forgot("ALICE@example.com") != 204 {
		t.Fatal("forgot alice")
	}
	m := e.lastMail("alice@example.com")
	rt := tokenOf(t, m)
	if !strings.Contains(m.Body, "https://sync.test/reset-password#token="+rt) || len(e.outbox()) != base+1 {
		t.Fatalf("reset mail:\n%s", m.Body)
	}
	// Cooldown: silently skipped, same status.
	if forgot("alice@example.com") != 204 || len(e.outbox()) != base+1 {
		t.Fatal("a second request inside a minute sends nothing but still answers 204")
	}
	e.ageTokens(`update email_tokens set created_at = now() - interval '2 minutes'`)
	if forgot("alice@example.com") != 204 || len(e.outbox()) != base+2 {
		t.Fatal("after the cooldown a new mail is sent")
	}

	// Suspended accounts get nothing.
	e.ageTokens(`update users set status = 'suspended' where username = 'alice'`)
	e.ageTokens(`update email_tokens set created_at = now() - interval '2 minutes'`)
	if forgot("alice@example.com") != 204 || len(e.outbox()) != base+2 {
		t.Fatal("suspended accounts get no reset mail")
	}
	_ = u
	if strings.Contains(e.logs.String(), rt) {
		t.Fatal("tokens must never be logged")
	}
}

func TestForgotPasswordWhenVerificationDisabledReachesUnverified(t *testing.T) {
	e := mailEnv(t, config.EmailDisabled)
	e.registerWith("bob", "bob@example.com")
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "bob@example.com"})
	if e.mailCount("bob@example.com") != 1 {
		t.Fatal("with verification disabled an unverified address still gets the reset mail")
	}
}

func TestForgotPasswordWithoutSMTP(t *testing.T) {
	e := newEnv(t)
	e.expectErr(409, "email_unavailable", "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "x@example.com"})
}

func TestForgotPasswordDoesNotWaitForSMTP(t *testing.T) {
	e := mailEnv(t, config.EmailDisabled)
	e.registerWith("bob", "bob@example.com")
	e.mailer.gate = make(chan struct{}) // every Send blocks until released
	for _, addr := range []string{"bob@example.com", "nobody@example.com"} {
		expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": addr})
	}
	close(e.mailer.gate)
	if e.mailCount("bob@example.com") != 1 {
		t.Fatal("the mail is delivered in the background")
	}
}

func TestResetPassword(t *testing.T) {
	e := mailEnv(t, config.EmailRequired)
	u := e.registerWith("alice", "alice@example.com")
	other := e.login("alice", "correct horse", "tablet")
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/email/verify", "", map[string]any{"token": tokenOf(t, e.lastMail("alice@example.com"))})
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "alice@example.com"})
	tok := tokenOf(t, e.lastMail("alice@example.com"))

	e.expectErr(422, "validation_failed", "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": tok, "newPassword": "short"})
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": "nonsense", "newPassword": "brand new pass"})
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": tok, "newPassword": "brand new pass"})

	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": "correct horse", "device": deviceInfo("x")})
	expect[session](e, 200, "POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": "brand new pass", "device": deviceInfo("x")})
	// every device was signed out
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", u.AccessToken, nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", other.AccessToken, nil)
	e.expectErr(401, "unauthorized", "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": other.RefreshToken})
	// single use
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": tok, "newPassword": "another pass 1"})
}

func TestResetPasswordTokenRules(t *testing.T) {
	e := mailEnv(t, config.EmailRequired)
	e.registerWith("alice", "alice@example.com")
	verify := tokenOf(t, e.lastMail("alice@example.com"))
	e.ageTokens(`update users set email_verified = true where username = 'alice'`)
	forgot := func() string {
		e.ageTokens(`update email_tokens set created_at = now() - interval '2 minutes'`)
		expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "alice@example.com"})
		return tokenOf(t, e.lastMail("alice@example.com"))
	}
	// a verification token cannot reset a password
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": verify, "newPassword": "brand new pass"})

	// expired
	t1 := forgot()
	e.ageTokens(`update email_tokens set expires_at = now() - interval '1 second' where purpose = 'reset_password'`)
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": t1, "newPassword": "brand new pass"})

	// using one token voids the other outstanding reset tokens
	e.ageTokens(`delete from email_tokens where purpose = 'reset_password'`)
	a := forgot()
	e.ageTokens(`update email_tokens set created_at = now() - interval '2 minutes'`)
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "alice@example.com"})
	b := tokenOf(t, e.lastMail("alice@example.com"))
	if a == b {
		t.Fatal("tokens must differ")
	}
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": a, "newPassword": "brand new pass"})
	e.expectErr(400, "invalid_token", "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": b, "newPassword": "brand new pass"})
}

func TestResetPasswordVerifiesMatchingAddress(t *testing.T) {
	e := mailEnv(t, config.EmailOptional)
	u := e.registerWith("alice", "alice@example.com") // unverified; optional policy still allows reset mails only when verified... disabled? no
	_ = u
	// Under "optional" an unverified address gets no reset mail, so verify by policy switch.
	e.ageTokens(`update users set email_verified = false where username = 'alice'`)
	admin := e.adminToken()
	expect[map[string]any](e, 200, "PATCH", "/api/v1/admin/settings", admin, map[string]any{"emailVerification": "disabled"})
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "alice@example.com"})
	tok := tokenOf(t, e.lastMail("alice@example.com"))
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/reset", "", map[string]any{"token": tok, "newPassword": "brand new pass"})
	s := expect[session](e, 200, "POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": "brand new pass", "device": deviceInfo("x")})
	me := expect[map[string]any](e, 200, "GET", "/api/v1/me", s.AccessToken, nil)
	if me["emailVerified"] != true {
		t.Fatalf("resetting through the mailed link proves the address: %v", me)
	}
}

func TestEmailTokenPurge(t *testing.T) {
	e := mailEnv(t, config.EmailOptional)
	e.registerWith("alice", "alice@example.com")
	e.registerWith("bob", "bob@example.com")
	e.ageTokens(`update email_tokens set expires_at = now() - interval '8 days' where user_id = (select id from users where username = 'alice')`)
	e.ageTokens(`update email_tokens set expires_at = now() - interval '1 day' where user_id = (select id from users where username = 'bob')`)
	r := newRunnerWithTokenRetention(e)
	n, err := r.GCEmailTokens(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("purged %d, err %v", n, err)
	}
	var left int
	if err := e.pool.QueryRow(context.Background(), `select count(*) from email_tokens`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("left %d, err %v", left, err)
	}
}

// Mail flows that touch the same account at once must wait for each other: none may fail with a
// server error (a deadlock would surface as one).
func TestConcurrentMailFlowsDoNotFail(t *testing.T) {
	e := mailEnv(t, config.EmailRequired)
	u := e.registerWith("alice", "alice@example.com")
	verify := tokenOf(t, e.lastMail("alice@example.com"))
	expect[map[string]any](e, 204, "POST", "/api/v1/auth/password/forgot", "", map[string]any{"email": "alice@example.com"})
	reset := tokenOf(t, e.lastMail("alice@example.com"))

	calls := []func() int{
		func() int {
			s, _ := e.call("PUT", "/api/v1/me/email", u.AccessToken, map[string]any{"email": "alice2@example.com", "currentPassword": "correct horse"})
			return s
		},
		func() int { return e.verifyToken(verify) },
		func() int { return e.verifyToken(verify) },
		func() int {
			s, _ := e.call("POST", "/api/v1/auth/password/reset", "", map[string]any{"token": reset, "newPassword": "brand new pass"})
			return s
		},
		func() int {
			s, _ := e.call("POST", "/api/v1/auth/password/reset", "", map[string]any{"token": reset, "newPassword": "another pass 1"})
			return s
		},
	}
	statuses := make([]int, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = c()
		}()
	}
	wg.Wait()
	for i, s := range statuses {
		if s >= 500 {
			t.Errorf("call %d answered %d", i, s)
		}
	}
	if statuses[3] == 204 && statuses[4] == 204 {
		t.Error("the reset token was used twice")
	}
}
