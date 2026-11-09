package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/aluo96078/shinsou_sync_service/internal/apierr"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	RetryAfterSeconds int    `json:"retryAfterSeconds,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAPIError(w http.ResponseWriter, e *apierr.Error) {
	if e.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfterSeconds))
	}
	if e.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	writeJSON(w, e.Status, errorBody{Error: errorDetail{Code: e.Code, Message: e.Message, RetryAfterSeconds: e.RetryAfterSeconds}})
}

// fail writes err as an Error response; unexpected errors become a logged 500.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apierr.As(err); ok {
		writeAPIError(w, e)
		return
	}
	if errors.Is(err, r.Context().Err()) && r.Context().Err() != nil {
		// client went away; nothing useful to send
		writeAPIError(w, apierr.New(499, "bad_request", "request cancelled"))
		return
	}
	s.log.Error("request failed", slog.String("path", r.URL.Path), slog.Any("err", err), slog.String("request_id", requestID(r)))
	writeAPIError(w, apierr.Internal())
}

// decodeJSON reads a size-limited JSON body into dst.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) error {
	body := http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return apierr.New(http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
		}
		return apierr.BadRequest("request body is not valid JSON of the expected shape")
	}
	return nil
}
