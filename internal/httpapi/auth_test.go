package httpapi_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/instance"
)

func TestRegisterLoginAndDevices(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice")
	if a.User.Username != "alice" || a.User.Role != "user" || a.User.Handle != "@alice@sync.test" {
		t.Fatalf("unexpected user: %+v", a.User)
	}
	if a.Device.SyncDeviceID == "" || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(a.Device.SyncDeviceID) {
		t.Fatalf("bad sync device id %q", a.Device.SyncDeviceID)
	}
	me := expect[map[string]any](e, 200, "GET", "/api/v1/me", a.AccessToken, nil)
	if me["username"] != "alice" {
		t.Fatalf("me = %v", me)
	}

	b := e.login("ALICE", "correct horse", "tablet") // usernames are case-insensitive at login
	if b.Device.SyncDeviceID == a.Device.SyncDeviceID {
		t.Fatal("each login must get its own sync device id")
	}
	list := expect[struct {
		Devices []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Current bool   `json:"current"`
		} `json:"devices"`
	}](e, 200, "GET", "/api/v1/devices", b.AccessToken, nil)
	if len(list.Devices) != 2 {
		t.Fatalf("want 2 devices, got %d", len(list.Devices))
	}
	for _, d := range list.Devices {
		if d.Current != (d.ID == b.Device.ID) {
			t.Fatalf("current flag wrong: %+v", d)
		}
	}

	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/auth/login", "", map[string]any{
		"username": "alice", "password": "wrong password", "device": deviceInfo("x")})
	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/auth/login", "", map[string]any{
		"username": "nobody", "password": "whatever1", "device": deviceInfo("x")})
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", "", nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", "garbage", nil)
}

func TestRegisterValidation(t *testing.T) {
	e := newEnv(t)
	e.register("alice")
	post := func(body map[string]any) map[string]any {
		body["device"] = deviceInfo("p")
		return body
	}
	e.expectErr(409, "username_taken", "POST", "/api/v1/auth/register", "", post(map[string]any{"username": "alice", "password": "long enough"}))
	for _, u := range []string{"ab", "Alice", "has space", "way_too_long_username_that_exceeds_32_chars", "dash-ed"} {
		e.expectErr(422, "validation_failed", "POST", "/api/v1/auth/register", "", post(map[string]any{"username": u, "password": "long enough"}))
	}
	e.expectErr(422, "validation_failed", "POST", "/api/v1/auth/register", "", post(map[string]any{"username": "bob", "password": "short"}))
	e.expectErr(422, "validation_failed", "POST", "/api/v1/auth/register", "", post(map[string]any{"username": "bob", "password": "long enough", "email": "not-an-email"}))
	e.expectErr(422, "validation_failed", "POST", "/api/v1/auth/register", "", map[string]any{"username": "bob", "password": "long enough"})
	e.expectErr(400, "bad_request", "POST", "/api/v1/auth/register", "", []byte("{nope"))

	e.expect201("bob1", "bob@example.com")
	e.expectErr(409, "email_taken", "POST", "/api/v1/auth/register", "", post(map[string]any{"username": "bob2", "password": "long enough", "email": "BOB@example.com"}))
}

func (e *env) expect201(username, email string) {
	e.t.Helper()
	expect[session](e, 201, "POST", "/api/v1/auth/register", "", map[string]any{
		"username": username, "password": "long enough", "email": email, "device": deviceInfo("p")})
}

func TestRegistrationPolicies(t *testing.T) {
	reg := func(e *env, user, invite string) (int, string) {
		body := map[string]any{"username": user, "password": "long enough", "device": deviceInfo("p")}
		if invite != "" {
			body["inviteCode"] = invite
		}
		status, raw := e.call("POST", "/api/v1/auth/register", "", body)
		var ae apiError
		_ = json.Unmarshal(raw, &ae)
		return status, ae.Error.Code
	}

	t.Run("closed", func(t *testing.T) {
		e := newEnv(t, func(c *config.Config) { c.Registration = config.RegistrationClosed })
		if s, c := reg(e, "alice", ""); s != 403 || c != "registration_closed" {
			t.Fatalf("got %d %s", s, c)
		}
		info := expect[map[string]any](e, 200, "GET", "/.well-known/shinsou", "", nil)
		if info["registration"] != "closed" {
			t.Fatalf("well-known = %v", info)
		}
	})

	t.Run("invite", func(t *testing.T) {
		e := newEnv(t, func(c *config.Config) { c.Registration = config.RegistrationInvite })
		if s, c := reg(e, "alice", ""); s != 403 || c != "invite_required" {
			t.Fatalf("got %d %s", s, c)
		}
		if s, c := reg(e, "alice", "AAAA-BBBB-CCCC-DDDD"); s != 403 || c != "invite_required" {
			t.Fatalf("bogus invite: got %d %s", s, c)
		}
		code, err := instance.CreateInvite(context.Background(), e.pool, 2, 0)
		if err != nil {
			t.Fatal(err)
		}
		if s, c := reg(e, "alice", code); s != 201 {
			t.Fatalf("valid invite: got %d %s", s, c)
		}
		// Case and dashes do not matter when typing a code.
		if s, c := reg(e, "bob", regexp.MustCompile(`-`).ReplaceAllString(code, "")); s != 201 {
			t.Fatalf("second use: got %d %s", s, c)
		}
		if s, c := reg(e, "carol", code); s != 403 || c != "invite_required" {
			t.Fatalf("used up invite: got %d %s", s, c)
		}
		// A failed registration must not burn an invite use.
		code2, _ := instance.CreateInvite(context.Background(), e.pool, 1, 0)
		if s, c := reg(e, "alice", code2); s != 409 || c != "username_taken" {
			t.Fatalf("duplicate: got %d %s", s, c)
		}
		if s, _ := reg(e, "dave", code2); s != 201 {
			t.Fatalf("invite should still be usable after a failed attempt, got %d", s)
		}
	})
}

func TestSetupCodeCreatesAdmin(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Registration = config.RegistrationClosed })
	ctx := context.Background()
	log := slogTo(e.logs)
	if err := instance.EnsureSetupCode(ctx, e.pool, log); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`"setup_code":"([A-Z2-7-]+)"`).FindStringSubmatch(e.logs.String())
	if m == nil {
		t.Fatalf("setup code was not logged once: %s", e.logs.String())
	}
	code := m[1]

	// A second start must not create or log another code.
	if err := instance.EnsureSetupCode(ctx, e.pool, log); err != nil || strings.Count(e.logs.String(), "setup_code") != 1 {
		t.Fatalf("setup code regenerated on restart: %v", err)
	}
	var stored string
	if err := e.pool.QueryRow(ctx, `select value::text from instance_settings where key = 'setup_code_hash'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(code).MatchString(stored) {
		t.Fatal("the plain setup code must not be stored")
	}

	reg := func(user, setup string) (int, session) {
		status, raw := e.call("POST", "/api/v1/auth/register", "", map[string]any{
			"username": user, "password": "long enough", "setupCode": setup, "device": deviceInfo("p")})
		var s session
		_ = json.Unmarshal(raw, &s)
		return status, s
	}
	if st, _ := reg("root", "AAAA-AAAA-AAAA-AAAA"); st != 403 {
		t.Fatalf("wrong code: %d", st)
	}
	st, s := reg("root", code)
	if st != 201 || s.User.Role != "admin" {
		t.Fatalf("setup registration: %d %+v", st, s.User)
	}
	if st, _ := reg("root2", code); st != 403 {
		t.Fatalf("setup code must be single use, got %d", st)
	}
	// Closed registration still applies to everybody else.
	e.expectErr(403, "registration_closed", "POST", "/api/v1/auth/register", "", map[string]any{
		"username": "carol", "password": "long enough", "device": deviceInfo("p")})

	if _, err := instance.ResetSetupCode(ctx, e.pool); err != instance.ErrAdminExists {
		t.Fatalf("reset with an admin present should fail, got %v", err)
	}
	if err := instance.EnsureSetupCode(ctx, e.pool, log); err != nil || strings.Count(e.logs.String(), "setup_code") != 1 {
		t.Fatal("no new code once an admin exists")
	}
}

func TestResetSetupCodeAndSetRole(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Registration = config.RegistrationClosed })
	ctx := context.Background()
	code, err := instance.ResetSetupCode(ctx, e.pool)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := instance.ResetSetupCode(ctx, e.pool)
	st, _ := e.call("POST", "/api/v1/auth/register", "", map[string]any{
		"username": "root", "password": "long enough", "setupCode": code, "device": deviceInfo("p")})
	if st != 403 {
		t.Fatalf("old code must stop working, got %d", st)
	}
	e.expectErr(201, "", "POST", "/api/v1/auth/register", "", map[string]any{
		"username": "root", "password": "long enough", "setupCode": other, "device": deviceInfo("p")})
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	other := e.login("alice", "correct horse", "tablet")

	type tokens struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	}
	t2 := expect[tokens](e, 200, "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": s.RefreshToken})
	if t2.RefreshToken == s.RefreshToken || t2.AccessToken == "" {
		t.Fatal("refresh must rotate the token")
	}
	expect[map[string]any](e, 200, "GET", "/api/v1/me", t2.AccessToken, nil)
	t3 := expect[tokens](e, 200, "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": t2.RefreshToken})

	// Replaying the first (already rotated) token kills the whole device session.
	e.expectErr(401, "refresh_token_reused", "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": s.RefreshToken})
	e.expectErr(401, "unauthorized", "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": t3.RefreshToken})
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", "", nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", t3.AccessToken, nil)

	// The other device is untouched.
	expect[map[string]any](e, 200, "GET", "/api/v1/me", other.AccessToken, nil)
	e.expectErr(401, "unauthorized", "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": "shr_unknown"})
}

func TestPasswordChangeRevokesOtherDevices(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice")
	b := e.login("alice", "correct horse", "tablet")

	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/me/password", a.AccessToken,
		map[string]any{"currentPassword": "nope nope", "newPassword": "brand new pass"})
	e.expectErr(422, "validation_failed", "POST", "/api/v1/me/password", a.AccessToken,
		map[string]any{"currentPassword": "correct horse", "newPassword": "short"})
	expect[any](e, 204, "POST", "/api/v1/me/password", a.AccessToken,
		map[string]any{"currentPassword": "correct horse", "newPassword": "brand new pass"})

	expect[map[string]any](e, 200, "GET", "/api/v1/me", a.AccessToken, nil) // caller stays signed in
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", b.AccessToken, nil)
	e.expectErr(401, "unauthorized", "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": b.RefreshToken})
	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/auth/login", "", map[string]any{
		"username": "alice", "password": "correct horse", "device": deviceInfo("x")})
	e.login("alice", "brand new pass", "x")
}

func TestLogoutRenameAndSignOutDevice(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice")
	b := e.login("alice", "correct horse", "tablet")

	renamed := expect[struct {
		Name string `json:"name"`
	}](e, 200, "PATCH", "/api/v1/devices/"+b.Device.ID, a.AccessToken, map[string]any{"name": "Living room iPad"})
	if renamed.Name != "Living room iPad" {
		t.Fatalf("rename failed: %+v", renamed)
	}
	e.expectErr(422, "validation_failed", "PATCH", "/api/v1/devices/"+b.Device.ID, a.AccessToken, map[string]any{"name": ""})
	e.expectErr(404, "not_found", "PATCH", "/api/v1/devices/not-a-uuid", a.AccessToken, map[string]any{"name": "x"})

	expect[any](e, 204, "DELETE", "/api/v1/devices/"+b.Device.ID, a.AccessToken, nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", b.AccessToken, nil) // takes effect immediately
	e.expectErr(404, "not_found", "DELETE", "/api/v1/devices/"+b.Device.ID, a.AccessToken, nil)

	// Another account's device is invisible.
	c := e.register("carol")
	e.expectErr(404, "not_found", "DELETE", "/api/v1/devices/"+a.Device.ID, c.AccessToken, nil)

	expect[any](e, 204, "POST", "/api/v1/auth/logout", a.AccessToken, nil)
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", a.AccessToken, nil)
	e.expectErr(401, "unauthorized", "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": a.RefreshToken})
}

func TestAPIVersionHeader(t *testing.T) {
	e := newEnv(t)
	s := e.register("alice")
	e.expectErr(426, "unsupported_api_version", "GET", "/api/v1/me", s.AccessToken, nil, "Shinsou-Api-Version", "2")
	e.expectErr(426, "unsupported_api_version", "POST", "/api/v1/auth/login", "", map[string]any{}, "Shinsou-Api-Version", "abc")
	expect[map[string]any](e, 200, "GET", "/api/v1/me", s.AccessToken, nil, "Shinsou-Api-Version", "1")
	expect[map[string]any](e, 200, "GET", "/api/v1/me", s.AccessToken, nil) // missing header means 1
	expect[map[string]any](e, 200, "GET", "/.well-known/shinsou", "", nil, "Shinsou-Api-Version", "9")
}

func TestInstanceInfoHealthAndErrorShape(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.InstanceName = "My Sync"; c.Contact = "admin@example.com" })
	info := expect[struct {
		Name                            string                 `json:"name"`
		Version                         string                 `json:"version"`
		APIVersions                     struct{ Min, Max int } `json:"apiVersions"`
		Registration, EmailVerification string
		Contact                         *string `json:"contact"`
		TermsURL                        *string `json:"termsUrl"`
		Features                        struct{ Sharing, Reviews bool }
	}](e, 200, "GET", "/.well-known/shinsou", "", nil)
	if info.Name != "My Sync" || info.APIVersions.Min != 1 || info.APIVersions.Max != 1 || info.Registration != "open" ||
		info.EmailVerification != "disabled" || info.Contact == nil || info.TermsURL != nil || info.Version != "test" {
		t.Fatalf("unexpected instance info: %+v", info)
	}
	if h := expect[map[string]string](e, 200, "GET", "/healthz", "", nil); h["status"] != "ok" {
		t.Fatalf("health = %v", h)
	}
	e.expectErr(404, "not_found", "GET", "/api/v1/nothing", "", nil)
	e.expectErr(405, "bad_request", "PUT", "/api/v1/me", "", nil) // 405 is rewritten into the Error shape
}

func TestAuthRateLimit(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AuthRatePerMinute, c.AuthRateBurst = 1, 2 })
	body := map[string]any{"username": "nobody", "password": "whatever1", "device": deviceInfo("x")}
	for range 2 {
		e.expectErr(401, "invalid_credentials", "POST", "/api/v1/auth/login", "", body)
	}
	ae := e.expectErr(429, "rate_limited", "POST", "/api/v1/auth/login", "", body)
	if ae.Error.RetryAfterSeconds < 1 {
		t.Fatalf("retryAfterSeconds missing: %+v", ae)
	}
	// Forwarded headers from an untrusted peer are ignored, so spoofing does not help.
	e.expectErr(429, "rate_limited", "POST", "/api/v1/auth/login", "", body, "X-Forwarded-For", "9.9.9.9")
	// Other endpoints are not affected.
	expect[map[string]string](e, 200, "GET", "/healthz", "", nil)
}

func TestAuthRateLimitHonoursTrustedProxy(t *testing.T) {
	e := newEnv(t, func(c *config.Config) {
		c.AuthRatePerMinute, c.AuthRateBurst = 1, 1
		c.TrustedProxies, _ = config.ParsePrefixes("127.0.0.1")
	})
	body := map[string]any{"username": "nobody", "password": "whatever1", "device": deviceInfo("x")}
	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/auth/login", "", body, "X-Forwarded-For", "1.1.1.1")
	e.expectErr(429, "rate_limited", "POST", "/api/v1/auth/login", "", body, "X-Forwarded-For", "1.1.1.1")
	e.expectErr(401, "invalid_credentials", "POST", "/api/v1/auth/login", "", body, "X-Forwarded-For", "2.2.2.2")
}
