import type { components } from "../generated/backend.js";
import { backendAdvice, clientAdvice, coreAdvice, type RetryAdvice } from "./advice.js";
import type { BackendErrorCode, ClientErrorCode, CoreErrorCode, KnownErrorCode } from "./codes.js";

export type { RetryAdvice } from "./advice.js";
export type { BackendErrorCode, ClientErrorCode, CoreErrorCode, KnownErrorCode } from "./codes.js";

export type ProblemField = components["schemas"]["ProblemField"];

export type ErrorSource = "backend" | "core" | "client";

export interface CalyxErrorInit {
  source: ErrorSource;
  code: string;
  status: number;
  detail: string;
  errors?: ProblemField[];
  requestId?: string;
  // The minimum wait in whole seconds: the Retry-After header of the backend or the RetryInfo
  // detail of the core. Unset or 0 means no wait.
  retryAfter?: number;
  idempotencyKey?: string;
  metadata?: Record<string, string>;
  cause?: unknown;
  // Overrides the advice of the code.
  retry?: RetryAdvice;
}

const maps: Record<ErrorSource, Record<string, RetryAdvice>> = {
  backend: backendAdvice,
  core: coreAdvice,
  client: clientAdvice,
};

function adviceFor(source: ErrorSource, code: string): RetryAdvice {
  // The own-property check keeps a code such as "constructor" from reading Object.prototype.
  return Object.hasOwn(maps[source], code) ? maps[source][code]! : "unknown";
}

export class CalyxError extends Error {
  readonly source: ErrorSource;
  readonly code: KnownErrorCode | (string & {});
  readonly status: number;
  readonly detail: string;
  readonly retry: RetryAdvice;
  readonly errors?: ProblemField[];
  readonly requestId?: string;
  // The minimum wait in whole seconds: the Retry-After header of the backend or the RetryInfo
  // detail of the core. Unset or 0 means no wait.
  readonly retryAfter?: number;
  readonly idempotencyKey?: string;
  readonly metadata?: Record<string, string>;

  constructor(init: CalyxErrorInit) {
    super(init.detail, init.cause === undefined ? undefined : { cause: init.cause });
    this.name = "CalyxError";
    this.source = init.source;
    this.code = init.code;
    this.status = init.status;
    this.detail = init.detail;
    this.retry = init.retry ?? adviceFor(init.source, init.code);
    if (init.errors !== undefined) this.errors = init.errors;
    if (init.requestId !== undefined) this.requestId = init.requestId;
    if (init.retryAfter !== undefined) this.retryAfter = init.retryAfter;
    if (init.idempotencyKey !== undefined) this.idempotencyKey = init.idempotencyKey;
    if (init.metadata !== undefined) this.metadata = init.metadata;
  }
}

export function isCalyxError(err: unknown): err is CalyxError;
export function isCalyxError<C extends KnownErrorCode>(
  err: unknown,
  code: C,
): err is CalyxError & { code: C };
export function isCalyxError(err: unknown, code?: string): boolean {
  return err instanceof CalyxError && (code === undefined || err.code === code);
}
