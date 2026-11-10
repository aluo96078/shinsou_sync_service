package httpapi

import (
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/aluo96078/shinsou_sync_service/internal/admin"
	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
	"github.com/aluo96078/shinsou_sync_service/internal/settings"
)

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	o, err := s.admin.Overview(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	offset, limit, err := pageParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	q := r.URL.Query().Get("q")
	if utf8.RuneCountInString(q) > 200 {
		s.fail(w, r, apierr.BadRequest("q must be at most 200 characters"))
		return
	}
	page, err := s.admin.ListUsers(r.Context(), q, offset, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func pathUUID(r *http.Request, name, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, apierr.NotFound(what + " not found")
	}
	return id, nil
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := pathUUID(r, "userId", "user")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var patch admin.UserPatch
	if err := s.decodeJSON(w, r, maxSmallBody, &patch); err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.admin.UpdateUser(r.Context(), p.UserID, id, patch)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := pathUUID(r, "userId", "user")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.admin.DeleteUser(r.Context(), p.UserID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminSignOutUser(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := pathUUID(r, "userId", "user")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.admin.SignOutUser(r.Context(), p.UserID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminListInvites(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	list, err := s.admin.ListInvites(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": list})
}

func (s *Server) handleAdminCreateInvite(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in admin.InviteInput
	if r.ContentLength != 0 {
		if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	inv, code, err := s.admin.CreateInvite(r.Context(), p.UserID, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invite": inv, "code": code})
}

func (s *Server) handleAdminDeleteInvite(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := pathUUID(r, "inviteId", "invite")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.admin.DeleteInvite(r.Context(), p.UserID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type settingsValues struct {
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	Registration      string  `json:"registration"`
	EmailVerification string  `json:"emailVerification"`
	Sharing           string  `json:"sharing"`
	TermsURL          *string `json:"termsUrl"`
	PrivacyURL        *string `json:"privacyUrl"`
	Contact           *string `json:"contact"`
}

func toSettingsValues(v settings.Values) settingsValues {
	return settingsValues{Name: v.Name, Description: v.Description, Registration: v.Registration, EmailVerification: v.EmailVerification,
		Sharing: v.Sharing, TermsURL: nilIfEmpty(v.TermsURL), PrivacyURL: nilIfEmpty(v.PrivacyURL), Contact: nilIfEmpty(v.Contact)}
}

func (s *Server) writeSettings(w http.ResponseWriter) {
	over := s.settings.Overridden()
	if over == nil {
		over = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"values":         toSettingsValues(s.settings.Get()),
		"defaults":       toSettingsValues(s.settings.Defaults()),
		"overridden":     over,
		"emailAvailable": s.settings.EmailAvailable(),
	})
}

func (s *Server) handleAdminGetSettings(w http.ResponseWriter, _ *http.Request, _ *auth.Principal) {
	s.writeSettings(w)
}

func (s *Server) handleAdminUpdateSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var patch map[string]json.RawMessage
	if err := s.decodeJSON(w, r, maxSmallBody, &patch); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.settings.Patch(r.Context(), p.UserID, patch); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeSettings(w)
}

func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request, _ *auth.Principal) {
	offset, limit, err := pageParams(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.admin.Audit(r.Context(), offset, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
