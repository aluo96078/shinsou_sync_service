// Package httpapi exposes the services over HTTP as described in api/openapi.yaml.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/instance"
	"github.com/aluo96078/shinsou_sync_service/internal/notify"
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
}

const maxSmallBody = 64 << 10

// New builds the server. It loads (or creates) the JWT signing key.
func New(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, log *slog.Logger, version string) (*Server, error) {
	key, err := instance.SigningKey(ctx, pool)
	if err != nil {
		return nil, err
	}
	hasher := auth.NewHasher(cfg.Argon2MemoryKiB, cfg.Argon2Time, cfg.Argon2Threads, 4)
	hub := notify.NewHub()
	s := &Server{
		cfg:         cfg,
		pool:        pool,
		log:         log,
		version:     version,
		auth:        auth.NewService(pool, cfg, hasher, auth.NewIssuer(key, cfg.AccessTokenTTL)),
		sync:        syncsvc.New(pool, hub, cfg.RecordQuota),
		hub:         hub,
		authLimiter: NewLimiter(cfg.AuthRatePerMinute, cfg.AuthRateBurst),
		pushLimiter: NewLimiter(cfg.PushRatePerMinute, cfg.PushRateBurst),
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

	mux.HandleFunc("POST /api/v1/sync/push", s.authed(s.handlePush))
	mux.HandleFunc("GET /api/v1/sync/changes", s.authed(s.handleChanges))
	mux.HandleFunc("GET /api/v1/sync/stream", s.handleStream)

	s.handler = s.wrap(mux)
	return s, nil
}

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
