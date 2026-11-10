package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/aluo96078/shinsou_sync_service/internal/config"
)

type webSession struct {
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"accessTokenExpiresAt"`
	User        struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	} `json:"user"`
	Device struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Platform string `json:"platform"`
	} `json:"device"`
}

const chromeMac = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

func (e *env) webRegister(username string, headers ...string) rawResp {
	return e.rawCall("POST", "/api/v1/web/register", map[string]any{"username": username, "password": "correct horse"}, nil, headers...)
}

func (e *env) webLogin(username string, headers ...string) rawResp {
	return e.rawCall("POST", "/api/v1/web/login", map[string]any{"username": username, "password": "correct horse"}, nil, headers...)
}

func TestWebRegisterSetsCookiesAndHidesRefreshToken(t *testing.T) {
	e := newEnv(t)
	r := e.webRegister("alice", "User-Agent", chromeMac)
	if r.Status != 201 {
		t.Fatalf("status %d: %s", r.Status, r.Body)
	}
	refresh, csrf := r.cookie("shinsou_refresh"), r.cookie("shinsou_csrf")
	if refresh == nil || csrf == nil {
		t.Fatalf("cookies missing: %+v", r.Cookies)
	}
	if strings.Contains(string(r.Body), "refreshToken") || strings.Contains(string(r.Body), refresh.Value) {
		t.Fatalf("the refresh token must not be in the body: %s", r.Body)
	}
	ttl := int(e.cfg.RefreshTokenTTL.Seconds())
	if !refresh.HttpOnly || refresh.SameSite != http.SameSiteLaxMode || refresh.Path != "/api/v1/web" || !refresh.Secure || refresh.MaxAge != ttl {
		t.Errorf("refresh cookie attributes wrong: %+v", refresh)
	}
	if csrf.HttpOnly || csrf.SameSite != http.SameSiteLaxMode || csrf.Path != "/" || !csrf.Secure || csrf.MaxAge != ttl || csrf.Value == "" {
		t.Errorf("csrf cookie attributes wrong: %+v", csrf)
	}
	var s webSession
	mustJSON(t, r.Body, &s)
	if s.Device.Platform != "web" || s.Device.Name != "Chrome on macOS" || s.User.Username != "alice" || s.AccessToken == "" {
		t.Fatalf("unexpected session: %+v", s)
	}
	expect[map[string]any](e, 200, "GET", "/api/v1/me", s.AccessToken, nil)

	// An explicit device name wins; an unknown User-Agent falls back to a generic label.
	r2 := e.rawCall("POST", "/api/v1/web/login", map[string]any{"username": "alice", "password": "correct horse", "deviceName": "Reading room"}, nil)
	var s2 webSession
	mustJSON(t, r2.Body, &s2)
	if r2.Status != 200 || s2.Device.Name != "Reading room" {
		t.Fatalf("login: %d %s", r2.Status, r2.Body)
	}
	r3 := e.webLogin("alice")
	var s3 webSession
	mustJSON(t, r3.Body, &s3)
	if s3.Device.Name != "Web browser" {
		t.Fatalf("default device name = %q", s3.Device.Name)
	}
	if e.webLogin("alice", "Origin", "https://evil.example").Status != 403 {
		t.Fatal("cross-origin login must be refused")
	}
	bad := e.rawCall("POST", "/api/v1/web/login", map[string]any{"username": "alice", "password": "nope nope"}, nil)
	if bad.Status != 401 || bad.errCode() != "invalid_credentials" {
		t.Fatalf("bad password: %d %s", bad.Status, bad.Body)
	}
	if e.rawCall("POST", "/api/v1/web/register", map[string]any{"username": "al", "password": "correct horse"}, nil).Status != 422 {
		t.Fatal("validation errors keep their status")
	}
}

func TestWebCookiesHaveNoSecureOnPlainHTTPPublicURL(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.PublicURL = "http://localhost:8080" })
	r := e.webRegister("alice")
	if r.Status != 201 {
		t.Fatalf("status %d: %s", r.Status, r.Body)
	}
	if r.cookie("shinsou_refresh").Secure || r.cookie("shinsou_csrf").Secure {
		t.Fatal("cookies must not be Secure when the public URL is http and the request is not TLS")
	}
}

func TestWebRefreshRotationAndReuse(t *testing.T) {
	e := newEnv(t)
	reg := e.webRegister("alice")
	var s0 webSession
	mustJSON(t, reg.Body, &s0)
	cookies := reg.Cookies
	csrf1 := reg.cookie("shinsou_csrf").Value

	r := e.rawCall("POST", "/api/v1/web/refresh", nil, cookies, "X-CSRF-Token", csrf1)
	if r.Status != 200 {
		t.Fatalf("refresh: %d %s", r.Status, r.Body)
	}
	var tok struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"accessTokenExpiresAt"`
	}
	mustJSON(t, r.Body, &tok)
	if tok.AccessToken == "" || tok.ExpiresAt == 0 || strings.Contains(string(r.Body), "refresh") {
		t.Fatalf("unexpected refresh body: %s", r.Body)
	}
	newRefresh, newCSRF := r.cookie("shinsou_refresh"), r.cookie("shinsou_csrf")
	if newRefresh == nil || newRefresh.Value == reg.cookie("shinsou_refresh").Value || newCSRF == nil || newCSRF.Value == csrf1 {
		t.Fatalf("both cookies must rotate: %+v", r.Cookies)
	}
	expect[map[string]any](e, 200, "GET", "/api/v1/me", tok.AccessToken, nil)

	// The rotated cookie works once more...
	r2 := e.rawCall("POST", "/api/v1/web/refresh", nil, r.Cookies, "X-CSRF-Token", newCSRF.Value)
	if r2.Status != 200 {
		t.Fatalf("second refresh: %d %s", r2.Status, r2.Body)
	}
	// ...but replaying the first one signs the browser out and clears the cookies.
	reuse := e.rawCall("POST", "/api/v1/web/refresh", nil, cookies, "X-CSRF-Token", csrf1)
	if reuse.Status != 401 || reuse.errCode() != "refresh_token_reused" {
		t.Fatalf("reuse: %d %s", reuse.Status, reuse.Body)
	}
	for _, name := range []string{"shinsou_refresh", "shinsou_csrf"} {
		c := reuse.cookie(name)
		if c == nil || c.MaxAge >= 0 || c.Value != "" {
			t.Errorf("%s was not cleared: %+v", name, c)
		}
	}
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", tok.AccessToken, nil)
}

func TestWebRefreshNeedsCookieAndCSRF(t *testing.T) {
	e := newEnv(t)
	a := e.webRegister("alice")
	b := e.webRegister("bob")
	acookies, acsrf := a.Cookies, a.cookie("shinsou_csrf").Value
	bcsrf := b.cookie("shinsou_csrf").Value

	noCookie := e.rawCall("POST", "/api/v1/web/refresh", nil, nil, "X-CSRF-Token", acsrf)
	if noCookie.Status != 401 || noCookie.cookie("shinsou_refresh") == nil || noCookie.cookie("shinsou_refresh").MaxAge >= 0 {
		t.Fatalf("no cookie: %d %s", noCookie.Status, noCookie.Body)
	}
	for name, hdr := range map[string][]string{
		"missing header":  nil,
		"wrong token":     {"X-CSRF-Token", "AAAA"},
		"other sessions":  {"X-CSRF-Token", bcsrf},
		"empty":           {"X-CSRF-Token", ""},
		"origin mismatch": {"X-CSRF-Token", acsrf, "Origin", "https://evil.example"},
		"null origin":     {"X-CSRF-Token", acsrf, "Origin", "null"},
	} {
		r := e.rawCall("POST", "/api/v1/web/refresh", nil, acookies, hdr...)
		if r.Status != 403 || r.errCode() != "csrf_failed" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
		if r.cookie("shinsou_refresh") != nil {
			t.Errorf("%s: a CSRF failure must not touch the cookies", name)
		}
	}
	// The token still works after all the failures, from either allowed origin.
	for _, origin := range []string{"", "https://sync.test", e.srv.URL} {
		hdr := []string{"X-CSRF-Token", acsrf}
		if origin != "" {
			hdr = append(hdr, "Origin", origin)
		}
		r := e.rawCall("POST", "/api/v1/web/refresh", nil, acookies, hdr...)
		if r.Status != 200 {
			t.Fatalf("origin %q: %d %s", origin, r.Status, r.Body)
		}
		acookies, acsrf = r.Cookies, r.cookie("shinsou_csrf").Value
	}
}

func TestWebLogout(t *testing.T) {
	e := newEnv(t)
	reg := e.webRegister("alice")
	var s webSession
	mustJSON(t, reg.Body, &s)
	csrf := reg.cookie("shinsou_csrf").Value

	if r := e.rawCall("POST", "/api/v1/web/logout", nil, reg.Cookies); r.Status != 403 || r.errCode() != "csrf_failed" {
		t.Fatalf("logout without csrf: %d %s", r.Status, r.Body)
	}
	if r := e.rawCall("POST", "/api/v1/web/logout", nil, reg.Cookies, "X-CSRF-Token", csrf, "Origin", "https://evil.example"); r.Status != 403 {
		t.Fatalf("logout with a foreign origin: %d", r.Status)
	}
	expect[map[string]any](e, 200, "GET", "/api/v1/me", s.AccessToken, nil)

	r := e.rawCall("POST", "/api/v1/web/logout", nil, reg.Cookies, "X-CSRF-Token", csrf)
	if r.Status != 204 || r.cookie("shinsou_refresh").MaxAge >= 0 || r.cookie("shinsou_csrf").MaxAge >= 0 {
		t.Fatalf("logout: %d %+v", r.Status, r.Cookies)
	}
	e.expectErr(401, "unauthorized", "GET", "/api/v1/me", s.AccessToken, nil)
	if again := e.rawCall("POST", "/api/v1/web/refresh", nil, reg.Cookies, "X-CSRF-Token", csrf); again.Status != 401 {
		t.Fatalf("refresh after logout: %d", again.Status)
	}
	// Without a cookie there is nothing to protect: still 204 and the cookies are cleared.
	if r := e.rawCall("POST", "/api/v1/web/logout", nil, nil); r.Status != 204 || r.cookie("shinsou_refresh") == nil {
		t.Fatalf("anonymous logout: %d", r.Status)
	}
}

func TestWebAndAppEndpointsCoexist(t *testing.T) {
	e := newEnv(t)
	reg := e.webRegister("alice")
	if reg.Status != 201 {
		t.Fatal(reg.Status)
	}
	app := e.login("alice", "correct horse", "phone") // /auth/login still returns the refresh token in the body
	if app.RefreshToken == "" {
		t.Fatal("the app endpoints keep returning the refresh token")
	}
	tok := expect[map[string]any](e, 200, "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": app.RefreshToken})
	if tok["refreshToken"] == nil {
		t.Fatal("/auth/refresh keeps returning a rotated refresh token")
	}
}

func TestWebAndAuthEndpointsShareTheIPLimiter(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.AuthRatePerMinute, c.AuthRateBurst = 1, 3 })
	bad := map[string]any{"username": "nobody", "password": "whatever1", "device": deviceInfo("p")}
	if st, _ := e.call("POST", "/api/v1/auth/login", "", bad); st != 401 {
		t.Fatalf("first: %d", st)
	}
	if r := e.rawCall("POST", "/api/v1/web/login", map[string]any{"username": "nobody", "password": "whatever1"}, nil); r.Status != 401 {
		t.Fatalf("second: %d", r.Status)
	}
	if r := e.rawCall("POST", "/api/v1/web/refresh", nil, nil); r.Status != 401 {
		t.Fatalf("third: %d", r.Status)
	}
	r := e.rawCall("POST", "/api/v1/web/login", map[string]any{"username": "nobody", "password": "whatever1"}, nil)
	if r.Status != 429 || r.errCode() != "rate_limited" || r.Header.Get("Retry-After") == "" {
		t.Fatalf("limited: %d %s", r.Status, r.Body)
	}
	if st, _ := e.call("POST", "/api/v1/auth/login", "", bad); st != 429 {
		t.Fatalf("the app endpoint shares the bucket: %d", st)
	}
}
