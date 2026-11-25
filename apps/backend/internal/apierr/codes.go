package apierr

// The error codes of the API.
const (
	CodeInvalidRequest             = "invalid_request"               // 400
	CodeUnauthenticated            = "unauthenticated"               // 401
	CodeCSRFCheckFailed            = "csrf_check_failed"             // 403
	CodeOwnerOnly                  = "owner_only"                    // 403
	CodeInstanceCreationNotAllowed = "instance_creation_not_allowed" // 403
	CodeWhatsAppWebNotAllowed      = "whatsapp_web_not_allowed"      // 403
	CodeWhatsAppWebNotAcknowledged = "whatsapp_web_not_acknowledged" // 403
	CodeAPIKeyNotAllowed           = "api_key_not_allowed"           // 403
	CodeNotFound                   = "not_found"                     // 404
	CodeQuotaExceeded              = "quota_exceeded"                // 409
	CodePhoneNumberIDInUse         = "phone_number_id_in_use"        // 409
	CodePairingInProgress          = "pairing_in_progress"           // 409
	CodePairingNotSupported        = "pairing_not_supported"         // 409
	CodeInstanceInError            = "instance_in_error"             // 409
	CodeInstanceConnected          = "instance_connected"            // 409
	CodeInstanceNotConnected       = "instance_not_connected"        // 409
	CodeInstanceNotReady           = "instance_not_ready"            // 409
	CodeClientHasInstances         = "client_has_instances"          // 409
	CodeDefaultClient              = "default_client"                // 409
	CodeEmailInUse                 = "email_in_use"                  // 409
	CodeConflict                   = "conflict"                      // 409
	CodeTokenInvalid               = "token_invalid"                 // 410
	CodeTooManyMessages            = "too_many_messages"             // 413
	CodeRateLimited                = "rate_limited"                  // 429
	CodeInstanceQueueFull          = "instance_queue_full"           // 429
	CodeInternal                   = "internal"                      // 500
	CodeCoreRejectedToken          = "core_rejected_token"           // 502
	CodeCoreUnavailable            = "core_unavailable"              // 503
	CodeSendOutcomeUnknown         = "send_outcome_unknown"          // 504
	CodeBulkOutcomeUnknown         = "bulk_outcome_unknown"          // 504
	CodeChangeOutcomeUnknown       = "change_outcome_unknown"        // 504

	// Core reasons of a send or a bulk create, read from the ErrorInfo detail.
	CodeIdempotencyKeyConflict       = "idempotency_key_conflict"        // 409
	CodeSendInProgress               = "send_in_progress"                // 409
	CodeIdempotencyKeyOutcomeUnknown = "idempotency_key_outcome_unknown" // 409
	CodeDuplicateMessage             = "duplicate_message"               // 409
)

// Codes of the SSE "failed" event of a pairing session. They are not HTTP errors.
const (
	CodePairingExpired   = "pairing_expired"
	CodePairingFailed    = "pairing_failed"
	CodePairingCancelled = "pairing_cancelled"
)
