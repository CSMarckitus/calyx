import { type QueryClient, QueryClientProvider, QueryObserver } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { createElement, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Failure, Outcome } from "./request.ts";

const request = vi.hoisted(() => vi.fn());
vi.mock("./request.ts", () => ({ request }));

import {
  apiDocumentOptions,
  apiKeysOptions,
  bulkJobsOptions,
  clientOptions,
  clientsOptions,
  createQueryClient,
  createReads,
  installRefresh,
  instancesOptions,
  keys,
  meOptions,
  organizationOptions,
  useBackendVersion,
  useChange,
  useCheckAgain,
} from "./queries.ts";

const ok = (data: unknown): Outcome<unknown> => ({ ok: true, data });
const fail = (failure: Failure): Outcome<unknown> => ({ ok: false, failure });
const problem: Failure = {
  kind: "problem",
  status: 401,
  code: "unauthorized",
  detail: "",
  fields: [],
  requestId: "",
};

// A read that the test ends by hand.
function deferred() {
  let end!: (o: Outcome<unknown>) => void;
  const promise = new Promise<Outcome<unknown>>((r) => {
    end = r;
  });
  return { promise, end };
}

let client: QueryClient;
beforeEach(() => {
  request.mockReset();
  client = createQueryClient();
});
afterEach(() => {
  client.clear();
  vi.useRealTimers();
});

describe("the queries", () => {
  it("read the operation of each key", async () => {
    request.mockResolvedValue(ok({}));
    await client.fetchQuery(meOptions());
    await client.fetchQuery(instancesOptions());
    await client.fetchQuery(clientsOptions());
    await client.fetchQuery(clientOptions("c1"));
    await client.fetchQuery(organizationOptions());
    expect(request.mock.calls.map((c) => [c[0], c[1]])).toEqual([
      ["getMe", {}],
      ["listInstances", {}],
      ["listClients", {}],
      ["getClient", { path: { id: "c1" } }],
      ["getOrganization", {}],
    ]);
  });

  it("read the API keys and the bulk jobs of an instance", async () => {
    request.mockResolvedValue(ok([]));
    await client.fetchQuery(apiKeysOptions());
    await client.fetchQuery(bulkJobsOptions("i1"));
    expect(keys.apiKeys).toEqual(["api-keys"]);
    expect(keys.bulkJobs("i1")).toEqual(["bulk-jobs", "i1"]);
    expect(request.mock.calls.map((c) => [c[0], c[1]])).toEqual([
      ["listAPIKeys", {}],
      ["listBulkJobs", { path: { id: "i1" } }],
    ]);
  });

  it("read the bulk jobs again on each mount, and the API keys only once", async () => {
    request.mockResolvedValue(ok([]));
    const open = (o: { queryKey: readonly unknown[] }) =>
      new QueryObserver(client, o as never).subscribe(() => undefined);
    for (const options of [bulkJobsOptions("i1"), apiKeysOptions()]) {
      for (let n = 0; n < 2; n++) {
        const stop = open(options);
        await vi.waitFor(() => expect(client.isFetching()).toBe(0));
        stop();
      }
    }
    expect(request.mock.calls.map((c) => c[0])).toEqual([
      "listBulkJobs",
      "listBulkJobs",
      "listAPIKeys",
    ]);
  });

  it("throw the failure, so the query holds it", async () => {
    request.mockResolvedValue(fail(problem));
    await expect(client.fetchQuery(meOptions())).rejects.toBe(problem);
  });

  it("do not get old by time", async () => {
    request.mockResolvedValue(ok({ n: 1 }));
    await client.fetchQuery(meOptions());
    await client.fetchQuery(meOptions());
    expect(request).toHaveBeenCalledTimes(1);
  });

  // fetchQuery has its own default of no retry, so these tests read as a screen does.
  const watch = (options: { queryKey: readonly unknown[] }) =>
    new QueryObserver(client, options as never).subscribe(() => undefined);

  it("retry once an unreachable read, and once a timeout", async () => {
    vi.useFakeTimers();
    request.mockResolvedValueOnce(fail({ kind: "unreachable" })).mockResolvedValue(ok({}));
    const stopMe = watch(meOptions());
    await vi.advanceTimersByTimeAsync(5000);
    expect(request).toHaveBeenCalledTimes(2);
    expect(client.getQueryState(["me"])?.status).toBe("success");

    request.mockReset();
    request.mockResolvedValue(fail({ kind: "timeout" }));
    const stopList = watch(instancesOptions());
    await vi.advanceTimersByTimeAsync(20_000);
    expect(request).toHaveBeenCalledTimes(2);
    expect(client.getQueryState(["instances"])?.error).toEqual({ kind: "timeout" });
    stopMe();
    stopList();
  });

  it("do not retry a problem", async () => {
    request.mockResolvedValue(fail(problem));
    const stop = watch(meOptions());
    await vi.waitFor(() => expect(client.getQueryState(["me"])?.status).toBe("error"));
    await new Promise((r) => setTimeout(r, 1200));
    expect(request).toHaveBeenCalledTimes(1);
    stop();
  });

  // The shell hides its screen while `me` reads. A screen that mounts and reads a failed `me`
  // again would hide itself, mount again, and read again with no end.
  it("do not read a failed me again when one more screen uses it", async () => {
    request.mockResolvedValue(fail(problem));
    const first = watch(meOptions());
    await vi.waitFor(() => expect(client.getQueryState(["me"])?.status).toBe("error"));
    const second = watch(meOptions());
    await new Promise((r) => setTimeout(r, 50));
    expect(request).toHaveBeenCalledTimes(1);
    first();
    second();
  });

  it("do not retry a change", () => {
    expect(client.getDefaultOptions().mutations?.retry).toBe(false);
  });
});

describe("the reads", () => {
  function mount(options: { queryKey: readonly unknown[] }) {
    const observer = new QueryObserver(client, options as never);
    return observer.subscribe(() => undefined);
  }

  it("read a mounted query again, and only mark an unmounted one", async () => {
    request.mockResolvedValue(ok({}));
    const reads = createReads(client);
    const stop = mount(meOptions());
    await client.fetchQuery(instancesOptions());
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(2));
    request.mockClear();

    reads.refresh();
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));
    expect(request.mock.calls[0]?.[0]).toBe("getMe");
    expect(client.getQueryState(["instances"])?.isInvalidated).toBe(true);
    stop();
    reads.dispose();
  });

  it("reads me even when no screen mounts it", async () => {
    request.mockResolvedValue(ok({}));
    const reads = createReads(client);
    await client.fetchQuery(meOptions());
    request.mockClear();
    reads.refresh();
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));
    reads.dispose();
  });

  it("read a key once more when it got old during its read, never in parallel", async () => {
    const reads = createReads(client);
    const first = deferred();
    const second = deferred();
    let running = 0;
    let most = 0;
    request.mockImplementation(async () => {
      running++;
      most = Math.max(most, running);
      const o = await (request.mock.calls.length === 1 ? first : second).promise;
      running--;
      return o;
    });
    const stop = mount(instancesOptions());
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));

    reads.refresh();
    reads.refresh();
    reads.refresh();
    expect(request).toHaveBeenCalledTimes(1);
    first.end(ok({ n: 1 }));
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(2));
    reads.refresh();
    second.end(ok({ n: 2 }));
    await vi.waitFor(() => expect(client.getQueryData(["instances"])).toEqual({ n: 2 }));
    await Promise.resolve();
    expect(request.mock.calls.length).toBe(3);
    expect(most).toBe(1);
    stop();
    reads.dispose();
  });

  it("run again only the queries that failed", async () => {
    const reads = createReads(client);
    request.mockImplementation(async (op: string) =>
      op === "getMe" ? ok({}) : fail({ kind: "unreachable" }),
    );
    const noRetry = { retry: false } as const;
    const stopA = mount({ ...meOptions(), ...noRetry });
    const stopB = mount({ ...instancesOptions(), ...noRetry });
    await vi.waitFor(() => expect(client.getQueryState(["instances"])?.status).toBe("error"));
    request.mockClear();
    request.mockResolvedValue(ok({}));
    reads.refreshFailed();
    await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(1));
    expect(request.mock.calls[0]?.[0]).toBe("listInstances");
    stopA();
    stopB();
    reads.dispose();
  });

  it("clear empties the cache and forgets the wait", async () => {
    request.mockResolvedValue(ok({}));
    const reads = createReads(client);
    await client.fetchQuery(meOptions());
    reads.clear();
    expect(client.getQueryCache().getAll()).toHaveLength(0);
    reads.dispose();
  });
});

// After a change with no known result, the page says "Checking the result" until the
// read of the data ends.
describe("a change", () => {
  const lost: [string, Failure][] = [
    ["timeout", { kind: "timeout" }],
    ["unreachable", { kind: "unreachable" }],
    ["change_outcome_unknown", { ...problem, status: 504, code: "change_outcome_unknown" }],
  ];

  const watch = (options: { queryKey: readonly unknown[] }) =>
    new QueryObserver(client, options as never).subscribe(() => undefined);

  function setup() {
    const reads = createReads(client);
    installRefresh(() => reads.refresh());
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client }, children);
    return renderHook(() => useChange("updateOrganization"), { wrapper });
  }

  it.each(lost)("with %s is checking until the read of the data ends", async (_n, failure) => {
    const read = deferred();
    request.mockImplementation((op: string) =>
      op === "listInstances" ? read.promise : Promise.resolve(fail(failure)),
    );
    const stop = watch(instancesOptions());
    read.end(ok([]));
    await vi.waitFor(() => expect(client.getQueryState(["instances"])?.status).toBe("success"));

    const again = deferred();
    request.mockImplementation((op: string) =>
      op === "listInstances" ? again.promise : Promise.resolve(fail(failure)),
    );
    const { result } = setup();
    expect(result.current.checking).toBe(false);
    act(() => result.current.mutate({} as never));
    await vi.waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.checking).toBe(true);
    await act(async () => again.end(ok([])));
    await vi.waitFor(() => expect(result.current.checking).toBe(false));
    stop();
  });

  it("with an answer is not checking", async () => {
    request.mockResolvedValue(fail({ ...problem, status: 409, code: "conflict" }));
    const { result } = setup();
    act(() => result.current.mutate({} as never));
    await vi.waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.checking).toBe(false);
  });
});

describe("Check again", () => {
  it("reads me, the clients and each client screen again", async () => {
    request.mockResolvedValue(ok({}));
    const stops = [meOptions(), clientsOptions(), clientOptions("c1")].map((o) =>
      new QueryObserver(client, o as never).subscribe(() => undefined),
    );
    await vi.waitFor(() => expect(client.isFetching()).toBe(0));
    expect(request).toHaveBeenCalledTimes(3);
    request.mockClear();
    const wrapper = ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client }, children);
    const { result } = renderHook(() => useCheckAgain(), { wrapper });
    await act(() => result.current.check());
    expect(request.mock.calls.map((c) => c[0]).sort()).toEqual([
      "getClient",
      "getMe",
      "listClients",
    ]);
    for (const stop of stops) stop();
  });
});

describe("the API document", () => {
  const fetchMock = vi.fn();
  beforeEach(() => {
    fetchMock.mockReset();
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("has its own key", () => {
    expect(keys.apiDocument).toEqual(["api-document"]);
    expect(apiDocumentOptions().queryKey).toEqual(keys.apiDocument);
  });

  it("reads /openapi.json from the same origin and returns the document", async () => {
    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ openapi: "3.1.0" }), { status: 200 }),
    );
    await expect(client.fetchQuery(apiDocumentOptions())).resolves.toEqual({ openapi: "3.1.0" });
    expect(fetchMock.mock.calls[0][0]).toBe("/openapi.json");
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ credentials: "same-origin" });
    expect(request).not.toHaveBeenCalled();
  });

  it("gives unreachable for the plain-text 503 answer of nginx", async () => {
    fetchMock.mockResolvedValue(new Response("Service Unavailable", { status: 503 }));
    await expect(client.fetchQuery(apiDocumentOptions())).rejects.toEqual({ kind: "unreachable" });
  });

  it("gives unreachable for a 200 answer that is not JSON", async () => {
    fetchMock.mockResolvedValue(new Response("<html>", { status: 200 }));
    await expect(client.fetchQuery(apiDocumentOptions())).rejects.toEqual({ kind: "unreachable" });
  });

  it("aborts after 30 seconds and gives unreachable", async () => {
    vi.useFakeTimers();
    let signal: AbortSignal | undefined;
    fetchMock.mockImplementation(
      (_url: string, init: RequestInit) =>
        new Promise((_resolve, reject) => {
          signal = init.signal as AbortSignal;
          signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
        }),
    );
    const result = client.fetchQuery(apiDocumentOptions()).catch((e) => e);
    await vi.advanceTimersByTimeAsync(29_999);
    expect(signal?.aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(signal?.aborted).toBe(true);
    // The query retries an unreachable read one time after 1 second, and it times out again.
    await vi.advanceTimersByTimeAsync(1000 + 30_000);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    await expect(result).resolves.toEqual({ kind: "unreachable" });
  });

  it("aborts the fetch when the query is cancelled", async () => {
    let signal: AbortSignal | undefined;
    fetchMock.mockImplementation(
      (_url: string, init: RequestInit) =>
        new Promise((_resolve, reject) => {
          signal = init.signal as AbortSignal;
          signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
        }),
    );
    const result = client.fetchQuery(apiDocumentOptions()).catch((e) => e);
    await client.cancelQueries({ queryKey: keys.apiDocument });
    expect(signal?.aborted).toBe(true);
    await result;
  });
});

describe("the backend version", () => {
  const fetchMock = vi.fn();
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children);
  beforeEach(() => {
    fetchMock.mockReset();
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  const body = { database: "ok", core: "ok", mail: "off", version: "0.1.0", core_version: "0.1.0" };

  it("reads /healthz once and returns the version", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify(body), { status: 200 }));
    const { result } = renderHook(() => useBackendVersion(), { wrapper });
    await waitFor(() => expect(result.current.data).toBe("0.1.0"));
    expect(fetchMock.mock.calls[0][0]).toBe("/healthz");
    expect(client.getQueryData(keys.healthz)).toBe("0.1.0");
  });

  it("returns the version of a 503 answer too", async () => {
    fetchMock.mockResolvedValue(
      new Response(JSON.stringify({ ...body, database: "unavailable" }), { status: 503 }),
    );
    const { result } = renderHook(() => useBackendVersion(), { wrapper });
    await waitFor(() => expect(result.current.data).toBe("0.1.0"));
  });

  it("returns an empty string when the request fails, with no retry", async () => {
    fetchMock.mockRejectedValue(new TypeError("network"));
    const { result } = renderHook(() => useBackendVersion(), { wrapper });
    await waitFor(() => expect(result.current.data).toBe(""));
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("aborts after 10 seconds and returns an empty string", async () => {
    vi.useFakeTimers();
    let signal: AbortSignal | undefined;
    fetchMock.mockImplementation(
      (_url: string, init: RequestInit) =>
        new Promise((_resolve, reject) => {
          signal = init.signal as AbortSignal;
          signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
        }),
    );
    renderHook(() => useBackendVersion(), { wrapper });
    await vi.advanceTimersByTimeAsync(9999);
    expect(signal?.aborted).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(signal?.aborted).toBe(true);
    expect(client.getQueryData(keys.healthz)).toBe("");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
