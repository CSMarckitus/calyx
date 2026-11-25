// Package apierr holds the one error type that services return and the HTTP layer maps 1:1 to a
// problem answer.
package apierr

import (
	"errors"
	"maps"
	"slices"
	"time"
)

// Error is returned by every service and mapped 1:1 to an HTTP problem answer.
type Error struct {
	Status int          // HTTP status
	Code   string       // a constant of codes.go
	Detail string       // English sentence for people; never secrets, texts or recipients
	Errors []FieldError // the wrong fields of an invalid request, else empty
	// RetryAfter is the wait the core asked for, else 0. The HTTP layer writes it as Retry-After.
	RetryAfter time.Duration
	cause      error // only set by Internal; logged by the HTTP layer, never shown
}

// FieldError names one wrong field. Field is a path in the request body, for example "text" or
// "messages[3].recipient". Message never holds the rejected value.
type FieldError struct {
	Field   string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Detail }

// Unwrap returns the cause of an Internal error, else nil.
func (e *Error) Unwrap() error { return e.cause }

func New(status int, code, detail string) *Error {
	return &Error{Status: status, Code: code, Detail: detail}
}

func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Internal returns a 500 error. The cause is kept for the log and never shown.
func Internal(err error) *Error {
	return &Error{Status: 500, Code: CodeInternal, Detail: "An internal error occurred.", cause: err}
}

func NotFound() *Error {
	return New(404, CodeNotFound, "The resource does not exist.")
}

func Invalid(detail string) *Error { return New(400, CodeInvalidRequest, detail) }

// InvalidFields returns a 400 error that lists every wrong field.
func InvalidFields(errs ...FieldError) *Error {
	e := Invalid("The request has invalid fields.")
	e.Errors = errs
	return e
}

func Conflict(code, detail string) *Error { return New(409, code, detail) }

func Forbidden(code, detail string) *Error { return New(403, code, detail) }

// StatusOf returns the HTTP status of a code of the errors table.
// It returns false for any other code, including the codes of the SSE "failed" event.
func StatusOf(code string) (status int, ok bool) {
	status, ok = statuses[code]
	return status, ok
}

// Codes lists the codes of the errors table, sorted. It does not include the pairing codes of the
// SSE "failed" event: see PairingFailureCodes.
func Codes() []string {
	return slices.Sorted(maps.Keys(statuses))
}

// PairingFailureCodes lists the codes of the SSE "failed" event of a pairing stream.
func PairingFailureCodes() []string {
	return []string{CodePairingExpired, CodePairingFailed, CodePairingCancelled}
}

var statuses = map[string]int{
	CodeInvalidRequest:             400,
	CodeUnauthenticated:            401,
	CodeCSRFCheckFailed:            403,
	CodeOwnerOnly:                  403,
	CodeInstanceCreationNotAllowed: 403,
	CodeWhatsAppWebNotAllowed:      403,
	CodeWhatsAppWebNotAcknowledged: 403,
	CodeAPIKeyNotAllowed:           403,
	CodeNotFound:                   404,
	CodeQuotaExceeded:              409,
	CodePhoneNumberIDInUse:         409,
	CodePairingInProgress:          409,
	CodePairingNotSupported:        409,
	CodeInstanceInError:            409,
	CodeInstanceConnected:          409,
	CodeInstanceNotConnected:       409,
	CodeInstanceNotReady:           409,
	CodeClientHasInstances:         409,
	CodeDefaultClient:              409,
	CodeEmailInUse:                 409,
	CodeConflict:                   409,
	CodeTokenInvalid:               410,
	CodeTooManyMessages:            413,
	CodeRateLimited:                429,
	CodeInstanceQueueFull:          429,
	CodeInternal:                   500,
	CodeCoreRejectedToken:          502,
	CodeCoreUnavailable:            503,
	CodeSendOutcomeUnknown:         504,
	CodeBulkOutcomeUnknown:         504,
	CodeChangeOutcomeUnknown:       504,

	CodeIdempotencyKeyConflict:       409,
	CodeSendInProgress:               409,
	CodeIdempotencyKeyOutcomeUnknown: 409,
	CodeDuplicateMessage:             409,
}
