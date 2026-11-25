import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Check } from "./health.ts";
import type { Failure } from "./request.ts";

type Item = { event: "changed" | "replaced"; data: unknown };
type Open = {
  signal: AbortSignal;
  send: (item: Item) => void;
  end: (result: "ended" | Failure) => void;
};

const opens: Open[] = [];
const stream = vi.hoisted(() => vi.fn());
vi.mock("./request.ts", () => ({ stream }));

import { createLive } from "./live.ts";

const changed: Item = { event: "changed", data: {} };
const replaced: Item = { event: "replaced", data: {} };
const unauthorized: Failure = {
  kind: "problem",
  status: 401,
  code: "unauthorized",
  detail: "",
  fields: [],
  requestId: "",
};

let visible = true;
function setVisible(value: boolean) {
  visible = value;
  document.dispatchEvent(new Event("visibilitychange"));
}

const reads = { refresh: vi.fn(), refreshFailed: vi.fn() };
let checkListener: (c: Check) => void;
const health = {
  checkNow: vi.fn(),
  requestUnreachable: vi.fn(),
  onCheck: vi.fn((l: (c: Check) => void) => {
    checkListener = l;
    return () => undefined;
  }),
};
const onUnauthorized = vi.fn();
const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms);
const last = () => opens[opens.length - 1] as Open;

let live: ReturnType<typeof createLive>;
beforeEach(() => {
  vi.useFakeTimers();
  opens.length = 0;
  visible = true;
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => (visible ? "visible" : "hidden"),
  });
  for (const f of [
    reads.refresh,
    reads.refreshFailed,
    health.checkNow,
    health.requestUnreachable,
    onUnauthorized,
  ])
    f.mockClear();
  // The fake stream ends with "ended" when the caller aborts, as the real one does.
  stream.mockReset();
  stream.mockImplementation(
    (_op: string, _p: unknown, onEvent: (i: Item) => void, signal: AbortSignal) =>
      new Promise((resolve) => {
        const open: Open = { signal, send: onEvent, end: resolve };
        opens.push(open);
        signal.addEventListener("abort", () => resolve("ended"));
      }),
  );
  live = createLive({ reads, health, onUnauthorized });
});
afterEach(() => {
  live.dispose();
  vi.useRealTimers();
});

describe("when the stream is open", () => {
  it("is closed with no user", async () => {
    await tick(0);
    expect(opens).toHaveLength(0);
    expect(live.isOpen()).toBe(false);
  });

  it("opens followEvents when there is a user and the page is visible", async () => {
    live.setUser(true);
    expect(stream).toHaveBeenCalledWith(
      "followEvents",
      {},
      expect.any(Function),
      expect.any(AbortSignal),
    );
    expect(live.isOpen()).toBe(true);
  });

  it("does not open for a hidden page, and opens when the page becomes visible", async () => {
    visible = false;
    live.setUser(true);
    expect(opens).toHaveLength(0);
    setVisible(true);
    expect(opens).toHaveLength(1);
  });

  it("closes when the page becomes hidden, and opens again when it is visible", async () => {
    live.setUser(true);
    setVisible(false);
    expect(last().signal.aborted).toBe(true);
    await tick(60_000);
    expect(opens).toHaveLength(1);
    setVisible(true);
    expect(opens).toHaveLength(2);
    expect(live.isOpen()).toBe(true);
  });

  it("closes when the user goes", async () => {
    live.setUser(true);
    live.setUser(false);
    expect(last().signal.aborted).toBe(true);
    expect(live.isOpen()).toBe(false);
    await tick(60_000);
    expect(opens).toHaveLength(1);
  });
});

describe("changed", () => {
  it("starts a wait of 300 ms, then marks the queries and checks the health", async () => {
    live.setUser(true);
    last().send(changed);
    await tick(299);
    expect(reads.refresh).not.toHaveBeenCalled();
    await tick(1);
    expect(reads.refresh).toHaveBeenCalledTimes(1);
    expect(health.checkNow).toHaveBeenCalledTimes(1);
  });

  it("does not extend the wait with a second changed", async () => {
    live.setUser(true);
    last().send(changed);
    await tick(200);
    last().send(changed);
    await tick(100);
    expect(reads.refresh).toHaveBeenCalledTimes(1);
    await tick(1000);
    expect(reads.refresh).toHaveBeenCalledTimes(1);
  });

  it("starts a new wait with a changed after the end of the first", async () => {
    live.setUser(true);
    last().send(changed);
    await tick(300);
    last().send(changed);
    await tick(300);
    expect(reads.refresh).toHaveBeenCalledTimes(2);
  });

  it("is read at once for a change of the person", () => {
    live.setUser(true);
    live.refreshNow();
    expect(reads.refresh).toHaveBeenCalledTimes(1);
    expect(health.checkNow).toHaveBeenCalledTimes(1);
  });

  it("has no read after the stream closes", async () => {
    live.setUser(true);
    last().send(changed);
    live.setUser(false);
    await tick(1000);
    expect(reads.refresh).not.toHaveBeenCalled();
  });
});

describe("replaced", () => {
  it("closes the stream until the person returns to the tab", async () => {
    live.setUser(true);
    last().send(replaced);
    expect(last().signal.aborted).toBe(true);
    expect(live.isOpen()).toBe(false);
    await tick(60_000);
    expect(opens).toHaveLength(1);
    setVisible(false);
    setVisible(true);
    expect(opens).toHaveLength(2);
  });
});

describe("the end of a stream", () => {
  it("asks the health for a check when it ends as unreachable, and not for another end", async () => {
    live.setUser(true);
    last().end("ended");
    await tick(1000);
    expect(health.requestUnreachable).not.toHaveBeenCalled();
    last().end({ kind: "unreachable" });
    await tick(0);
    expect(health.requestUnreachable).toHaveBeenCalledTimes(1);
  });

  it("ends the login session on a 401, and does not open again", async () => {
    live.setUser(true);
    last().end(unauthorized);
    await tick(60_000);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(opens).toHaveLength(1);
  });

  it("opens again after 1, 2, 5, 10 and then 30 s, for each other end", async () => {
    live.setUser(true);
    const ends: ("ended" | Failure)[] = [
      "ended",
      { kind: "unreachable" },
      { kind: "timeout" },
      { ...unauthorized, status: 502, code: "bad_gateway" },
      { kind: "unreachable" },
      "ended",
    ];
    const waits = [1000, 2000, 5000, 10_000, 30_000, 30_000];
    for (const [i, result] of ends.entries()) {
      const count = opens.length;
      last().end(result);
      await tick((waits[i] as number) - 1);
      expect(opens).toHaveLength(count);
      await tick(1);
      expect(opens).toHaveLength(count + 1);
    }
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("starts again at 1 s after a stream that gave an event", async () => {
    live.setUser(true);
    last().end("ended");
    await tick(1000);
    last().end("ended");
    await tick(2000);
    last().send(changed);
    last().end("ended");
    await tick(999);
    expect(opens).toHaveLength(3);
    await tick(1);
    expect(opens).toHaveLength(4);
  });

  it("does not open again after the page is hidden during the wait", async () => {
    live.setUser(true);
    last().end("ended");
    setVisible(false);
    await tick(60_000);
    expect(opens).toHaveLength(1);
  });
});

describe("the checks of the health", () => {
  const check = (state: Check["state"], recovered = false) => checkListener({ state, recovered });

  it("read all queries and open the stream at once after a return", async () => {
    live.setUser(true);
    last().end({ kind: "unreachable" });
    await tick(0);
    reads.refresh.mockClear();
    check("ok", true);
    expect(reads.refresh).toHaveBeenCalledTimes(1);
    expect(opens).toHaveLength(2);
    await tick(5000);
    expect(opens).toHaveLength(2);
  });

  it("do not open the stream after a return when it was replaced", () => {
    live.setUser(true);
    last().send(replaced);
    check("core", true);
    expect(reads.refresh).toHaveBeenCalled();
    expect(opens).toHaveLength(1);
  });

  it("read again only the reads that failed, while the stream is open", () => {
    live.setUser(true);
    check("ok");
    expect(reads.refreshFailed).toHaveBeenCalledTimes(1);
    expect(reads.refresh).not.toHaveBeenCalled();
  });

  it("read all queries while no stream is open", async () => {
    live.setUser(true);
    last().end({ kind: "unreachable" });
    await tick(0);
    check("core");
    expect(reads.refresh).toHaveBeenCalledTimes(1);
    expect(reads.refreshFailed).not.toHaveBeenCalled();
  });

  it("read nothing in another state, or with no user", () => {
    live.setUser(true);
    for (const state of ["checking", "database", "backend"] as const) check(state);
    live.setUser(false);
    check("ok");
    check("ok", true);
    expect(reads.refresh).not.toHaveBeenCalled();
    expect(reads.refreshFailed).not.toHaveBeenCalled();
    expect(opens).toHaveLength(1);
  });
});

describe("dispose", () => {
  it("closes the stream and ends the timers", async () => {
    live.setUser(true);
    last().send(changed);
    live.dispose();
    expect(last().signal.aborted).toBe(true);
    await tick(60_000);
    expect(reads.refresh).not.toHaveBeenCalled();
    expect(opens).toHaveLength(1);
  });
});
