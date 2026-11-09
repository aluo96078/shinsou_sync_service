// Package apierr defines the error type shared by services and the HTTP layer. The JSON shape
// is the Error schema in api/openapi.yaml.
package apierr

import (
	"errors"
	"net/http"
)

// Error is a client-visible failure.
type Error struct {
	Status            int
	Code              string
	Message           string
	RetryAfterSeconds int
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New builds an Error.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// As extracts an *Error from err.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Constructors for the stable error codes.
func BadRequest(msg string) *Error { return New(http.StatusBadRequest, "bad_request", msg) }
func Unauthorized(msg string) *Error {
	return New(http.StatusUnauthorized, "unauthorized", msg)
}
func Forbidden(msg string) *Error { return New(http.StatusForbidden, "forbidden", msg) }
func NotFound(msg string) *Error  { return New(http.StatusNotFound, "not_found", msg) }
func Validation(msg string) *Error {
	return New(http.StatusUnprocessableEntity, "validation_failed", msg)
}
func Internal() *Error {
	return New(http.StatusInternalServerError, "internal", "internal server error")
}
