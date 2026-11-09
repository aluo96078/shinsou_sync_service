package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
)

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var in auth.RegisterInput
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.auth.Register(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in auth.LoginInput
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.auth.Login(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.RefreshToken == "" {
		s.fail(w, r, apierr.Validation("refreshToken is required"))
		return
	}
	t, err := s.auth.Refresh(r.Context(), in.RefreshToken)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.auth.Logout(r.Context(), p); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	u, err := s.auth.Me(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auth.ChangePassword(r.Context(), p, in.CurrentPassword, in.NewPassword); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	devs, err := s.auth.ListDevices(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

func (s *Server) handleRenameDevice(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := uuid.Parse(r.PathValue("deviceId"))
	if err != nil {
		s.fail(w, r, apierr.NotFound("device not found"))
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	d, err := s.auth.RenameDevice(r.Context(), p, id, in.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleSignOutDevice(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := uuid.Parse(r.PathValue("deviceId"))
	if err != nil {
		s.fail(w, r, apierr.NotFound("device not found"))
		return
	}
	if err := s.auth.SignOutDevice(r.Context(), p, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
