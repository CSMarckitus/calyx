import type { QueryClient } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";

export type HealthState = "checking" | "ok" | "core" | "database" | "backend";

export type Health = {
  state: HealthState;
  // False when the data on the screen can be older than the state of the backend.
  dataIsFresh: boolean;
  // Time of the last answer. Time of the last answer in the state `ok`.
  lastAnswerAt?: number;
  lastGoodAt?: number;
  // null: no answer.
  database: "ok" | "unavailable" | null;
  core: "ok" | "unreachable" | "unknown" | null;
  mail: "configured" | "off" | null;
  // Empty: no answer, or an older backend that sends no version.
  version: string;
  coreVersion: string;
};

// `recovered`: the state is `ok` or `core` after `backend` or `database`, also with `checking` between.
export type Check = { state: HealthState; recovered: boolean };

export type HealthStore = {
  getSnapshot(): Health;
  subscribe(listener: () => void): () => void;
  onCheck(listener: (check: Check) => void): () => void;
  start(): void;
  stop(): void;
  checkNow(): void;
  // A request was unreachable (a read, a change or a stream). Checks, with a gap since the last check.
  requestUnreachable(): void;
};

export type Answer = {
  database: "ok" | "unavailable";
  core: "ok" | "unreachable" | "unknown";
  mail: "configured" | "off";
  version: string;
  core_version: string;
};

const pollMs = 15_000;
const limitMs = 5000;
const secondCheckMs = 2000;
// A check after an unreachable read waits this long since the last check. Else a read that fails
// at each check would start a check at each failure.
const errorGapMs = 2000;
const hiddenLongMs = 30_000;

const databases = ["ok", "unavailable"];
const cores = ["ok", "unreachable", "unknown"];
const mails = ["configured", "off"];

// `/healthz` is not in backend.json, so the answer is checked by hand. Anything else is "no answer".
function parse(text: string): Answer | null {
  let body: Partial<Record<keyof Answer, unknown>> | null;
  try {
    body = JSON.parse(text);
  } catch {
    return null;
  }
  if (
    typeof body?.database !== "string" ||
    !databases.includes(body.database) ||
    typeof body.core !== "string" ||
    !cores.includes(body.core) ||
    typeof body.mail !== "string" ||
    !mails.includes(body.mail)
  )
    return null;
  // An older backend sends no versions: an absent field is an empty string.
  const asText = (v: unknown) => (typeof v === "string" ? v : "");
  return {
    database: body.database,
    core: body.core,
    mail: body.mail,
    version: asText(body.version),
    core_version: asText(body.core_version),
  } as Answer;
}

// One read of `/healthz`: the body of a 200 or a 503 answer, or null when it is not usable.
export async function readHealthz(signal: AbortSignal): Promise<Answer | null> {
  try {
    const res = await fetch("/healthz", { credentials: "same-origin", signal });
    return parse(await res.text());
  } catch {
    return null;
  }
}

// The first rule that matches wins.
function stateOf(a: Answer): HealthState {
  if (a.database === "unavailable") return "database";
  if (a.core === "unreachable") return "core";
  if (a.core === "ok" && a.database === "ok") return "ok";
  return "checking";
}

const bad = (s: HealthState) => s === "backend" || s === "database";
const good = (s: HealthState) => s === "ok" || s === "core";

export function createHealth(
  options: { client?: QueryClient; now?: () => number } = {},
): HealthStore {
  const { client } = options;
  const now = options.now ?? Date.now;

  let snapshot: Health = {
    state: "checking",
    dataIsFresh: true,
    database: null,
    core: null,
    mail: null,
    version: "",
    coreVersion: "",
  };
  let lastAnswerAt: number | undefined;
  let lastGoodAt: number | undefined;
  let state: HealthState = "checking";
  let answer: Answer | null = null;

  const listeners = new Set<() => void>();
  const checkListeners = new Set<(c: Check) => void>();

  let running = false;
  let inFlight = false;
  // Each stop() ends a generation: a check of an older one must not touch the new one.
  let generation = 0;
  let misses = 0;
  let lastCheckAt = Number.NEGATIVE_INFINITY;
  let poll: ReturnType<typeof setInterval> | undefined;
  let second: ReturnType<typeof setTimeout> | undefined;
  let stopWatching: (() => void) | undefined;
  let control: AbortController | undefined;

  // Freshness of the data.
  let needNewRead = false; // the state was `backend` or `database`
  let recoveredAt: number | undefined; // the state became `ok` or `core` after that
  let listFailed = false;
  let readStart: number | undefined; // start of the read of the list in flight
  let lastOkStart: number | undefined; // start of the last read of the list that succeeded
  let hiddenAt: number | undefined;
  let returnedAt: number | undefined; // the page came back after more than 30 s

  const visible = () => document.visibilityState === "visible";

  function fresh(): boolean {
    if (bad(state) || listFailed) return false;
    const since = (t: number | undefined) => t !== undefined && (lastOkStart ?? -1) >= t;
    if (needNewRead && !since(recoveredAt)) return false;
    if (returnedAt !== undefined && !since(returnedAt)) return false;
    return true;
  }

  function publish() {
    const next: Health = {
      state,
      dataIsFresh: fresh(),
      database: answer?.database ?? null,
      core: answer?.core ?? null,
      mail: answer?.mail ?? null,
      version: answer?.version ?? "",
      coreVersion: answer?.core_version ?? "",
      ...(lastAnswerAt !== undefined ? { lastAnswerAt } : {}),
      ...(lastGoodAt !== undefined ? { lastGoodAt } : {}),
    };
    const same = (Object.keys(next) as (keyof Health)[]).every((k) => next[k] === snapshot[k]);
    if (same && Object.keys(snapshot).length === Object.keys(next).length) return;
    snapshot = next;
    for (const l of [...listeners]) l();
  }

  function settle(next: HealthState, a: Answer | null) {
    state = next;
    answer = a;
    if (bad(next)) {
      needNewRead = true;
      recoveredAt = undefined;
    } else if (good(next) && needNewRead && recoveredAt === undefined) {
      recoveredAt = now();
    }
  }

  async function once(): Promise<Answer | null> {
    control = new AbortController();
    const timer = setTimeout(() => control?.abort(), limitMs);
    try {
      return await readHealthz(control.signal);
    } finally {
      clearTimeout(timer);
    }
  }

  function checkNow() {
    if (!running || inFlight) return;
    clearTimeout(second);
    second = undefined;
    inFlight = true;
    lastCheckAt = now();
    const mine = generation;
    void once().then((a) => {
      if (mine !== generation) return;
      inFlight = false;
      if (!running) return;
      if (a === null) {
        misses++;
        if (misses < 2) {
          second = setTimeout(checkNow, secondCheckMs);
          return;
        }
        finish("backend", null);
        return;
      }
      misses = 0;
      lastAnswerAt = now();
      if (stateOf(a) === "ok") lastGoodAt = lastAnswerAt;
      finish(stateOf(a), a);
    });
  }

  // A check ended with a state. `recovered` is true once for each return, so a listener reads all
  // queries one time. `checking` between a bad state and a good one keeps `wasBad`.
  let wasBad = false;
  function finish(next: HealthState, a: Answer | null) {
    if (bad(next)) wasBad = true;
    const recovered = good(next) && wasBad;
    if (good(next)) wasBad = false;
    settle(next, a);
    publish();
    for (const l of [...checkListeners]) l({ state: next, recovered });
  }

  function schedule() {
    clearInterval(poll);
    poll = undefined;
    if (running && visible()) poll = setInterval(checkNow, pollMs);
  }

  function onVisibility() {
    if (visible()) {
      if (hiddenAt !== undefined && now() - hiddenAt > hiddenLongMs) returnedAt = now();
      hiddenAt = undefined;
      publish();
      checkNow();
    } else {
      hiddenAt ??= now();
    }
    schedule();
  }

  function watchCache() {
    if (!client) return () => undefined;
    const stopQueries = client.getQueryCache().subscribe((event) => {
      if (event.type !== "updated") return;
      const action = event.action;
      if (event.query.queryKey[0] === "instances") {
        if (action.type === "fetch") readStart = now();
        else if (action.type === "success") {
          listFailed = false;
          lastOkStart = readStart ?? now();
          publish();
        } else if (action.type === "error") {
          listFailed = true;
          publish();
        }
      }
      if (action.type === "error") unreachable(action.error);
    });
    const stopMutations = client.getMutationCache().subscribe((event) => {
      if (event.type === "updated" && event.action.type === "error")
        unreachable(event.action.error);
    });
    return () => {
      stopQueries();
      stopMutations();
    };
  }

  function unreachable(error: unknown) {
    if ((error as { kind?: string } | null)?.kind !== "unreachable") return;
    if (now() - lastCheckAt >= errorGapMs) checkNow();
  }

  return {
    getSnapshot: () => snapshot,
    subscribe(listener) {
      listeners.add(listener);
      return () => void listeners.delete(listener);
    },
    onCheck(listener) {
      checkListeners.add(listener);
      return () => void checkListeners.delete(listener);
    },
    start() {
      if (running) return;
      running = true;
      document.addEventListener("visibilitychange", onVisibility);
      stopWatching = watchCache();
      if (!visible()) hiddenAt = now();
      schedule();
      if (visible()) checkNow();
    },
    stop() {
      running = false;
      generation++;
      inFlight = false;
      misses = 0;
      clearInterval(poll);
      clearTimeout(second);
      control?.abort();
      document.removeEventListener("visibilitychange", onVisibility);
      stopWatching?.();
    },
    checkNow,
    requestUnreachable: () => unreachable({ kind: "unreachable" }),
  };
}

// One store for the page. The page installs the one that has its query client, before the first render.
let current = createHealth();
export function installHealth(store: HealthStore) {
  current = store;
}
export const getHealth = () => current;

export function useHealth(): Health {
  return useSyncExternalStore(
    (listener) => current.subscribe(listener),
    () => current.getSnapshot(),
  );
}
