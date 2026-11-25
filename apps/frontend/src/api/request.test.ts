import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { request, stream } from "./request.ts";

// Vitest runs in apps/frontend.
const data = (name: string) =>
  JSON.parse(
    readFileSync(resolve(process.cwd(), "../../packages/client-sdk/testdata", name), "utf8"),
  );

interface ErrorCase {
  name: string;
  status: number;
  headers: Record<string, string>;
  body: string;
  expected: {
    source: "backend" | "client";
    code: string;
    status: number;
    detail: string;
    requestId: string;
    retryAfterSeconds: number;
    errors: { location: string; message: string }[];
  };
}

interface SseCase {
  name: string;
  chunks: string[];
  expected: { items: unknown[]; error: string | null };
}

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

// Rejects as fetch does when the signal aborts.
const hang = (_url: RequestInfo | URL, init?: RequestInit) =>
  new Promise<Response>((_resolve, reject) => {
    init?.signal?.addEventListener("abort", () =>
      reject(new DOMException("aborted", "AbortError")),
    );
  });

describe("request", () => {
  it("sends the path, the method and the body, and gives the data", async () => {
    fetchMock.mockResolvedValue(json({ id: "i1" }, 201));
    const out = await request("createInstance", {
      body: { name: "Shop", provider: "WHATSAPP_WEB" } as never,
    });
    expect(out).toEqual({ ok: true, data: { id: "i1" } });
    const [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(url).toBe("/api/v1/instances");
    expect(init?.method).toBe("POST");
    expect(init?.credentials).toBe("same-origin");
    expect(init?.mode).toBeUndefined();
    expect(new Headers(init?.headers).get("Content-Type")).toBe("application/json");
    expect(init?.body).toBe(JSON.stringify({ name: "Shop", provider: "WHATSAPP_WEB" }));
  });

  it("puts the path parameters in the path, escaped", async () => {
    fetchMock.mockResolvedValue(json({}));
    await request("getInstance", { path: { id: "a/b c" } });
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/instances/a%2Fb%20c");
    expect(fetchMock.mock.calls[0]?.[1]?.body).toBeUndefined();
  });

  it("gives undefined for an answer with no body", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    expect(await request("logout", {})).toEqual({ ok: true, data: undefined });
  });

  it("gives unreachable for a good answer that is not JSON", async () => {
    fetchMock.mockResolvedValue(new Response("<html>", { status: 200 }));
    expect(await request("getMe", {})).toEqual({ ok: false, failure: { kind: "unreachable" } });
  });

  it("gives unreachable when fetch fails", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    expect(await request("getMe", {})).toEqual({ ok: false, failure: { kind: "unreachable" } });
  });

  it("does not send a request again", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    await request("getMe", {});
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["getMe", 20_000],
    ["updateInstance", 45_000],
    ["removeInstance", 75_000],
  ] as const)("times out %s after %i ms", async (op, ms) => {
    vi.useFakeTimers();
    fetchMock.mockImplementation(hang);
    const params =
      op === "getMe"
        ? {}
        : op === "updateInstance"
          ? { path: { id: "i" }, body: {} }
          : { path: { id: "i" } };
    const pending = request(op, params as never);
    await vi.advanceTimersByTimeAsync(ms - 1);
    let done = false;
    void pending.then(() => {
      done = true;
    });
    await vi.advanceTimersByTimeAsync(0);
    expect(done).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(await pending).toEqual({ ok: false, failure: { kind: "timeout" } });
  });

  it("gives unreachable when the caller aborts", async () => {
    fetchMock.mockImplementation(hang);
    const controller = new AbortController();
    const pending = request("getMe", {}, controller.signal);
    controller.abort();
    expect(await pending).toEqual({ ok: false, failure: { kind: "unreachable" } });
  });

  describe("backend-errors.json", () => {
    for (const c of data("backend-errors.json").cases as ErrorCase[]) {
      it(c.name, async () => {
        fetchMock.mockResolvedValue(
          new Response(c.body === "" ? null : c.body, { status: c.status, headers: c.headers }),
        );
        const out = await request("getMe", {});
        expect(out.ok).toBe(false);
        if (out.ok) return;
        if (c.expected.source === "client") {
          expect(out.failure).toEqual({ kind: "unreachable" });
          return;
        }
        const e = c.expected;
        expect(out.failure).toEqual({
          kind: "problem",
          status: e.status,
          code: e.code,
          detail: e.detail,
          fields: e.errors,
          requestId: e.requestId,
          ...(e.retryAfterSeconds > 0 ? { retryAfter: e.retryAfterSeconds } : {}),
        });
      });
    }
  });
});

const sseResponse = (chunks: string[]) => {
  const encoder = new TextEncoder();
  return new Response(
    new ReadableStream({
      start(controller) {
        for (const c of chunks) controller.enqueue(encoder.encode(c));
        controller.close();
      },
    }),
    { status: 200, headers: { "Content-Type": "text/event-stream" } },
  );
};

describe("stream", () => {
  it("sends a GET that accepts an event stream", async () => {
    fetchMock.mockResolvedValue(sseResponse([]));
    await stream("followPairing", { path: { id: "i1" } }, () => {}, new AbortController().signal);
    const [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(url).toBe("/api/v1/instances/i1/pairing");
    expect(init?.method).toBe("GET");
    expect(init?.credentials).toBe("same-origin");
    expect(new Headers(init?.headers).get("Accept")).toBe("text/event-stream");
  });

  describe("sse-cases.json", () => {
    for (const c of data("sse-cases.json").cases as SseCase[]) {
      it(c.name, async () => {
        fetchMock.mockResolvedValue(sseResponse(c.chunks));
        const items: unknown[] = [];
        const out = await stream(
          "followPairing",
          { path: { id: "i1" } },
          (item) => items.push(item),
          new AbortController().signal,
        );
        expect(items).toEqual(c.expected.items);
        if (c.expected.error === null || c.expected.error === "stream_failed") {
          expect(out).toBe("ended");
        } else {
          expect(out).toEqual({ kind: "unreachable" });
        }
      });
    }
  });

  it("skips an event that the operation does not have", async () => {
    fetchMock.mockResolvedValue(
      sseResponse(["event: other\ndata: {}\n\n", "event: changed\ndata: {}\n\n"]),
    );
    const items: unknown[] = [];
    await stream("followEvents", {}, (i) => items.push(i), new AbortController().signal);
    expect(items).toEqual([{ event: "changed", data: {} }]);
  });

  it("gives a problem for an error answer", async () => {
    fetchMock.mockResolvedValue(
      new Response(
        JSON.stringify({
          status: 404,
          code: "not_found",
          detail: "No such instance.",
          title: "Not Found",
        }),
        {
          status: 404,
          headers: { "Content-Type": "application/problem+json", "X-Request-Id": "r9" },
        },
      ),
    );
    expect(
      await stream("followPairing", { path: { id: "x" } }, () => {}, new AbortController().signal),
    ).toEqual({
      kind: "problem",
      status: 404,
      code: "not_found",
      detail: "No such instance.",
      fields: [],
      requestId: "r9",
    });
  });

  it("gives unreachable for a 502 with no problem object", async () => {
    fetchMock.mockResolvedValue(new Response("<html>", { status: 502 }));
    expect(await stream("followEvents", {}, () => {}, new AbortController().signal)).toEqual({
      kind: "unreachable",
    });
  });

  it("gives unreachable when the answer is not an event stream", async () => {
    fetchMock.mockResolvedValue(json({}));
    expect(await stream("followEvents", {}, () => {}, new AbortController().signal)).toEqual({
      kind: "unreachable",
    });
  });

  it("gives unreachable when fetch fails", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));
    expect(await stream("followEvents", {}, () => {}, new AbortController().signal)).toEqual({
      kind: "unreachable",
    });
  });

  it("gives unreachable for an event above 1 MiB", async () => {
    fetchMock.mockResolvedValue(sseResponse([`data: ${"x".repeat(1024 * 1024)}\n\n`]));
    expect(await stream("followEvents", {}, () => {}, new AbortController().signal)).toEqual({
      kind: "unreachable",
    });
  });

  it("gives unreachable after 45 s with no byte, and restarts the wait at each chunk", async () => {
    vi.useFakeTimers();
    let push: (text: string) => void = () => {};
    fetchMock.mockImplementation((_url, init) =>
      Promise.resolve(
        new Response(
          new ReadableStream({
            start(controller) {
              const encoder = new TextEncoder();
              push = (t) => controller.enqueue(encoder.encode(t));
              init?.signal?.addEventListener("abort", () =>
                controller.error(new DOMException("aborted", "AbortError")),
              );
            },
          }),
          { status: 200, headers: { "Content-Type": "text/event-stream" } },
        ),
      ),
    );
    const pending = stream("followEvents", {}, () => {}, new AbortController().signal);
    await vi.advanceTimersByTimeAsync(44_000);
    push(": ping\n\n");
    await vi.advanceTimersByTimeAsync(44_000);
    push(": ping\n\n");
    await vi.advanceTimersByTimeAsync(44_999);
    await vi.advanceTimersByTimeAsync(1);
    expect(await pending).toEqual({ kind: "unreachable" });
  });

  it("gives ended when the caller aborts", async () => {
    fetchMock.mockImplementation((_url, init) =>
      Promise.resolve(
        new Response(
          new ReadableStream({
            start(controller) {
              init?.signal?.addEventListener("abort", () =>
                controller.error(new DOMException("aborted", "AbortError")),
              );
            },
          }),
          { status: 200, headers: { "Content-Type": "text/event-stream" } },
        ),
      ),
    );
    const controller = new AbortController();
    const pending = stream("followEvents", {}, () => {}, controller.signal);
    await Promise.resolve();
    controller.abort();
    expect(await pending).toBe("ended");
  });

  it("does not call fetch when the signal is already aborted", async () => {
    const controller = new AbortController();
    controller.abort();
    expect(await stream("followEvents", {}, () => {}, controller.signal)).toBe("ended");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
