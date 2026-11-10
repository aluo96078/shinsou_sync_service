package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
	"github.com/aluo96078/shinsou_sync_service/internal/reviews"
)

// reviewing is verified plus the reviews switch: every review endpoint answers 403
// feature_disabled while the operator has reviews turned off (docs/spec/reviews.md section 1).
func (s *Server) reviewing(h principalHandler) http.HandlerFunc {
	return s.verified(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if !s.reviews.Enabled() {
			writeAPIError(w, reviews.ErrDisabled())
			return
		}
		h(w, r, p)
	})
}

// reviewsPublic serves the read endpoints that visitors may use while the operator allows it.
// With a bearer token the caller is authenticated like everywhere else; without one the handler
// runs with the nil user id when visitor browsing is on and the request is refused otherwise.
func (s *Server) reviewsPublic(h func(http.ResponseWriter, *http.Request, uuid.UUID)) http.HandlerFunc {
	signedIn := s.reviewing(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) { h(w, r, p.UserID) })
	return func(w http.ResponseWriter, r *http.Request) {
		if bearerToken(r) != "" {
			signedIn(w, r)
			return
		}
		switch {
		case !s.reviews.Enabled():
			writeAPIError(w, reviews.ErrDisabled())
		case !s.reviews.VisitorBrowsing():
			writeAPIError(w, apierr.Unauthorized("missing bearer token"))
		default:
			h(w, r, uuid.Nil)
		}
	}
}

// moderator is authed plus a role check. Moderation stays available while reviews are switched
// off, so what is already there can still be dealt with.
func (s *Server) moderator(h principalHandler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if !reviews.CanModerate(p.Role) {
			writeAPIError(w, apierr.Forbidden("moderator access is required"))
			return
		}
		h(w, r, p)
	})
}

func (s *Server) handleWork(w http.ResponseWriter, r *http.Request, viewer uuid.UUID) {
	page, err := s.reviews.Work(r.Context(), viewer, r.PathValue("hash"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleWorkReviews(w http.ResponseWriter, r *http.Request, viewer uuid.UUID) {
	offset, limit, err := pageParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.reviews.List(r.Context(), viewer, r.PathValue("hash"), r.URL.Query().Get("sort"), offset, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handlePutReview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Rating  *int   `json:"rating"`
		Body    string `json:"body"`
		Spoiler bool   `json:"spoiler"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	review, created, err := s.reviews.Put(r.Context(), p.UserID, r.PathValue("hash"), in.Rating, in.Body, in.Spoiler)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, review)
}

func (s *Server) handleDeleteReview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.reviews.Delete(r.Context(), p.UserID, r.PathValue("hash")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMyReviews(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	offset, limit, err := pageParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.reviews.Mine(r.Context(), p.UserID, offset, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleHelpful(on bool) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		id, err := pathUUID(r, "reviewId", "review")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		vote, err := s.reviews.SetHelpful(r.Context(), p.UserID, id, on)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, vote)
	}
}

func (s *Server) handleReplies(w http.ResponseWriter, r *http.Request, viewer uuid.UUID) {
	id, err := pathUUID(r, "reviewId", "review")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	offset, limit, err := pageParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.reviews.Replies(r.Context(), viewer, id, offset, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleCreateReply(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := pathUUID(r, "reviewId", "review")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	reply, err := s.reviews.Reply(r.Context(), p.UserID, id, in.Body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, reply)
}

func (s *Server) handleDeleteReply(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := pathUUID(r, "replyId", "reply")
	if err == nil {
		err = s.reviews.DeleteReply(r.Context(), p.UserID, id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reviewTarget reads the review or reply id of a route that exists for both.
func reviewTarget(r *http.Request, reply bool) (uuid.UUID, error) {
	if reply {
		return pathUUID(r, "replyId", "reply")
	}
	return pathUUID(r, "reviewId", "review")
}

func (s *Server) handleReport(reply bool) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		id, err := reviewTarget(r, reply)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var in struct {
			Reason string `json:"reason"`
			Note   string `json:"note"`
		}
		if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		report, created, err := s.reviews.Report(r.Context(), p.UserID, id, reply, in.Reason, in.Note)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(w, status, report)
	}
}

func (s *Server) handleModReports(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	offset, limit, err := pageParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.reviews.Reports(r.Context(), p.UserID, r.URL.Query().Get("status"), offset, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleModResolve(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := pathUUID(r, "reportId", "report")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Action string `json:"action"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	resolution, err := s.reviews.Resolve(r.Context(), p.UserID, id, in.Action)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved", "resolution": resolution})
}

func (s *Server) handleModList(reply bool) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		offset, limit, err := pageParams(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var page any
		if reply {
			page, err = s.reviews.ModReplies(r.Context(), p.UserID, r.URL.Query().Get("status"), offset, limit)
		} else {
			page, err = s.reviews.ModReviews(r.Context(), p.UserID, r.URL.Query().Get("status"), offset, limit)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

// reasonBody reads the optional {"reason": "..."} body of a moderation action.
func (s *Server) reasonBody(w http.ResponseWriter, r *http.Request) (string, error) {
	var in struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength == 0 {
		return "", nil
	}
	err := s.decodeJSON(w, r, maxSmallBody, &in)
	return in.Reason, err
}

// handleModVisibility hides or unhides a review or a reply and answers with its new state.
func (s *Server) handleModVisibility(reply, hide bool) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		id, err := reviewTarget(r, reply)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if hide {
			var reason string
			if reason, err = s.reasonBody(w, r); err == nil {
				err = s.reviews.Hide(r.Context(), p.UserID, id, reply, reason)
			}
		} else {
			err = s.reviews.Unhide(r.Context(), p.UserID, id, reply)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var out any
		if reply {
			out, err = s.reviews.ModReply(r.Context(), p.UserID, id)
		} else {
			out, err = s.reviews.ModReview(r.Context(), p.UserID, id)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) handleModRemove(reply bool) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		id, err := reviewTarget(r, reply)
		if err == nil {
			var reason string
			if reason, err = s.reasonBody(w, r); err == nil {
				err = s.reviews.Remove(r.Context(), p.UserID, id, reply, reason)
			}
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleModSuspend(suspend bool) principalHandler {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		id, err := pathUUID(r, "userId", "user")
		if err == nil {
			var reason string
			if reason, err = s.reasonBody(w, r); err == nil {
				err = s.reviews.SetSuspended(r.Context(), p.UserID, p.Role, id, suspend, reason)
			}
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
