import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createHealth, type HealthStore, readHealthz } from "./health.ts";

type Body = { database: string; core: string; mail: string };
const good: Body = { database: "ok", core: "ok", mail: "configured" };

const fetchMock = vi.fn<(url: string, init: RequestInit) => Promise<Response>>();
const json = (body: unknown, status = 200) =>
  Promise.resolve(new Response(JSON.stringify(body), { status }));
// A check that never answers. It ends only when the limit of 5 s aborts it.
const silent = (_url: string, init: RequestInit) =>
  new Promise<Response>((_, reject) => {
    init.signal?.addEventListener("abort", () => reject(new Error("aborted")));
  });

let visible = true;
function setVisible(value: boolean) {
  visible = value;
  document.dispatchEvent(new Event("visibilitychange"));
}

let client: QueryClient;
let store: HealthStore;
const answers = (body: Body | Promise<Response>) =>
  fetchMock.mockImplementation(() => (body instanceof Promise ? body : json(body)));
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms);

beforeEach(() => {
  vi.useFakeTimers({ now: 1_000_000 });
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
  visible = true;
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => (visible ? "visible" : "hidden"),
  });
  client = new QueryClient();
  store = createHealth({ client });
});
afterEach(() => {
  store.stop();
  client.clear();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("the state", () => {
  it("starts as checking, with no data", () => {
    expect(store.getSnapshot()).toEqual({
      state: "checking",
      dataIsFresh: true,
      database: null,
      core: null,
      mail: null,
      version: "",
      coreVersion: "",
    });
  });

  it.each([
    [{ database: "ok", core: "ok", mail: "configured" }, "ok"],
    [{ database: "ok", core: "unreachable", mail: "off" }, "core"],
    [{ database: "unavailable", core: "unknown", mail: "off" }, "database"],
    [{ database: "unavailable", core: "unreachable", mail: "off" }, "database"],
    [{ database: "ok", core: "unknown", mail: "off" }, "checking"],
  ])("maps %j to %s", async (body, state) => {
    answers(body);
    store.start();
    await tick(0);
    expect(store.getSnapshot()).toMatchObject({
      state,
      database: body.database,
      core: body.core,
      mail: body.mail,
    });
  });

  it("takes a 503 with the body of /healthz as an answer", async () => {
    fetchMock.mockImplementation(() =>
      json({ database: "unavailable", core: "unknown", mail: "off" }, 503),
    );
    store.start();
    await tick(0);
    expect(store.getSnapshot().state).toBe("database");
  });

  it("asks /healthz of the same origin", async () => {
    answers(good);
    store.start();
    await tick(0);
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/healthz");
    expect(fetchMock.mock.calls[0]?.[1].credentials).toBe("same-origin");
  });
});

describe("no answer", () => {
  it("needs two in a row, the second 2 s after the first", async () => {
    fetchMock.mockImplementation(() => Promise.reject(new TypeError("network")));
    store.start();
    await tick(0);
    expect(store.getSnapshot().state).toBe("checking");
    await tick(1900);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await tick(100);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(store.getSnapshot()).toMatchObject({
      state: "backend",
      database: null,
      core: null,
      mail: null,
    });
  });

  it("does not change the state after one miss followed by an answer", async () => {
    answers(good);
    store.start();
    await tick(0);
    fetchMock.mockImplementationOnce(() => Promise.reject(new TypeError("network")));
    await tick(15_000);
    expect(store.getSnapshot().state).toBe("ok");
    answers(good);
    await tick(2000);
    expect(store.getSnapshot().state).toBe("ok");
  });

  it("counts a check that lasts more than 5 s as a miss", async () => {
    fetchMock.mockImplementation(silent);
    store.start();
    await tick(4900);
    expect(store.getSnapshot().state).toBe("checking");
    await tick(100);
    await tick(2000);
    await tick(5000);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(store.getSnapshot().state).toBe("backend");
  });

  it("counts a body that is not the JSON of /healthz as a miss", async () => {
    for (const text of [
      "<html>bad gateway</html>",
      "{}",
      '{"database":"ok","core":"maybe","mail":"off"}',
      "null",
    ]) {
      fetchMock.mockReset();
      fetchMock.mockImplementation(() => Promise.resolve(new Response(text, { status: 502 })));
      const s = createHealth({ client });
      s.start();
      await tick(2000);
      expect(s.getSnapshot().state).toBe("backend");
      s.stop();
    }
  });

  it("keeps the last good time, and shows the time of the last answer", async () => {
    answers(good);
    store.start();
    await tick(0);
    const t = Date.now();
    expect(store.getSnapshot()).toMatchObject({ lastGoodAt: t, lastAnswerAt: t });
    answers({ database: "ok", core: "unreachable", mail: "off" });
    await tick(15_000);
    expect(store.getSnapshot()).toMatchObject({
      state: "core",
      lastGoodAt: t,
      lastAnswerAt: t + 15_000,
    });
    fetchMock.mockImplementation(() => Promise.reject(new TypeError("network")));
    await tick(15_000 + 2000);
    expect(store.getSnapshot()).toMatchObject({
      state: "backend",
      lastGoodAt: t,
      lastAnswerAt: t + 15_000,
    });
  });
});

describe("the schedule", () => {
  it("checks each 15 s while the page is visible", async () => {
    answers(good);
    store.start();
    await tick(0);
    await tick(45_000);
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("does not check while hidden, and checks when it becomes visible", async () => {
    answers(good);
    store.start();
    await tick(0);
    setVisible(false);
    await tick(60_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    setVisible(true);
    await tick(0);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("runs one check at a time", async () => {
    let end!: (r: Response) => void;
    fetchMock.mockImplementation(
      () =>
        new Promise<Response>((r) => {
          end = r;
        }),
    );
    store.start();
    store.checkNow();
    store.checkNow();
    await tick(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    end(new Response(JSON.stringify(good)));
    await tick(0);
    store.checkNow();
    await tick(0);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("checks after a read that was unreachable, at most once in 2 s", async () => {
    answers(good);
    store.start();
    await tick(0);
    fetchMock.mockClear();
    await tick(2000);
    const fail = () =>
      client
        .fetchQuery({
          queryKey: ["x"],
          queryFn: () => Promise.reject({ kind: "unreachable" }),
          retry: false,
        })
        .catch(() => undefined);
    await fail();
    await fail();
    await tick(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    await tick(2000);
    await client
      .fetchQuery({
        queryKey: ["y"],
        queryFn: () => Promise.reject({ kind: "problem" }),
        retry: false,
      })
      .catch(() => undefined);
    await tick(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("stops all timers on stop", async () => {
    answers(good);
    store.start();
    await tick(0);
    store.stop();
    await tick(60_000);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("stop and start", () => {
  it("ignores the callback of a check that an older start began", async () => {
    let end!: (r: Response) => void;
    fetchMock.mockImplementationOnce(
      () =>
        new Promise<Response>((r) => {
          end = r;
        }),
    );
    store.start();
    await tick(0);
    store.stop();
    fetchMock.mockImplementation(() => new Promise<Response>(() => undefined));
    store.start();
    await tick(0);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    end(new Response("x"));
    await tick(2100);
    // The old check must not count a miss, and must not free the slot of the new check.
    expect(store.getSnapshot().state).toBe("checking");
    store.checkNow();
    await tick(0);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("checks for the request that the stream reports as unreachable", async () => {
    answers(good);
    store.start();
    await tick(2000);
    fetchMock.mockClear();
    store.requestUnreachable();
    await tick(0);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("the checks", () => {
  it("tells the listeners of each check, and if the page came back", async () => {
    const seen: unknown[] = [];
    store.onCheck((c) => seen.push(c));
    answers({ database: "ok", core: "unreachable", mail: "off" });
    store.start();
    await tick(0);
    answers({ database: "unavailable", core: "unknown", mail: "off" });
    await tick(15_000);
    answers({ database: "ok", core: "unknown", mail: "off" });
    await tick(15_000);
    answers(good);
    await tick(15_000);
    answers(good);
    await tick(15_000);
    expect(seen).toEqual([
      { state: "core", recovered: false },
      { state: "database", recovered: false },
      { state: "checking", recovered: false },
      { state: "ok", recovered: true },
      { state: "ok", recovered: false },
    ]);
  });

  it("tells the listeners that a return from backend is recovered", async () => {
    const seen: unknown[] = [];
    store.onCheck((c) => seen.push(c));
    fetchMock.mockImplementation(() => Promise.reject(new TypeError("network")));
    store.start();
    await tick(2000);
    answers({ database: "ok", core: "unreachable", mail: "off" });
    await tick(15_000);
    expect(seen).toEqual([
      { state: "backend", recovered: false },
      { state: "core", recovered: true },
    ]);
  });
});

describe("dataIsFresh", () => {
  // A read of the list that the test ends by hand.
  function startRead() {
    let settle!: (failed: boolean) => void;
    const done = client
      .fetchQuery({
        queryKey: ["instances"],
        staleTime: 0,
        retry: false,
        queryFn: () =>
          new Promise((resolve, reject) => {
            settle = (failed) => (failed ? reject({ kind: "unreachable" }) : resolve([]));
          }),
      })
      .catch(() => undefined);
    return {
      end: async (failed = false) => {
        settle(failed);
        await done;
      },
    };
  }

  it("is false while the state is backend or database", async () => {
    fetchMock.mockImplementation(() => Promise.reject(new TypeError("network")));
    store.start();
    await tick(2000);
    expect(store.getSnapshot().dataIsFresh).toBe(false);
    answers({ database: "unavailable", core: "unknown", mail: "off" });
    await tick(15_000);
    expect(store.getSnapshot().dataIsFresh).toBe(false);
  });

  it("stays false after the return, also with checking between, until a read that started after it succeeds", async () => {
    answers({ database: "unavailable", core: "unknown", mail: "off" });
    store.start();
    await tick(0);
    answers({ database: "ok", core: "unknown", mail: "off" });
    await tick(15_000);
    expect(store.getSnapshot()).toMatchObject({ state: "checking", dataIsFresh: false });
    // A read that started before the return does not count.
    const old = startRead();
    await tick(15_000);
    answers(good);
    await tick(15_000);
    expect(store.getSnapshot().state).toBe("ok");
    await old.end();
    expect(store.getSnapshot().dataIsFresh).toBe(false);
    await startRead().end();
    expect(store.getSnapshot().dataIsFresh).toBe(true);
  });

  it("is false when the last read of the list failed, and true after one that succeeds", async () => {
    answers(good);
    store.start();
    await tick(0);
    await startRead().end(true);
    expect(store.getSnapshot().dataIsFresh).toBe(false);
    await startRead().end();
    expect(store.getSnapshot().dataIsFresh).toBe(true);
  });

  it("is false after a page hidden for more than 30 s, until a read that started after the return succeeds", async () => {
    answers(good);
    store.start();
    await tick(0);
    setVisible(false);
    await tick(31_000);
    setVisible(true);
    expect(store.getSnapshot().dataIsFresh).toBe(false);
    await startRead().end();
    expect(store.getSnapshot().dataIsFresh).toBe(true);
  });

  it("stays true after a page hidden for 30 s or less", async () => {
    answers(good);
    store.start();
    await tick(0);
    setVisible(false);
    await tick(30_000);
    setVisible(true);
    expect(store.getSnapshot().dataIsFresh).toBe(true);
  });
});

describe("the subscription", () => {
  it("gives the same snapshot until something changes, and calls the listeners then", async () => {
    answers(good);
    const calls = vi.fn();
    const stop = store.subscribe(calls);
    store.start();
    await tick(0);
    const first = store.getSnapshot();
    expect(store.getSnapshot()).toBe(first);
    expect(calls).toHaveBeenCalledTimes(1);
    await tick(15_000);
    expect(store.getSnapshot()).not.toBe(first);
    expect(calls).toHaveBeenCalledTimes(2);
    stop();
    await tick(15_000);
    expect(calls).toHaveBeenCalledTimes(2);
  });
});

describe("the versions", () => {
  const withVersions = { ...good, version: "0.1.0", core_version: "0.1.1" };

  it("reads the versions of a 200 answer", async () => {
    answers(withVersions);
    store.start();
    await tick(0);
    expect(store.getSnapshot()).toMatchObject({ version: "0.1.0", coreVersion: "0.1.1" });
  });

  it("takes an absent field as an empty string", async () => {
    answers(good);
    store.start();
    await tick(0);
    expect(store.getSnapshot()).toMatchObject({ version: "", coreVersion: "" });
  });

  it("takes a version that is not a string as an empty string", async () => {
    fetchMock.mockImplementation(() => json({ ...good, version: 1, core_version: null }));
    expect(await readHealthz(new AbortController().signal)).toMatchObject({
      version: "",
      core_version: "",
    });
  });

  it("readHealthz returns the body of a 200 answer", async () => {
    answers(withVersions);
    expect(await readHealthz(new AbortController().signal)).toEqual({
      ...withVersions,
    });
  });

  it("readHealthz returns the body of a 503 answer", async () => {
    fetchMock.mockImplementation(() =>
      json({ database: "unavailable", core: "unknown", mail: "off", version: "dev" }, 503),
    );
    expect(await readHealthz(new AbortController().signal)).toMatchObject({
      database: "unavailable",
      version: "dev",
      core_version: "",
    });
  });

  it("readHealthz returns null when the answer is not usable", async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response("oops", { status: 502 })));
    expect(await readHealthz(new AbortController().signal)).toBeNull();
    fetchMock.mockImplementation(() => Promise.reject(new TypeError("network")));
    expect(await readHealthz(new AbortController().signal)).toBeNull();
  });
});
