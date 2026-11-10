// Package httpapi exposes the services over HTTP as described in api/openapi.yaml.
package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/account"
	"github.com/aluo96078/shinsou_sync_service/internal/admin"
	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/instance"
	"github.com/aluo96078/shinsou_sync_service/internal/library"
	"github.com/aluo96078/shinsou_sync_service/internal/mail"
	"github.com/aluo96078/shinsou_sync_service/internal/notify"
	"github.com/aluo96078/shinsou_sync_service/internal/settings"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

// Server wires the services into an http.Handler.
type Server struct {
	cfg         config.Config
	pool        *pgxpool.Pool
	log         *slog.Logger
	version     string
	auth        *auth.Service
	sync        *syncsvc.Service
	hub         *notify.Hub
	authLimiter *Limiter
	pushLimiter *Limiter
	handler     http.Handler

	settings *settings.Store
	mail     *mail.Queue
	account  *account.Service
	library  *library.Service
	admin    *admin.Service
	csrfKey  []byte
}

const maxSmallBody = 64 << 10

// Option customises New.
type Option func(*options)

type options struct {
	sender mail.Sender
	webFS  fs.FS
}

// WithMailSender replaces the SMTP sender; tests use it to observe outgoing mail. Mail is still
// only sent when the configuration says the server can send it (config.Config.EmailAvailable).
func WithMailSender(m mail.Sender) Option { return func(o *options) { o.sender = m } }

// WithWebFS serves a single-page front end from fsys (rooted at the build output) for every
// GET/HEAD path the API does not claim. Without it the server speaks only the API.
func WithWebFS(fsys fs.FS) Option { return func(o *options) { o.webFS = fsys } }

// New builds the server. It loads (or creates) the JWT signing key and the CSRF key, and loads the
// runtime settings.
func New(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, log *slog.Logger, version string, opts ...Option) (*Server, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	key, err := instance.SigningKey(ctx, pool)
	if err != nil {
		return nil, err
	}
	csrfKey, err := instance.CSRFKey(ctx, pool)
	if err != nil {
		return nil, err
	}
	live := settings.New(pool, cfg)
	if err := live.Load(ctx); err != nil {
		return nil, err
	}
	sender := o.sender
	if sender == nil && cfg.EmailAvailable() {
		sender, err = mail.NewSMTPSender(mail.SMTPFromConfig(cfg))
		if err != nil {
			return nil, err
		}
	}
	queue := mail.NewQueue(sender, cfg.EmailAvailable(), mail.QueueSize, 2, log)

	hasher := auth.NewHasher(cfg.Argon2MemoryKiB, cfg.Argon2Time, cfg.Argon2Threads, 4)
	hub := notify.NewHub()
	authSvc := auth.NewService(pool, cfg, hasher, auth.NewIssuer(key, cfg.AccessTokenTTL)).WithRuntime(live, queue)
	s := &Server{
		cfg:         cfg,
		pool:        pool,
		log:         log,
		version:     version,
		auth:        authSvc,
		sync:        syncsvc.New(pool, hub, cfg.RecordQuota),
		hub:         hub,
		authLimiter: NewLimiter(cfg.AuthRatePerMinute, cfg.AuthRateBurst),
		pushLimiter: NewLimiter(cfg.PushRatePerMinute, cfg.PushRateBurst),
		settings:    live,
		mail:        queue,
		account:     account.New(pool, authSvc, cfg.RecordQuota),
		library:     library.New(pool),
		admin:       admin.New(pool, version, cfg.EmailAvailable()),
		csrfKey:     csrfKey,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/shinsou", s.handleInstance)
	mux.HandleFunc("GET /healthz", s.handleHealth)

	mux.HandleFunc("POST /api/v1/auth/register", s.limitAuth(s.handleRegister))
	mux.HandleFunc("POST /api/v1/auth/login", s.limitAuth(s.handleLogin))
	mux.HandleFunc("POST /api/v1/auth/refresh", s.limitAuth(s.handleRefresh))
	mux.HandleFunc("POST /api/v1/auth/logout", s.authed(s.handleLogout))
	mux.HandleFunc("GET /api/v1/me", s.authed(s.handleMe))
	mux.HandleFunc("POST /api/v1/me/password", s.authed(s.handleChangePassword))
	mux.HandleFunc("GET /api/v1/devices", s.authed(s.handleListDevices))
	mux.HandleFunc("PATCH /api/v1/devices/{deviceId}", s.authed(s.handleRenameDevice))
	mux.HandleFunc("DELETE /api/v1/devices/{deviceId}", s.authed(s.handleSignOutDevice))

	mux.HandleFunc("POST /api/v1/auth/email/verify", s.limitAuth(s.handleVerifyEmail))
	mux.HandleFunc("POST /api/v1/auth/password/forgot", s.limitAuth(s.handleForgotPassword))
	mux.HandleFunc("POST /api/v1/auth/password/reset", s.limitAuth(s.handleResetPassword))
	mux.HandleFunc("PUT /api/v1/me/email", s.authed(s.handleChangeEmail))
	mux.HandleFunc("POST /api/v1/me/email/verification", s.authed(s.handleResendVerification))
	mux.HandleFunc("GET /api/v1/me/usage", s.authed(s.handleUsage))
	mux.HandleFunc("GET /api/v1/me/export", s.verified(s.handleExport))
	mux.HandleFunc("POST /api/v1/me/delete", s.authed(s.handleDeleteAccount))

	mux.HandleFunc("POST /api/v1/web/register", s.limitAuth(s.handleWebRegister))
	mux.HandleFunc("POST /api/v1/web/login", s.limitAuth(s.handleWebLogin))
	mux.HandleFunc("POST /api/v1/web/refresh", s.limitAuth(s.handleWebRefresh))
	mux.HandleFunc("POST /api/v1/web/logout", s.handleWebLogout)

	mux.HandleFunc("POST /api/v1/sync/push", s.verified(s.handlePush))
	mux.HandleFunc("GET /api/v1/sync/changes", s.verified(s.handleChanges))
	mux.HandleFunc("GET /api/v1/sync/stream", s.handleStream)

	mux.HandleFunc("GET /api/v1/library", s.verified(s.handleLibraryList))
	mux.HandleFunc("GET /api/v1/library/{key}", s.verified(s.handleLibraryGet))
	mux.HandleFunc("GET /api/v1/categories", s.verified(s.handleCategories))

	mux.HandleFunc("GET /api/v1/admin/overview", s.adminOnly(s.handleAdminOverview))
	mux.HandleFunc("GET /api/v1/admin/users", s.adminOnly(s.handleAdminListUsers))
	mux.HandleFunc("PATCH /api/v1/admin/users/{userId}", s.adminOnly(s.handleAdminUpdateUser))
	mux.HandleFunc("DELETE /api/v1/admin/users/{userId}", s.adminOnly(s.handleAdminDeleteUser))
	mux.HandleFunc("POST /api/v1/admin/users/{userId}/sign-out", s.adminOnly(s.handleAdminSignOutUser))
	mux.HandleFunc("GET /api/v1/admin/invites", s.adminOnly(s.handleAdminListInvites))
	mux.HandleFunc("POST /api/v1/admin/invites", s.adminOnly(s.handleAdminCreateInvite))
	mux.HandleFunc("DELETE /api/v1/admin/invites/{inviteId}", s.adminOnly(s.handleAdminDeleteInvite))
	mux.HandleFunc("GET /api/v1/admin/settings", s.adminOnly(s.handleAdminGetSettings))
	mux.HandleFunc("PATCH /api/v1/admin/settings", s.adminOnly(s.handleAdminUpdateSettings))
	mux.HandleFunc("GET /api/v1/admin/audit", s.adminOnly(s.handleAdminAudit))

	var root http.Handler = mux
	if o.webFS != nil {
		root = spaFallback(mux, newSPA(o.webFS))
	}
	s.handler = s.wrap(root)
	return s, nil
}

// Close stops the background mail workers after letting them finish what is queued.
func (s *Server) Close() { s.mail.Close() }

// WaitMail blocks until every queued mail has been handed to the sender (or failed). It exists so
// tests can observe outgoing mail without sleeping.
func (s *Server) WaitMail() { s.mail.Wait() }

// Handler returns the root handler.
func (s *Server) Handler() http.Handler { return s.handler }

// HTTPServer returns an http.Server for the handler. There is no write timeout because
// streams are long-lived.
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

type principalHandler func(w http.ResponseWriter, r *http.Request, p *auth.Principal)

// bearerToken extracts a token from the Authorization header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// authed requires a valid access token for a device that is still signed in.
func (s *Server) authed(h principalHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if tok == "" {
			writeAPIError(w, apierr.Unauthorized("missing bearer token"))
			return
		}
		p, err := s.auth.Authenticate(r.Context(), tok)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		h(w, r, p)
	}
}

// verified is authed plus the email verification gate (docs/spec/web.md section 3).
func (s *Server) verified(h principalHandler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if e := s.verificationGate(p); e != nil {
			writeAPIError(w, e)
			return
		}
		h(w, r, p)
	})
}

// verificationGate is the 403 for an unverified, non-administrator account while the instance
// requires verification; nil otherwise.
func (s *Server) verificationGate(p *auth.Principal) *apierr.Error {
	if p.EmailVerified || p.Role == "admin" || !s.auth.RequiresVerification() {
		return nil
	}
	return apierr.New(http.StatusForbidden, "email_unverified", "verify your email address to use this endpoint")
}

// adminOnly is authed plus a role check; the role comes from the database on every request.
func (s *Server) adminOnly(h principalHandler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if p.Role != "admin" {
			writeAPIError(w, apierr.Forbidden("administrator access is required"))
			return
		}
		h(w, r, p)
	})
}
