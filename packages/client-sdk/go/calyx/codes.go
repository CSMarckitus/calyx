package calyx

// Source tells which side gave an error.
type Source string

// The sources of an Error.
const (
	// SourceBackend marks an error from the backend HTTP API.
	SourceBackend Source = "backend"
	// SourceCore marks an error from the core gRPC API.
	SourceCore Source = "core"
	// SourceClient marks an error that the SDK itself found.
	SourceClient Source = "client"
)

// Retry tells the program what to do after an error. The SDK never acts on it.
type Retry string

// The retry advice values.
const (
	// RetryNow means: send the same request again now, with the same idempotency key.
	RetryNow Retry = "retry_now"
	// RetryLater means: wait, then send the same request with the same idempotency key.
	RetryLater Retry = "retry_later"
	// RetryCheckIfSent means: check the chat first. Send again only if the message is missing,
	// with a new idempotency key, after 10 minutes.
	RetryCheckIfSent Retry = "check_if_sent"
	// RetryDoNotRetry means: change the request or the setup first.
	RetryDoNotRetry Retry = "do_not_retry"
	// RetryUnknown means: the SDK has no advice.
	RetryUnknown Retry = "unknown"
)

// Code is the machine-readable reason of an error. The type is open: a newer server can send
// a value that these constants do not list.
type Code string

// Codes of the backend. They equal the Problem.code enum of the OpenAPI document.
const (
	CodeSendOutcomeUnknown           Code = "send_outcome_unknown"
	CodeBulkOutcomeUnknown           Code = "bulk_outcome_unknown"
	CodeChangeOutcomeUnknown         Code = "change_outcome_unknown"
	CodeSendInProgress               Code = "send_in_progress"
	CodeInstanceQueueFull            Code = "instance_queue_full"
	CodeAPIKeyNotAllowed             Code = "api_key_not_allowed"
	CodeRateLimited                  Code = "rate_limited"
	CodeInstanceNotConnected         Code = "instance_not_connected"
	CodePairingInProgress            Code = "pairing_in_progress"
	CodeCoreUnavailable              Code = "core_unavailable"
	CodeIdempotencyKeyOutcomeUnknown Code = "idempotency_key_outcome_unknown"
	CodeDuplicateMessage             Code = "duplicate_message"
	CodeIdempotencyKeyConflict       Code = "idempotency_key_conflict"
	CodeInvalidRequest               Code = "invalid_request"
	CodeUnauthenticated              Code = "unauthenticated"
	CodeCSRFCheckFailed              Code = "csrf_check_failed"
	CodeOwnerOnly                    Code = "owner_only"
	CodeInstanceCreationNotAllowed   Code = "instance_creation_not_allowed"
	CodeWhatsAppWebNotAllowed        Code = "whatsapp_web_not_allowed"
	CodeWhatsAppWebNotAcknowledged   Code = "whatsapp_web_not_acknowledged"
	CodeNotFound                     Code = "not_found"
	CodeQuotaExceeded                Code = "quota_exceeded"
	CodePhoneNumberIDInUse           Code = "phone_number_id_in_use"
	CodePairingNotSupported          Code = "pairing_not_supported"
	CodeInstanceInError              Code = "instance_in_error"
	CodeInstanceConnected            Code = "instance_connected"
	CodeInstanceNotReady             Code = "instance_not_ready"
	CodeClientHasInstances           Code = "client_has_instances"
	CodeDefaultClient                Code = "default_client"
	CodeEmailInUse                   Code = "email_in_use"
	CodeTokenInvalid                 Code = "token_invalid"
	CodeTooManyMessages              Code = "too_many_messages"
	CodeCoreRejectedToken            Code = "core_rejected_token"
	CodeInternal                     Code = "internal"
	CodeConflict                     Code = "conflict"
)

// The reasons of the core. They equal the ErrorReason enum of the proto files.
const (
	CodeErrorReasonSendInProgress         Code = "ERROR_REASON_SEND_IN_PROGRESS"
	CodeErrorReasonSendOutcomeUnknown     Code = "ERROR_REASON_SEND_OUTCOME_UNKNOWN"
	CodeErrorReasonDuplicateMessage       Code = "ERROR_REASON_DUPLICATE_MESSAGE"
	CodeErrorReasonIdempotencyKeyConflict Code = "ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT"
	CodeErrorReasonInstanceQueueFull      Code = "ERROR_REASON_INSTANCE_QUEUE_FULL"
)

// Canonical gRPC status names, without OK. The core sends them when it gives no reason.
const (
	CodeGRPCUnavailable        Code = "UNAVAILABLE"
	CodeGRPCDeadlineExceeded   Code = "DEADLINE_EXCEEDED"
	CodeGRPCCancelled          Code = "CANCELLED"
	CodeGRPCResourceExhausted  Code = "RESOURCE_EXHAUSTED"
	CodeGRPCInvalidArgument    Code = "INVALID_ARGUMENT"
	CodeGRPCNotFound           Code = "NOT_FOUND"
	CodeGRPCAlreadyExists      Code = "ALREADY_EXISTS"
	CodeGRPCPermissionDenied   Code = "PERMISSION_DENIED"
	CodeGRPCFailedPrecondition Code = "FAILED_PRECONDITION"
	CodeGRPCOutOfRange         Code = "OUT_OF_RANGE"
	CodeGRPCUnimplemented      Code = "UNIMPLEMENTED"
	CodeGRPCUnauthenticated    Code = "UNAUTHENTICATED"
	CodeGRPCUnknown            Code = "UNKNOWN"
	CodeGRPCAborted            Code = "ABORTED"
	CodeGRPCInternal           Code = "INTERNAL"
	CodeGRPCDataLoss           Code = "DATA_LOSS"
)

// Codes that the SDK itself gives.
const (
	CodeTimeout          Code = "timeout"
	CodeNetworkError     Code = "network_error"
	CodeInvalidResponse  Code = "invalid_response"
	CodeAborted          Code = "aborted"
	CodeStreamFailed     Code = "stream_failed"
	CodeInvalidSignature Code = "invalid_signature"
	CodeStaleTimestamp   Code = "stale_timestamp"
	CodeInvalidEvent     Code = "invalid_event"
	CodeInvalidConfig    Code = "invalid_config"
)
