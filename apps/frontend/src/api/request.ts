import {
  type Answer,
  type JsonOperationId,
  operations,
  type Params,
  type StreamItem,
  type StreamOperationId,
} from "./operations.ts";
import { FrameParser } from "./sse.ts";

export type Failure =
  | {
      kind: "problem";
      status: number;
      code: string;
      detail: string;
      fields: { location: string; message: string }[];
      requestId: string;
      // Seconds. Absent when the backend gives none, or gives 0.
      retryAfter?: number;
    }
  | { kind: "unreachable" }
  | { kind: "timeout" };

export type Outcome<T> = { ok: true; data: T } | { ok: false; failure: Failure };

const unreachable: Failure = { kind: "unreachable" };

// The backend sends a ping every 15 s, so 45 s with no byte means a dead connection.
const idleMs = 45_000;

function url(path: string, params: { path?: Record<string, string> | undefined }): string {
  return path.replace(/\{(\w+)\}/g, (_, name: string) =>
    encodeURIComponent(params.path?.[name] ?? ""),
  );
}

// An answer that is not 2xx. Only a problem object with a `code` is a `problem`. Anything else
// (a page of a proxy, an empty body) means that the backend did not answer.
async function problem(res: Response): Promise<Failure> {
  let body: { code?: unknown; detail?: unknown; title?: unknown; errors?: unknown } | null;
  try {
    body = JSON.parse(await res.text());
  } catch {
    return unreachable;
  }
  if (typeof body?.code !== "string") return unreachable;
  const wait = Number(res.headers.get("retry-after")?.trim() || Number.NaN);
  return {
    kind: "problem",
    status: res.status,
    code: body.code,
    detail:
      typeof body.detail === "string"
        ? body.detail
        : typeof body.title === "string"
          ? body.title
          : "",
    fields: Array.isArray(body.errors)
      ? (body.errors as { location: string; message: string }[])
      : [],
    requestId: res.headers.get("x-request-id") ?? "",
    ...(Number.isFinite(wait) && wait > 0 ? { retryAfter: wait } : {}),
  };
}

// Sends one request, and never sends it again. A caller that aborts gets `unreachable`: it does
// not read the result.
export async function request<K extends JsonOperationId>(
  op: K,
  params: Params<K>,
  signal?: AbortSignal,
): Promise<Outcome<Answer<K>>> {
  const row = operations[op] as { method: string; path: string; timeoutMs: number };
  const control = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    control.abort();
  }, row.timeoutMs);
  const abort = () => control.abort();
  signal?.addEventListener("abort", abort);
  if (signal?.aborted) control.abort();
  try {
    const hasBody = params.body !== undefined;
    const res = await fetch(url(row.path, params as { path?: Record<string, string> }), {
      method: row.method,
      credentials: "same-origin",
      headers: hasBody ? { "Content-Type": "application/json" } : undefined,
      body: hasBody ? JSON.stringify(params.body) : undefined,
      signal: control.signal,
    });
    if (!res.ok) return { ok: false, failure: await problem(res) };
    const text = await res.text();
    if (text === "") return { ok: true, data: undefined as Answer<K> };
    return { ok: true, data: JSON.parse(text) as Answer<K> };
  } catch {
    return { ok: false, failure: timedOut ? { kind: "timeout" } : unreachable };
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
  }
}

// Reads a stream until the server closes it. It gives "ended" when the server closes it, and when
// the caller aborts. It gives a failure for any other end, and never opens the stream again.
export async function stream<K extends StreamOperationId>(
  op: K,
  params: Params<K>,
  onEvent: (item: StreamItem<K>) => void,
  signal: AbortSignal,
): Promise<"ended" | Failure> {
  if (signal.aborted) return "ended";
  const row = operations[op] as { method: string; path: string; events: readonly string[] };
  const control = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  // The wait starts before the connection opens, and restarts at each chunk.
  const restart = () => {
    clearTimeout(timer);
    timer = setTimeout(() => {
      control.abort();
    }, idleMs);
  };
  const abort = () => control.abort();
  signal.addEventListener("abort", abort);
  restart();
  try {
    const res = await fetch(url(row.path, params as { path?: Record<string, string> }), {
      method: row.method,
      credentials: "same-origin",
      headers: { Accept: "text/event-stream" },
      signal: control.signal,
    });
    if (!res.ok) return await problem(res);
    const type = res.headers.get("content-type") ?? "";
    if (!type.toLowerCase().startsWith("text/event-stream") || res.body === null)
      return unreachable;

    const decoder = new TextDecoder();
    const parser = new FrameParser();
    const reader = res.body.getReader();
    try {
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done) return "ended";
        restart();
        for (const frame of parser.push(decoder.decode(chunk.value, { stream: true }))) {
          if (!row.events.includes(frame.event)) continue;
          onEvent({ event: frame.event, data: JSON.parse(frame.data) } as StreamItem<K>);
        }
      }
    } finally {
      await reader.cancel().catch(() => undefined);
    }
  } catch {
    if (signal.aborted) return "ended";
    // An event above 1 MiB, data that is not JSON, an idle stream and a broken connection.
    return unreachable;
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", abort);
    control.abort();
  }
}
