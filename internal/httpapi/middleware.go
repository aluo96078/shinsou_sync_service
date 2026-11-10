package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
)

type ctxKey int

const requestIDKey ctxKey = 1

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey).(string)
	return id
}

// statusWriter records the status for logging, keeps streaming and WebSocket upgrades working,
// and rewrites the router's plain-text 404/405 answers into the Error shape.
type statusWriter struct {
	http.ResponseWriter
	status   int
	bytes    int
	discard  bool
	wroteHdr bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wroteHdr {
		return
	}
	w.wroteHdr = true
	w.status = code
	if (code == http.StatusNotFound || code == http.StatusMethodNotAllowed) &&
		strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Type", "application/json")
		w.ResponseWriter.WriteHeader(code)
		e := apierr.NotFound("no such endpoint")
		if code == http.StatusMethodNotAllowed {
			e = apierr.New(code, "bad_request", "method not allowed")
		}
		body := `{"error":{"code":"` + e.Code + `","message":"` + e.Message + `"}}` + "\n"
		n, _ := w.ResponseWriter.Write([]byte(body))
		w.bytes += n
		w.discard = true
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHdr {
		w.WriteHeader(http.StatusOK)
	}
	if w.discard {
		return len(b), nil
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking is not supported")
	}
	if !w.wroteHdr {
		w.wroteHdr = true
		w.status = http.StatusSwitchingProtocols
	}
	return h.Hijack()
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// wrap applies request IDs, panic recovery, logging and the API version check.
func (s *Server) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var rid [8]byte
		_, _ = rand.Read(rid[:])
		id := hex.EncodeToString(rid[:])
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
		rw.Header().Set("X-Request-Id", id)
		rw.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			rw.Header().Set("Cache-Control", "no-store")
		}
		w := &statusWriter{ResponseWriter: rw, status: http.StatusOK}

		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec) // a handler asked to drop the connection (a failed streamed export)
				}
				s.log.Error("panic", slog.Any("panic", rec), slog.String("request_id", id), slog.String("path", r.URL.Path))
				if !w.wroteHdr {
					writeAPIError(w, apierr.Internal())
				}
			}
			if r.URL.Path == "/healthz" && w.status == http.StatusOK {
				return
			}
			s.log.Info("request", slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Int("status", w.status),
				slog.Int("bytes", w.bytes), slog.Duration("took", time.Since(start)), slog.String("request_id", id))
		}()

		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			if v := r.Header.Get("Shinsou-Api-Version"); v != "" && v != "1" {
				writeAPIError(w, apierr.New(http.StatusUpgradeRequired, "unsupported_api_version",
					"this server speaks Shinsou API version 1; update the app or the server"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// limitAuth applies the per-IP limiter to unauthenticated account endpoints.
func (s *Server) limitAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := s.authLimiter.Allow(clientIP(r, s.cfg.TrustedProxies)); !ok {
			writeAPIError(w, tooMany(wait))
			return
		}
		h(w, r)
	}
}

func tooMany(wait time.Duration) *apierr.Error {
	secs := int(wait.Seconds()) + 1
	return &apierr.Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many requests; slow down", RetryAfterSeconds: secs}
}
