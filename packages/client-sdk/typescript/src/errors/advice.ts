import type { BackendErrorCode, ClientErrorCode, CoreErrorCode } from "./codes.js";

// What the program does for each value. The SDK never acts on it.
// retry_now: send the same request again now, with the same idempotency key.
// retry_later: wait, then send the same request with the same idempotency key.
// check_if_sent: check the chat first. Send again only if the message is missing, with a new idempotency key, after 10 minutes.
// do_not_retry: change the request or the setup first.
// unknown: the SDK has no advice.
export type RetryAdvice =
  | "retry_now"
  | "retry_later"
  | "check_if_sent"
  | "do_not_retry"
  | "unknown";

// Each map is exhaustive: tsc fails when a contract adds a code without a row.
export const backendAdvice: Record<BackendErrorCode, RetryAdvice> = {
  send_outcome_unknown: "retry_now",
  bulk_outcome_unknown: "retry_now",
  change_outcome_unknown: "retry_now",
  send_in_progress: "retry_later",
  instance_queue_full: "retry_later",
  rate_limited: "retry_later",
  instance_not_connected: "retry_later",
  pairing_in_progress: "retry_later",
  core_unavailable: "retry_later",
  idempotency_key_outcome_unknown: "check_if_sent",
  duplicate_message: "do_not_retry",
  idempotency_key_conflict: "do_not_retry",
  invalid_request: "do_not_retry",
  unauthenticated: "do_not_retry",
  csrf_check_failed: "do_not_retry",
  owner_only: "do_not_retry",
  api_key_not_allowed: "do_not_retry",
  instance_creation_not_allowed: "do_not_retry",
  whatsapp_web_not_allowed: "do_not_retry",
  whatsapp_web_not_acknowledged: "do_not_retry",
  not_found: "do_not_retry",
  quota_exceeded: "do_not_retry",
  phone_number_id_in_use: "do_not_retry",
  pairing_not_supported: "do_not_retry",
  instance_in_error: "do_not_retry",
  instance_connected: "do_not_retry",
  instance_not_ready: "do_not_retry",
  client_has_instances: "do_not_retry",
  default_client: "do_not_retry",
  email_in_use: "do_not_retry",
  token_invalid: "do_not_retry",
  too_many_messages: "do_not_retry",
  core_rejected_token: "do_not_retry",
  internal: "unknown",
  conflict: "unknown",
};

export const coreAdvice: Record<CoreErrorCode, RetryAdvice> = {
  UNAVAILABLE: "retry_now",
  DEADLINE_EXCEEDED: "retry_now",
  CANCELLED: "retry_now",
  ERROR_REASON_SEND_IN_PROGRESS: "retry_later",
  ERROR_REASON_INSTANCE_QUEUE_FULL: "retry_later",
  RESOURCE_EXHAUSTED: "retry_later",
  ERROR_REASON_SEND_OUTCOME_UNKNOWN: "check_if_sent",
  ERROR_REASON_DUPLICATE_MESSAGE: "do_not_retry",
  ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT: "do_not_retry",
  INVALID_ARGUMENT: "do_not_retry",
  NOT_FOUND: "do_not_retry",
  ALREADY_EXISTS: "do_not_retry",
  PERMISSION_DENIED: "do_not_retry",
  FAILED_PRECONDITION: "do_not_retry",
  OUT_OF_RANGE: "do_not_retry",
  UNIMPLEMENTED: "do_not_retry",
  UNAUTHENTICATED: "do_not_retry",
  UNKNOWN: "unknown",
  ABORTED: "unknown",
  INTERNAL: "unknown",
  DATA_LOSS: "unknown",
};

export const clientAdvice: Record<ClientErrorCode, RetryAdvice> = {
  timeout: "retry_now",
  network_error: "retry_now",
  invalid_response: "retry_now",
  aborted: "retry_now",
  stream_failed: "retry_now",
  invalid_signature: "do_not_retry",
  stale_timestamp: "do_not_retry",
  invalid_event: "do_not_retry",
  unsupported_runtime: "do_not_retry",
  invalid_config: "do_not_retry",
};
