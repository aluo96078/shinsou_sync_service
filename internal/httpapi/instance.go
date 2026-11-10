package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
	"github.com/aluo96078/shinsou_sync_service/internal/config"
	"github.com/aluo96078/shinsou_sync_service/internal/instance"
)

type instanceInfo struct {
	Name              string  `json:"name"`
	Description       string  `json:"description,omitempty"`
	Version           string  `json:"version"`
	APIVersions       apiVers `json:"apiVersions"`
	Registration      string  `json:"registration"`
	EmailVerification string  `json:"emailVerification"`
	TermsURL          *string `json:"termsUrl"`
	PrivacyURL        *string `json:"privacyUrl"`
	Contact           *string `json:"contact"`
	SetupPending      bool    `json:"setupPending"`
	Features          struct {
		Sharing         bool `json:"sharing"`
		Reviews         bool `json:"reviews"`
		VisitorBrowsing bool `json:"visitorBrowsing"`
		Email           bool `json:"email"`
	} `json:"features"`
}

type apiVers struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *Server) handleInstance(w http.ResponseWriter, r *http.Request) {
	v := s.settings.Get()
	info := instanceInfo{
		Name:              v.Name,
		Description:       v.Description,
		Version:           s.version,
		APIVersions:       apiVers{Min: 1, Max: 1},
		Registration:      v.Registration,
		EmailVerification: v.EmailVerification,
		TermsURL:          nilIfEmpty(v.TermsURL),
		PrivacyURL:        nilIfEmpty(v.PrivacyURL),
		Contact:           nilIfEmpty(v.Contact),
	}
	info.Features.Email = s.mail.Enabled()
	info.Features.Sharing = v.Sharing == config.SharingEnabled
	info.Features.Reviews = v.Reviews == config.Enabled
	info.Features.VisitorBrowsing = info.Features.Reviews && v.VisitorBrowsing == config.Enabled
	has, err := instance.HasAdmin(r.Context(), s.pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	info.SetupPending = !has
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		s.log.Error("health check failed", "err", err)
		writeAPIError(w, apierr.New(http.StatusServiceUnavailable, "internal", "database is unreachable"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
