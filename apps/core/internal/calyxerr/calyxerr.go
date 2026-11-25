// Package calyxerr holds the errors that carry a gRPC status code. The gRPC layer turns them into
// statuses once; any other error becomes Internal there.
package calyxerr

import (
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

type Error struct {
	Code    codes.Code
	Message string
	// Reason and Metadata are optional. When Reason is set, the gRPC layer sends them as a
	// google.rpc.ErrorInfo detail, so clients can branch on a stable name.
	Reason   corev1.ErrorReason
	Metadata map[string]string
	// RetryAfter is how long the client should wait before it tries again. Zero means no advice.
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Message }

func New(code codes.Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithReason is like New, with a machine-readable reason and optional metadata.
func WithReason(code codes.Code, reason corev1.ErrorReason, metadata map[string]string, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Reason: reason, Metadata: metadata}
}

// WithRetryAfter sets the wait the client should observe and returns the same error.
func (e *Error) WithRetryAfter(d time.Duration) *Error {
	e.RetryAfter = d
	return e
}

// CodeOf returns the code of the first *Error in err's chain, or codes.Unknown when there is none.
func CodeOf(err error) codes.Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return codes.Unknown
}
