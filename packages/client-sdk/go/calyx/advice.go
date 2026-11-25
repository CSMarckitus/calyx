package calyx

// advice is the retry advice of every known code. A test compares it with
// packages/client-sdk/testdata/retry-advice.json in both directions.
// Core errors use two kinds of key: the ErrorReason name when the core sends one, and the
// gRPC status name otherwise.
var advice = map[Source]map[Code]Retry{
	SourceBackend: {
		CodeSendOutcomeUnknown:           RetryNow,
		CodeBulkOutcomeUnknown:           RetryNow,
		CodeChangeOutcomeUnknown:         RetryNow,
		CodeSendInProgress:               RetryLater,
		CodeInstanceQueueFull:            RetryLater,
		CodeRateLimited:                  RetryLater,
		CodeInstanceNotConnected:         RetryLater,
		CodePairingInProgress:            RetryLater,
		CodeCoreUnavailable:              RetryLater,
		CodeIdempotencyKeyOutcomeUnknown: RetryCheckIfSent,
		CodeDuplicateMessage:             RetryDoNotRetry,
		CodeIdempotencyKeyConflict:       RetryDoNotRetry,
		CodeInvalidRequest:               RetryDoNotRetry,
		CodeUnauthenticated:              RetryDoNotRetry,
		CodeCSRFCheckFailed:              RetryDoNotRetry,
		CodeAPIKeyNotAllowed:             RetryDoNotRetry,
		CodeOwnerOnly:                    RetryDoNotRetry,
		CodeInstanceCreationNotAllowed:   RetryDoNotRetry,
		CodeWhatsAppWebNotAllowed:        RetryDoNotRetry,
		CodeWhatsAppWebNotAcknowledged:   RetryDoNotRetry,
		CodeNotFound:                     RetryDoNotRetry,
		CodeQuotaExceeded:                RetryDoNotRetry,
		CodePhoneNumberIDInUse:           RetryDoNotRetry,
		CodePairingNotSupported:          RetryDoNotRetry,
		CodeInstanceInError:              RetryDoNotRetry,
		CodeInstanceConnected:            RetryDoNotRetry,
		CodeInstanceNotReady:             RetryDoNotRetry,
		CodeClientHasInstances:           RetryDoNotRetry,
		CodeDefaultClient:                RetryDoNotRetry,
		CodeEmailInUse:                   RetryDoNotRetry,
		CodeTokenInvalid:                 RetryDoNotRetry,
		CodeTooManyMessages:              RetryDoNotRetry,
		CodeCoreRejectedToken:            RetryDoNotRetry,
		CodeInternal:                     RetryUnknown,
		CodeConflict:                     RetryUnknown,
	},
	SourceCore: {
		CodeGRPCUnavailable:                   RetryNow,
		CodeGRPCDeadlineExceeded:              RetryNow,
		CodeGRPCCancelled:                     RetryNow,
		CodeErrorReasonSendInProgress:         RetryLater,
		CodeErrorReasonInstanceQueueFull:      RetryLater,
		CodeGRPCResourceExhausted:             RetryLater,
		CodeErrorReasonSendOutcomeUnknown:     RetryCheckIfSent,
		CodeErrorReasonDuplicateMessage:       RetryDoNotRetry,
		CodeErrorReasonIdempotencyKeyConflict: RetryDoNotRetry,
		CodeGRPCInvalidArgument:               RetryDoNotRetry,
		CodeGRPCNotFound:                      RetryDoNotRetry,
		CodeGRPCAlreadyExists:                 RetryDoNotRetry,
		CodeGRPCPermissionDenied:              RetryDoNotRetry,
		CodeGRPCFailedPrecondition:            RetryDoNotRetry,
		CodeGRPCOutOfRange:                    RetryDoNotRetry,
		CodeGRPCUnimplemented:                 RetryDoNotRetry,
		CodeGRPCUnauthenticated:               RetryDoNotRetry,
		CodeGRPCUnknown:                       RetryUnknown,
		CodeGRPCAborted:                       RetryUnknown,
		CodeGRPCInternal:                      RetryUnknown,
		CodeGRPCDataLoss:                      RetryUnknown,
	},
	SourceClient: {
		CodeTimeout:          RetryNow,
		CodeNetworkError:     RetryNow,
		CodeInvalidResponse:  RetryNow,
		CodeAborted:          RetryNow,
		CodeStreamFailed:     RetryNow,
		CodeInvalidSignature: RetryDoNotRetry,
		CodeStaleTimestamp:   RetryDoNotRetry,
		CodeInvalidEvent:     RetryDoNotRetry,
		CodeInvalidConfig:    RetryDoNotRetry,
	},
}

// AdviceFor gives the retry advice for a code of a source. An unknown code gives RetryUnknown.
// It knows only the source and the code. The advice of a call is in Error.Retry.
func AdviceFor(source Source, code Code) Retry {
	if r, ok := advice[source][code]; ok {
		return r
	}
	return RetryUnknown
}
