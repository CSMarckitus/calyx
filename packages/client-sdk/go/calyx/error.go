package calyx

import (
	"errors"
	"strconv"
	"time"
)

// Error is the one error type of the SDK. Use errors.As to read its fields.
type Error struct {
	// Source tells which side gave the error.
	Source Source
	// Code is the machine-readable reason.
	Code Code
	// Status is the HTTP status (backend), the gRPC code number (core) or 0 (client).
	Status int
	// Detail is the human-readable text from the server.
	Detail string
	// Retry is the retry advice of the call. AdviceFor knows only the source and the code, and
	// a create call can differ.
	Retry Retry
	// Errors holds the field errors of the backend (Problem.errors).
	Errors []FieldError
	// RequestID is the X-Request-Id of the backend. Other sources leave it empty.
	RequestID string
	// RetryAfter is the minimum wait: the Retry-After header of the backend, or the RetryInfo
	// detail of the core. It is 0 when there is none.
	RetryAfter time.Duration
	// IdempotencyKey is the key of the failed send. Only the send calls set it.
	IdempotencyKey string
	// Metadata is the ErrorInfo metadata of the core, for example "at".
	Metadata map[string]string
	// cause stays private, so that no text of it reaches Error().
	cause error
}

// FieldError is one error of one field of a backend request.
type FieldError struct {
	// Location tells which field failed.
	Location string
	// Message tells why the field failed.
	Message string
}

// Error gives the source, the code, the status and, when set, the detail, the request ID and
// the idempotency key. It never gives the cause, an API key or a core token.
func (e *Error) Error() string {
	s := "calyx " + string(e.Source) + " error " + string(e.Code) + " (status " + strconv.Itoa(e.Status) + ")"
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	if e.RequestID != "" {
		s += ", request " + e.RequestID
	}
	if e.IdempotencyKey != "" {
		s += ", idempotency key " + e.IdempotencyKey
	}
	return s
}

// Unwrap gives the cause, so that errors.Is finds context.Canceled and
// context.DeadlineExceeded.
func (e *Error) Unwrap() error { return e.cause }

// HasCode tells whether err, or an error that err wraps, is an *Error with this code.
func HasCode(err error, code Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}
