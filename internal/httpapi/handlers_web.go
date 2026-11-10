package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
)

// Cookie names of the web session (docs/spec/web.md section 1).
const (
	refreshCookie = "shinsou_refresh"
	csrfCookie    = "shinsou_csrf"
	webAPIPath    = "/api/v1/web"
)

// webSession is WebSession in the API: a Session without the refresh token, which only ever
// travels in the HttpOnly cookie.
type webSession struct {
	AccessToken          string      `json:"accessToken"`
	AccessTokenExpiresAt int64       `json:"accessTokenExpiresAt"`
	User                 auth.User   `json:"user"`
	Device               auth.Device `json:"device"`
}

type webTokens struct {
	AccessToken          string `json:"accessToken"`
	AccessTokenExpiresAt int64  `json:"accessTokenExpiresAt"`
}

func csrfFailed(msg string) *apierr.Error {
	return apierr.New(http.StatusForbidden, "csrf_failed", msg)
}

// csrfToken is base64url(HMAC-SHA256(csrfKey, refreshToken)).
func (s *Server) csrfToken(refresh string) string {
	mac := hmac.New(sha256.New, s.csrfKey)
	mac.Write([]byte(refresh))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// secure reports whether cookies get the Secure attribute: an https public URL or a TLS request.
func (s *Server) secure(r *http.Request) bool {
	return strings.HasPrefix(s.cfg.PublicURL, "https://") || r.TLS != nil
}

// originOf returns scheme://host of a URL, or "".
func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// checkOrigin rejects a cross-site browser request. A request without Origin passes (non-browser
// clients); one whose Origin is neither the request's own origin nor the public URL's does not.
func (s *Server) checkOrigin(r *http.Request) *apierr.Error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	origin = strings.ToLower(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if origin == scheme+"://"+strings.ToLower(r.Host) {
		return nil
	}
	if pub := originOf(s.cfg.PublicURL); pub != "" && origin == pub {
		return nil
	}
	return csrfFailed("cross-origin request rejected")
}

func (s *Server) setSessionCookies(w http.ResponseWriter, r *http.Request, refresh string) {
	maxAge := int(s.cfg.RefreshTokenTTL.Seconds())
	secure := s.secure(r)
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: refresh, Path: webAPIPath, MaxAge: maxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: s.csrfToken(refresh), Path: "/", MaxAge: maxAge,
		Secure: secure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	secure := s.secure(r)
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: "", Path: webAPIPath, MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: "", Path: "/", MaxAge: -1,
		Secure: secure, SameSite: http.SameSiteLaxMode})
}

// checkCSRF compares the X-CSRF-Token header with the value recomputed from the refresh cookie.
func (s *Server) checkCSRF(r *http.Request, refresh string) *apierr.Error {
	got := r.Header.Get("X-CSRF-Token")
	want := s.csrfToken(refresh)
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return csrfFailed("missing or wrong X-CSRF-Token header")
	}
	return nil
}

func (s *Server) webSessionResponse(w http.ResponseWriter, r *http.Request, status int, sess *auth.Session) {
	s.setSessionCookies(w, r, sess.RefreshToken)
	writeJSON(w, status, webSession{AccessToken: sess.AccessToken, AccessTokenExpiresAt: sess.AccessTokenExpiresAt,
		User: sess.User, Device: sess.Device})
}

// deviceFor builds the device record of a browser session.
func deviceFor(r *http.Request, name *string) (*auth.DeviceInfo, error) {
	n := ""
	if name != nil {
		n = strings.TrimSpace(*name)
	}
	if n == "" {
		n = deviceNameFromUA(r.UserAgent())
	} else if utf8.RuneCountInString(n) > 64 {
		return nil, apierr.Validation("deviceName must be at most 64 characters")
	}
	return &auth.DeviceInfo{Name: n, Platform: "web"}, nil
}

func (s *Server) handleWebRegister(w http.ResponseWriter, r *http.Request) {
	if e := s.checkOrigin(r); e != nil {
		writeAPIError(w, e)
		return
	}
	var in struct {
		Username   string  `json:"username"`
		Password   string  `json:"password"`
		Email      *string `json:"email"`
		InviteCode *string `json:"inviteCode"`
		SetupCode  *string `json:"setupCode"`
		DeviceName *string `json:"deviceName"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	dev, err := deviceFor(r, in.DeviceName)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.auth.Register(r.Context(), auth.RegisterInput{Username: in.Username, Password: in.Password, Email: in.Email,
		InviteCode: in.InviteCode, SetupCode: in.SetupCode, Device: dev})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.webSessionResponse(w, r, http.StatusCreated, sess)
}

func (s *Server) handleWebLogin(w http.ResponseWriter, r *http.Request) {
	if e := s.checkOrigin(r); e != nil {
		writeAPIError(w, e)
		return
	}
	var in struct {
		Username   string  `json:"username"`
		Password   string  `json:"password"`
		DeviceName *string `json:"deviceName"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	dev, err := deviceFor(r, in.DeviceName)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.auth.Login(r.Context(), auth.LoginInput{Username: in.Username, Password: in.Password, Device: dev})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.webSessionResponse(w, r, http.StatusOK, sess)
}

func (s *Server) handleWebRefresh(w http.ResponseWriter, r *http.Request) {
	if e := s.checkOrigin(r); e != nil {
		writeAPIError(w, e)
		return
	}
	c, err := r.Cookie(refreshCookie)
	if err != nil || c.Value == "" {
		s.clearSessionCookies(w, r)
		writeAPIError(w, apierr.Unauthorized("no web session"))
		return
	}
	if e := s.checkCSRF(r, c.Value); e != nil {
		writeAPIError(w, e)
		return
	}
	t, err := s.auth.Refresh(r.Context(), c.Value)
	if err != nil {
		if e, ok := apierr.As(err); ok && e.Status == http.StatusUnauthorized {
			s.clearSessionCookies(w, r)
		}
		s.fail(w, r, err)
		return
	}
	s.setSessionCookies(w, r, t.RefreshToken)
	writeJSON(w, http.StatusOK, webTokens{AccessToken: t.AccessToken, AccessTokenExpiresAt: t.AccessTokenExpiresAt})
}

func (s *Server) handleWebLogout(w http.ResponseWriter, r *http.Request) {
	if e := s.checkOrigin(r); e != nil {
		writeAPIError(w, e)
		return
	}
	if c, err := r.Cookie(refreshCookie); err == nil && c.Value != "" {
		if e := s.checkCSRF(r, c.Value); e != nil {
			writeAPIError(w, e)
			return
		}
		if err := s.auth.LogoutRefreshToken(r.Context(), c.Value); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.clearSessionCookies(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// deviceNameFromUA turns a User-Agent into a short label such as "Chrome on macOS".
func deviceNameFromUA(ua string) string {
	browser := ""
	switch {
	case strings.Contains(ua, "Edg/") || strings.Contains(ua, "EdgA/") || strings.Contains(ua, "EdgiOS/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/") || strings.Contains(ua, "Opera"):
		browser = "Opera"
	case strings.Contains(ua, "Firefox/") || strings.Contains(ua, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/") && strings.Contains(ua, "Version/"):
		browser = "Safari"
	}
	osName := ""
	switch {
	case strings.Contains(ua, "Android"):
		osName = "Android"
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad") || strings.Contains(ua, "iPod"):
		osName = "iOS"
	case strings.Contains(ua, "Windows"):
		osName = "Windows"
	case strings.Contains(ua, "Macintosh") || strings.Contains(ua, "Mac OS X"):
		osName = "macOS"
	case strings.Contains(ua, "CrOS"):
		osName = "ChromeOS"
	case strings.Contains(ua, "Linux"):
		osName = "Linux"
	}
	switch {
	case browser != "" && osName != "":
		return browser + " on " + osName
	case browser != "":
		return browser
	default:
		return "Web browser"
	}
}
