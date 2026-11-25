import { status as grpcStatus, type ServiceError } from "@grpc/grpc-js";

import { CalyxError } from "../errors/index.js";
import { ErrorInfo, RetryInfo } from "../generated/core-grpc/google/rpc/error_details.js";
import { Status } from "../generated/core-grpc/google/rpc/status.js";

const errorInfoType = "type.googleapis.com/google.rpc.ErrorInfo";
const retryInfoType = "type.googleapis.com/google.rpc.RetryInfo";
// The domain of the core (errorDomain in apps/core/internal/grpcserver/server.go).
const coreDomain = "calyx.core.v1";

// Steps 1 to 4: the ErrorInfo detail of the core, if the status has one.
function coreInfo(err: ServiceError): ErrorInfo | undefined {
  const raw = err.metadata?.get("grpc-status-details-bin")[0];
  if (!(raw instanceof Uint8Array)) return undefined;
  try {
    for (const detail of Status.decode(raw).details) {
      if (detail.typeUrl !== errorInfoType) continue;
      const info = ErrorInfo.decode(detail.value);
      if (info.domain === coreDomain && info.reason !== "") return info;
    }
  } catch {
    // A detail that does not decode gives the status code name, as a status without details.
  }
  return undefined;
}

// The wait of the RetryInfo detail in whole seconds, rounded up, so the program never waits
// less than the core asks. A missing detail or a zero delay gives undefined.
function retryAfterSeconds(err: ServiceError): number | undefined {
  const raw = err.metadata?.get("grpc-status-details-bin")[0];
  if (!(raw instanceof Uint8Array)) return undefined;
  try {
    for (const detail of Status.decode(raw).details) {
      if (detail.typeUrl !== retryInfoType) continue;
      const delay = RetryInfo.decode(detail.value).retryDelay;
      if (delay === undefined) return undefined;
      const seconds = Number(delay.seconds) + Math.ceil(delay.nanos / 1e9);
      return seconds > 0 ? seconds : undefined;
    }
  } catch {
    // A detail that does not decode gives no wait.
  }
  return undefined;
}

// Step 5: a failed gRPC call becomes a CalyxError with the reason of the core or the status name.
export function coreError(err: ServiceError, idempotencyKey?: string): CalyxError {
  const info = coreInfo(err);
  const retryAfter = retryAfterSeconds(err);
  return new CalyxError({
    source: "core",
    code: info?.reason ?? grpcStatus[err.code] ?? String(err.code),
    status: err.code,
    detail: err.details,
    ...(info === undefined ? {} : { metadata: info.metadata }),
    ...(retryAfter === undefined ? {} : { retryAfter }),
    ...(idempotencyKey === undefined ? {} : { idempotencyKey }),
    cause: err,
  });
}
