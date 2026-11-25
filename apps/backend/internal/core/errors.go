// Package core is the only backend package that talks to the Calyx core over gRPC.
package core

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"calyx/backend/internal/apierr"
	corev1 "github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// callKind tells mapError which column of the error table applies.
type callKind int

const (
	callRead   callKind = iota // ListInstances, GetInstanceStatus, GetBulkJob, GetBulkJobs, CancelBulkJob
	callChange                 // UpdateInstanceConfig
	callRemove                 // RemoveInstance
	callCreate                 // CreateInstance (stream)
	callBulk                   // SendBulkSameMessage, SendBulkCustomMessages
	callSend                   // SendPlainText
)

// ErrPairingExpired is wrapped by the error of CreateInstance when the core answers
// DEADLINE_EXCEEDED: the codes of the pairing ended. Callers test it with errors.Is.
var ErrPairingExpired = errors.New("the pairing codes ended")

var quoted = regexp.MustCompile(`'[^']*'`)

// redactQuoted replaces every single-quoted part of a core message with '<hidden>'.
// The core can quote a recipient in a refusal, and details never hold recipients.
func redactQuoted(message string) string {
	return quoted.ReplaceAllString(message, "'<hidden>'")
}

// invalidArgument returns a fixed error for an INVALID_ARGUMENT of the core. The core's message
// can quote a recipient or a text, so it reaches neither the answer nor the log.
func invalidArgument(kind callKind) error {
	switch kind {
	case callSend:
		// The backend checks the text first, so the core refused the recipient.
		return apierr.InvalidFields(apierr.FieldError{Field: "to",
			Message: "must be a phone number with country code, or a WhatsApp JID (…@s.whatsapp.net, …@lid, …@g.us)."})
	case callBulk:
		return apierr.Invalid("The core refused a recipient or a pacing value of the bulk job.")
	}
	return apierr.Invalid("The core refused a value of the request.")
}

// sendError is the error of SendText. It keeps the raw gRPC code, because the mapped error cannot
// tell a refusal of an instance that is not connected from a failed send.
type sendError struct {
	mapped *apierr.Error // a named field: an embedded *apierr.Error would clash with Error()
	raw    codes.Code
}

func (e sendError) Error() string { return e.mapped.Error() }

func (e sendError) Unwrap() error { return e.mapped }

// IsUnavailable reports whether err is the error of a send for which the core answered UNAVAILABLE.
func IsUnavailable(err error) bool {
	var se sendError
	return errors.As(err, &se) && se.raw == codes.Unavailable
}

// Loggable returns what the log may hold of an error. A message of the core can hold a recipient
// or a text, so a gRPC status is reduced to its code.
func Loggable(err error) any {
	if st, ok := status.FromError(err); ok && st.Code() != codes.OK {
		return "core call failed: " + st.Code().String()
	}
	if st, ok := status.FromError(errors.Unwrap(err)); ok && st.Code() != codes.OK {
		return "core call failed: " + st.Code().String()
	}
	return err
}

// reasonOf returns the reason and the metadata of the first ErrorInfo detail of the core.
// Without such a detail it returns an empty reason.
func reasonOf(err error) (reason string, meta map[string]string) {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == "calyx.core.v1" {
			return info.GetReason(), info.GetMetadata()
		}
	}
	return "", nil
}

// retryDelayOf returns the wait of the RetryInfo detail of the core, or 0 without one.
func retryDelayOf(err error) time.Duration {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.RetryInfo); ok {
			return info.GetRetryDelay().AsDuration()
		}
	}
	return 0
}

// withWait sets the wait of the core on ae and adds it to the detail. An older core sends no
// RetryInfo: then ae stays as it is.
func withWait(ae *apierr.Error, err error) *apierr.Error {
	wait := retryDelayOf(err)
	if wait <= 0 {
		return ae
	}
	ae.RetryAfter = wait
	ae.Detail += fmt.Sprintf(" Retry with the same idempotency key in %d seconds.", retryAfterSeconds(wait))
	return ae
}

// retryAfterSeconds rounds a wait up to whole seconds.
func retryAfterSeconds(d time.Duration) int {
	return int((d + time.Second - 1) / time.Second)
}

// reasonRow maps a pair of gRPC code and core reason to a 409 problem code.
func reasonRow(code codes.Code, reason string) (string, bool) {
	switch {
	case code == codes.AlreadyExists && reason == corev1.ErrorReason_ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT.String():
		return apierr.CodeIdempotencyKeyConflict, true
	case code == codes.Aborted && reason == corev1.ErrorReason_ERROR_REASON_SEND_IN_PROGRESS.String():
		return apierr.CodeSendInProgress, true
	case code == codes.AlreadyExists && reason == corev1.ErrorReason_ERROR_REASON_SEND_OUTCOME_UNKNOWN.String():
		return apierr.CodeIdempotencyKeyOutcomeUnknown, true
	case code == codes.AlreadyExists && reason == corev1.ErrorReason_ERROR_REASON_DUPLICATE_MESSAGE.String():
		return apierr.CodeDuplicateMessage, true
	}
	return "", false
}

// mapError turns a gRPC error into an *apierr.Error.
// It returns an untyped nil for a nil error. method is only used for the log.
func mapError(kind callKind, method string, err error) error {
	if err == nil {
		return nil
	}
	code := status.Code(err)
	msg := status.Convert(err).Message()
	if kind == callSend || kind == callBulk {
		msg = redactQuoted(msg)
	}
	unknown := apierr.New(504, apierr.CodeSendOutcomeUnknown,
		"The core did not confirm the send. The message may have been sent. "+
			"If you sent an Idempotency-Key, retry with the same key. Without a key, do not retry automatically.")
	if kind == callBulk {
		unknown = apierr.New(504, apierr.CodeBulkOutcomeUnknown,
			"The core did not confirm the bulk job. It may be running. "+
				"If you sent an Idempotency-Key, retry with the same key. Without a key, do not start it again automatically.")
	}
	sendLike := kind == callSend || kind == callBulk
	if sendLike {
		reason, meta := reasonOf(err)
		if problem, ok := reasonRow(code, reason); ok {
			if problem == apierr.CodeDuplicateMessage && meta["message_id"] != "" {
				msg += " Earlier message id: " + meta["message_id"] + "."
			}
			ae := apierr.Conflict(problem, msg)
			if problem == apierr.CodeSendInProgress {
				ae = withWait(ae, err)
			}
			return ae
		}
	}

	switch code {
	case codes.InvalidArgument:
		return invalidArgument(kind)
	case codes.NotFound:
		if kind == callRemove {
			return nil
		}
		return apierr.NotFound()
	case codes.AlreadyExists:
		if sendLike {
			return unknown
		}
		return apierr.Conflict(apierr.CodeConflict, msg)
	case codes.FailedPrecondition:
		if sendLike {
			return apierr.Conflict(apierr.CodeInstanceNotReady, "The instance is disabled or must be paired again.")
		}
		return apierr.Conflict(apierr.CodeConflict, msg)
	case codes.ResourceExhausted:
		// Without the reason the answer is not known to be a full queue: fall through.
		if reason, _ := reasonOf(err); reason == corev1.ErrorReason_ERROR_REASON_INSTANCE_QUEUE_FULL.String() {
			return withWait(apierr.New(429, apierr.CodeInstanceQueueFull, msg), err)
		}
	case codes.Unauthenticated:
		slog.Default().Error("The core refused CORE_TOKEN.", "method", method)
		return apierr.New(502, apierr.CodeCoreRejectedToken, "The core refused CORE_TOKEN.")
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		switch kind {
		case callChange:
			return apierr.New(504, apierr.CodeChangeOutcomeUnknown,
				"The core did not confirm the change. Nothing was saved; send the change again.")
		case callCreate:
			return apierr.New(503, apierr.CodeCoreUnavailable, "The pairing failed. The core did not confirm it. Try again.")
		case callBulk, callSend:
			return unknown
		}
		return apierr.New(503, apierr.CodeCoreUnavailable, "The core could not be reached. Try again.")
	}
	if sendLike {
		return unknown
	}
	return apierr.Internal(err)
}

// createError maps an error of CreateInstance. DEADLINE_EXCEEDED also wraps ErrPairingExpired,
// and the mapped error stays the same.
func createError(err error) error {
	mapped := mapError(callCreate, methodCreate, err)
	if status.Code(err) == codes.DeadlineExceeded {
		return fmt.Errorf("%w: %w", ErrPairingExpired, mapped)
	}
	return mapped
}
