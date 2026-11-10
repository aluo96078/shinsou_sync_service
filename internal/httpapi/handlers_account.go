package httpapi

import (
	"io"
	"net/http"
	"time"

	"github.com/aluo96078/shinsou_sync_service/internal/account"
	"github.com/aluo96078/shinsou_sync_service/internal/auth"
)

func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auth.VerifyEmail(r.Context(), in.Token); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auth.ForgotPassword(r.Context(), in.Email); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auth.ResetPassword(r.Context(), in.Token, in.NewPassword); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleChangeEmail(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Email           *string `json:"email"`
		CurrentPassword string  `json:"currentPassword"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.auth.ChangeEmail(r.Context(), p, in.Email, in.CurrentPassword)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleResendVerification(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.auth.ResendVerification(r.Context(), p); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	u, err := s.account.Usage(r.Context(), p.UserID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	started := false
	err := s.account.Export(r.Context(), p, account.ExportMeta{Name: s.settings.Get().Name, URL: s.cfg.PublicURL},
		func(username string) io.Writer {
			started = true
			h := w.Header()
			h.Set("Content-Type", "application/json")
			h.Set("Content-Disposition", `attachment; filename="shinsou-export-`+username+`-`+time.Now().UTC().Format("20060102")+`.json"`)
			w.WriteHeader(http.StatusOK)
			return w
		})
	if err == nil {
		return
	}
	if !started {
		s.fail(w, r, err)
		return
	}
	// The status line is gone. Abort the connection so the client sees a failed download
	// instead of a truncated document that looks complete.
	s.log.Error("export failed midway", "err", err, "request_id", requestID(r))
	panic(http.ErrAbortHandler)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		CurrentPassword string `json:"currentPassword"`
	}
	if err := s.decodeJSON(w, r, maxSmallBody, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.auth.DeleteAccount(r.Context(), p, in.CurrentPassword); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
