package httpapi

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
	"github.com/aluo96078/shinsou_sync_service/internal/social"
)

// sharing is verified plus the sharing switch: every /api/v1/shares endpoint answers 403
// feature_disabled while the operator has sharing turned off (docs/spec/sharing.md section 1).
func (s *Server) sharing(h principalHandler) http.HandlerFunc {
	return s.verified(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if !s.social.Enabled() {
			writeAPIError(w, social.ErrDisabled())
			return
		}
		h(w, r, p)
	})
}

// limitSocial applies the per-account limiter shared by user search and share creation.
func (s *Server) limitSocial(h principalHandler) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if ok, wait := s.socialLimiter.Allow(p.UserID.String()); !ok {
			writeAPIError(w, tooMany(wait))
			return
		}
		h(w, r, p)
	}
}

func (s *Server) handleSearchUsers(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > social.MaxSearchLimit {
			s.fail(w, r, apierr.BadRequest("limit must be an integer from 1 to "+strconv.Itoa(social.MaxSearchLimit)))
			return
		}
		limit = n
	}
	users, err := s.social.Search(r.Context(), p.UserID, r.URL.Query().Get("q"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// userList serves one of the caller's lists of accounts.
func (s *Server) userList(list func(*social.Service, *http.Request, uuid.UUID, int, int) (*social.UserPage, error)) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		offset, limit, err := pageParams(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		page, err := list(s.social, r, p.UserID, offset, limit)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

// userAction serves a PUT or DELETE on one of the caller's relations to another account.
func (s *Server) userAction(act func(*social.Service, *http.Request, uuid.UUID, uuid.UUID) error) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		target, err := pathUUID(r, "userId", "user")
		if err == nil {
			err = act(s.social, r, p.UserID, target)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleGetSharing(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	policy, err := s.social.ReceiveFrom(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"receiveFrom": policy})
}

func (s *Server) handlePutSharing(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		ReceiveFrom string `json:"receiveFrom"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.social.SetReceiveFrom(r.Context(), p.UserID, in.ReceiveFrom); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"receiveFrom": in.ReceiveFrom})
}

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	offset, limit, err := pageParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	var sent bool
	switch q.Get("box") {
	case "", "inbox":
	case "sent":
		sent = true
	default:
		s.fail(w, r, apierr.BadRequest("box must be inbox or sent"))
		return
	}
	page, err := s.social.ListShares(r.Context(), p.UserID, sent, q.Get("status"), offset, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		RecipientID string `json:"recipientId"`
		Work        string `json:"work"`
		Message     string `json:"message"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	recipient, err := uuid.Parse(in.RecipientID)
	if err != nil {
		s.fail(w, r, apierr.Validation("recipientId must be a user id"))
		return
	}
	share, created, err := s.social.CreateShare(r.Context(), p.UserID, recipient, in.Work, in.Message)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, share)
}

func (s *Server) handleShareSummary(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, err := s.social.PendingCount(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"pending": n})
}

// shareAction serves accept and dismiss.
func (s *Server) shareAction(act func(*social.Service, *http.Request, uuid.UUID, uuid.UUID) (*social.Share, error)) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		id, err := pathUUID(r, "shareId", "share")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		share, err := act(s.social, r, p.UserID, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, share)
	}
}

func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := pathUUID(r, "shareId", "share")
	if err == nil {
		err = s.social.Delete(r.Context(), p.UserID, id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
