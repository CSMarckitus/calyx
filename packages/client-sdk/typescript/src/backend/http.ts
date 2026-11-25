import { CalyxError, type CalyxErrorInit, type ProblemField } from "../errors/index.js";
import { version } from "../version.js";
import { createCallCodes, createCallOperations, createCallRetry } from "./create-calls.js";
import type { OperationRow } from "./operations.js";

export interface HttpConfig {
  baseUrl: string;
  apiKey?: string;
  fetch?: typeof fetch;
  timeoutMs?: number;
}

export interface CallOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
}

export interface Call {
  config: HttpConfig;
  // The operation id. The stream of the pairing sets none.
  op?: string;
  row: OperationRow;
  params: Record<string, unknown>;
  options: CallOptions;
  idempotencyKey?: string;
}

// The size limits of CONTRACT.md. The SDK has no public option for them. Only the tests change them.
export let maxAnswerBytes = 32 * 1024 * 1024;
export let maxEventBytes = 1024 * 1024;

export function setSizeLimitsForTest(answer: number, event: number): void {
  maxAnswerBytes = answer;
  maxEventBytes = event;
}

// A typed cast, because the backend bundle imports no "node:" module and has no Node types.
const nodeVersion = (globalThis as { process?: { versions?: { node?: unknown } } }).process
  ?.versions?.node;

// A browser keeps its own User-Agent, so only Node gets the header.
export function setUserAgent(headers: Headers): void {
  if (typeof nodeVersion === "string") headers.set("User-Agent", `calyx-typescript-sdk/${version}`);
}

class AnswerTooLarge extends Error {}

// Reads a body as a stream and counts the bytes. A body of exactly the limit is accepted.
export async function readText(res: Response): Promise<string> {
  if (res.body === null) return "";
  const reader = res.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maxAnswerBytes) throw new AnswerTooLarge();
      chunks.push(value);
    }
  } catch (err) {
    // Close the connection, so a large body does not keep arriving.
    await reader.cancel().catch(() => undefined);
    throw err;
  }
  const bytes = new Uint8Array(size);
  let at = 0;
  for (const c of chunks) {
    bytes.set(c, at);
    at += c.byteLength;
  }
  return new TextDecoder().decode(bytes);
}

export function isAnswerTooLarge(err: unknown): boolean {
  return err instanceof AnswerTooLarge;
}

export function tooLarge(call: Call, cause?: unknown): CalyxError {
  return fail(call, {
    code: "invalid_response",
    detail: "The backend answered with a body that is larger than the SDK reads.",
    cause,
  });
}

const noBody = new Set(["GET", "DELETE"]);

// Steps 1 and 2: the URL, and the split of the parameter object into path parameters and body.
export function build(call: Call): { url: string; body?: string } {
  const { row, params } = call;
  const rest: Record<string, unknown> = { ...params };
  const path = row.path.replace(/\{(\w+)\}/g, (_, name: string) => {
    const value = rest[name];
    delete rest[name];
    return encodeURIComponent(String(value));
  });
  const url = call.config.baseUrl.replace(/\/$/, "") + path;
  // Every write body is required by the backend, so an empty object still goes out.
  return noBody.has(row.method) ? { url } : { url, body: JSON.stringify(rest) };
}

export function fail(
  call: Call,
  init: Omit<CalyxErrorInit, "source" | "status" | "idempotencyKey" | "retry">,
): CalyxError {
  const unsure =
    call.op !== undefined &&
    createCallOperations.includes(call.op) &&
    createCallCodes.includes(init.code);
  return new CalyxError({
    source: "client",
    status: 0,
    ...(unsure ? { retry: createCallRetry } : {}),
    ...(call.idempotencyKey === undefined ? {} : { idempotencyKey: call.idempotencyKey }),
    ...init,
  });
}

function isHeadersTimeout(err: unknown): boolean {
  const cause =
    err instanceof TypeError ? (err as { cause?: { code?: unknown } }).cause : undefined;
  return cause?.code === "UND_ERR_HEADERS_TIMEOUT";
}

// Step 5: a non-2xx answer. Only a problem+json body with a code becomes a backend error.
export function problemError(call: Call, res: Response, text: string): CalyxError {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    parsed = undefined;
  }
  const p = parsed as {
    code?: unknown;
    detail?: unknown;
    title?: unknown;
    errors?: ProblemField[];
  } | null;
  if (typeof p?.code !== "string") {
    return fail(call, {
      code: "invalid_response",
      detail: `The backend answered ${res.status} with a body that is not problem+json.`,
    });
  }
  const rawRetryAfter = res.headers.get("retry-after")?.trim();
  const retryAfter = rawRetryAfter ? Number(rawRetryAfter) : Number.NaN;
  const requestId = res.headers.get("x-request-id");
  return new CalyxError({
    source: "backend",
    code: p.code,
    status: res.status,
    detail: typeof p.detail === "string" ? p.detail : typeof p.title === "string" ? p.title : "",
    ...(p.errors === undefined ? {} : { errors: p.errors }),
    ...(requestId === null ? {} : { requestId }),
    ...(Number.isFinite(retryAfter) && retryAfter >= 0 ? { retryAfter } : {}),
    ...(call.idempotencyKey === undefined ? {} : { idempotencyKey: call.idempotencyKey }),
  });
}

// The fetch layer. It sends one request and never sends it again.
export async function request(call: Call): Promise<unknown> {
  const { config, row, options } = call;
  const { url, body } = build(call);

  // Step 3: the headers.
  const headers = new Headers();
  if (config.apiKey !== undefined) headers.set("Authorization", `Bearer ${config.apiKey}`);
  if (body !== undefined) headers.set("Content-Type", "application/json");
  if (call.idempotencyKey !== undefined) headers.set("Idempotency-Key", call.idempotencyKey);
  setUserAgent(headers);

  // Step 4: the call limit, then the client limit, then the default of the operation.
  const limit = options.timeoutMs ?? config.timeoutMs ?? row.timeoutMs;
  const timeout = limit === undefined ? undefined : AbortSignal.timeout(limit);
  const signals = [options.signal, timeout].filter((s): s is AbortSignal => s !== undefined);
  const signal = signals.length > 1 ? AbortSignal.any(signals) : signals[0];

  const doFetch = config.fetch ?? globalThis.fetch;
  let res: Response;
  let text: string;
  try {
    res = await doFetch(url, {
      method: row.method,
      headers,
      ...(body === undefined ? {} : { body }),
      ...(signal === undefined ? {} : { signal }),
    });
    text = await readText(res);
  } catch (err) {
    if (isAnswerTooLarge(err)) throw tooLarge(call, err);
    // Step 6 and 7: the reason of a failure.
    if (options.signal?.aborted) {
      throw fail(call, { code: "aborted", detail: "The program aborted the call.", cause: err });
    }
    if (timeout?.aborted || isHeadersTimeout(err)) {
      throw fail(call, {
        code: "timeout",
        detail: "The backend did not answer in time.",
        cause: err,
      });
    }
    throw fail(call, {
      code: "network_error",
      detail: "The connection to the backend failed.",
      cause: err,
    });
  }

  if (!res.ok) throw problemError(call, res, text);
  if (text === "") return undefined;
  try {
    return JSON.parse(text);
  } catch (err) {
    throw fail(call, {
      code: "invalid_response",
      detail: `The backend answered ${res.status} with a body that is not JSON.`,
      cause: err,
    });
  }
}
