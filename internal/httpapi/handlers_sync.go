package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
	"github.com/aluo96078/shinsou_sync_service/internal/syncsvc"
)

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if ok, wait := s.pushLimiter.Allow(p.UserID.String()); !ok {
		writeAPIError(w, tooMany(wait))
		return
	}
	var req syncsvc.PushRequest
	if err := s.decodeJSON(w, r, syncsvc.MaxBodyBytes, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	resp, err := s.sync.Push(r.Context(), p.UserID, req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	since, err := strconv.ParseInt(q.Get("since"), 10, 64)
	if err != nil || since < 0 {
		s.fail(w, r, apierr.BadRequest("since is required and must be a non-negative integer"))
		return
	}
	limit := syncsvc.MaxChangesLimit
	if v := q.Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 {
			s.fail(w, r, apierr.BadRequest("limit must be a positive integer"))
			return
		}
	}
	resp, err := s.sync.Changes(r.Context(), p.UserID, since, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// streamToken finds the access token for the stream endpoint: the Authorization header, or a
// `bearer.<token>` entry of Sec-WebSocket-Protocol for browsers that cannot set headers.
func streamToken(r *http.Request) (token, subprotocol string) {
	if t := bearerToken(r); t != "" {
		return t, ""
	}
	for _, h := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, part := range strings.Split(h, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "bearer.") && len(part) > len("bearer.") {
				return part[len("bearer."):], part
			}
		}
	}
	return "", ""
}

const streamCheckInterval = 30 * time.Second

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	tok, sub := streamToken(r)
	if tok == "" {
		writeAPIError(w, apierr.Unauthorized("missing bearer token"))
		return
	}
	p, err := s.auth.Authenticate(r.Context(), tok)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if e := s.verificationGate(p); e != nil {
		writeAPIError(w, e)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") && !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.streamSSE(w, r, p)
		return
	}
	var protos []string
	if sub != "" {
		protos = []string{sub}
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: protos})
	if err != nil {
		return // Accept already replied
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())

	subscription := s.hub.Subscribe(p.UserID)
	defer subscription.Close()
	cur, err := s.sync.CurrentSeq(ctx, p.UserID)
	if err != nil {
		c.Close(websocket.StatusInternalError, "internal error")
		return
	}
	send := func(seq int64) error {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return c.Write(wctx, websocket.MessageText, []byte(fmt.Sprintf(`{"seq":%d}`, seq)))
	}
	if send(cur) != nil {
		return
	}
	tick := time.NewTicker(streamCheckInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case seq := <-subscription.C:
			if send(seq) != nil {
				return
			}
		case <-tick.C:
			if ok, err := s.auth.DeviceActive(ctx, p.UserID, p.DeviceID); err == nil && !ok {
				c.Close(websocket.StatusPolicyViolation, "signed out")
				return
			}
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) streamSSE(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ctx := r.Context()
	subscription := s.hub.Subscribe(p.UserID)
	defer subscription.Close()
	cur, err := s.sync.CurrentSeq(ctx, p.UserID)
	if err != nil {
		return
	}
	emit := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !emit("data: {\"seq\":%d}\n\n", cur) {
		return
	}
	tick := time.NewTicker(streamCheckInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case seq := <-subscription.C:
			if !emit("data: {\"seq\":%d}\n\n", seq) {
				return
			}
		case <-tick.C:
			if ok, err := s.auth.DeviceActive(ctx, p.UserID, p.DeviceID); err == nil && !ok {
				return
			}
			if !emit(": ping\n\n") {
				return
			}
		}
	}
}
