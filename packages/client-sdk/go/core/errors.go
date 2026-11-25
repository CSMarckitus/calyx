package core

import (
	"context"
	"errors"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/client-sdk/go/internal/errcause"
)

// reasonDomain is the ErrorInfo domain of the core reasons.
const reasonDomain = "calyx.core.v1"

// canonicalNames gives the canonical name of each gRPC code. codes.Code.String() writes
// "Canceled" and "DeadlineExceeded", so the SDK uses its own table.
var canonicalNames = map[codes.Code]calyx.Code{
	codes.Canceled:           calyx.CodeGRPCCancelled,
	codes.Unknown:            calyx.CodeGRPCUnknown,
	codes.InvalidArgument:    calyx.CodeGRPCInvalidArgument,
	codes.DeadlineExceeded:   calyx.CodeGRPCDeadlineExceeded,
	codes.NotFound:           calyx.CodeGRPCNotFound,
	codes.AlreadyExists:      calyx.CodeGRPCAlreadyExists,
	codes.PermissionDenied:   calyx.CodeGRPCPermissionDenied,
	codes.ResourceExhausted:  calyx.CodeGRPCResourceExhausted,
	codes.FailedPrecondition: calyx.CodeGRPCFailedPrecondition,
	codes.Aborted:            calyx.CodeGRPCAborted,
	codes.OutOfRange:         calyx.CodeGRPCOutOfRange,
	codes.Unimplemented:      calyx.CodeGRPCUnimplemented,
	codes.Internal:           calyx.CodeGRPCInternal,
	codes.Unavailable:        calyx.CodeGRPCUnavailable,
	codes.DataLoss:           calyx.CodeGRPCDataLoss,
	codes.Unauthenticated:    calyx.CodeGRPCUnauthenticated,
}

// convertError converts an error of a call on ctx to a *calyx.Error that carries idemKey.
// When ctx ended, the code is aborted or timeout. Otherwise it is the core reason of an
// ErrorInfo with the domain calyx.core.v1, or the canonical name of the gRPC code.
func convertError(ctx context.Context, err error, idemKey string) *calyx.Error {
	switch ctxErr := ctx.Err(); {
	case errors.Is(ctxErr, context.Canceled):
		return clientError(calyx.CodeAborted, "The program canceled the call.", idemKey, ctxErr)
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return clientError(calyx.CodeTimeout, "The core did not answer in time.", idemKey, ctxErr)
	}

	st := status.Convert(err)
	code, ok := canonicalNames[st.Code()]
	if !ok {
		// A code outside 1 to 16 is not valid gRPC; UNKNOWN is the gRPC rule for it.
		code = calyx.CodeGRPCUnknown
	}
	var metadata map[string]string
	var retryAfter time.Duration
	haveInfo := false
	for _, d := range st.Details() {
		switch detail := d.(type) {
		case *errdetails.ErrorInfo:
			// The first ErrorInfo of the domain wins.
			if haveInfo || detail.GetDomain() != reasonDomain {
				continue
			}
			haveInfo = true
			// An empty reason keeps the canonical name.
			if detail.GetReason() != "" {
				code = calyx.Code(detail.GetReason())
			}
			metadata = detail.GetMetadata()
		case *errdetails.RetryInfo:
			// Round up to whole seconds, so the program never waits less than the core asks.
			delay := detail.GetRetryDelay().AsDuration()
			retryAfter = (delay + time.Second - 1).Truncate(time.Second)
			retryAfter = max(retryAfter, 0)
			// A negative delay means no wait, as in the TypeScript SDK.
		}
	}
	e := &calyx.Error{
		Source:         calyx.SourceCore,
		Code:           code,
		Status:         int(st.Code()),
		Detail:         st.Message(),
		Retry:          calyx.AdviceFor(calyx.SourceCore, code),
		RetryAfter:     retryAfter,
		IdempotencyKey: idemKey,
		Metadata:       metadata,
	}
	errcause.Set(e, err)
	return e
}

// closedError is the error of a call after Close.
func closedError(idemKey string) *calyx.Error {
	return clientError(calyx.CodeAborted, "The client is closed.", idemKey, nil)
}

// clientError builds an error that the SDK itself found. The cause never reaches Error().
func clientError(code calyx.Code, detail, idemKey string, cause error) *calyx.Error {
	e := &calyx.Error{
		Source:         calyx.SourceClient,
		Code:           code,
		Detail:         detail,
		Retry:          calyx.AdviceFor(calyx.SourceClient, code),
		IdempotencyKey: idemKey,
	}
	if cause != nil {
		errcause.Set(e, cause)
	}
	return e
}
