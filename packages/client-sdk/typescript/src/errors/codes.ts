import type { components } from "../generated/backend.js";
import type { ErrorReason } from "../generated/core-types/calyx/core/v1/message.js";

export type BackendErrorCode = components["schemas"]["Problem"]["code"];

type ReasonName = Exclude<keyof typeof ErrorReason, "ERROR_REASON_UNSPECIFIED" | "UNRECOGNIZED">;

// The 16 gRPC status code names, without OK.
export const grpcStatusNames = [
  "CANCELLED",
  "UNKNOWN",
  "INVALID_ARGUMENT",
  "DEADLINE_EXCEEDED",
  "NOT_FOUND",
  "ALREADY_EXISTS",
  "PERMISSION_DENIED",
  "RESOURCE_EXHAUSTED",
  "FAILED_PRECONDITION",
  "ABORTED",
  "OUT_OF_RANGE",
  "UNIMPLEMENTED",
  "INTERNAL",
  "UNAVAILABLE",
  "DATA_LOSS",
  "UNAUTHENTICATED",
] as const;

export type GrpcStatusName = (typeof grpcStatusNames)[number];

export type CoreErrorCode = ReasonName | GrpcStatusName;

// These codes belong to the SDK. No contract lists them.
export type ClientErrorCode =
  | "timeout"
  | "aborted"
  | "network_error"
  | "invalid_response"
  | "stream_failed"
  | "invalid_signature"
  | "stale_timestamp"
  | "invalid_event"
  | "unsupported_runtime"
  | "invalid_config";

export type KnownErrorCode = BackendErrorCode | CoreErrorCode | ClientErrorCode;
