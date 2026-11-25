import type { Check, HealthStore } from "./health.ts";
import type { createReads } from "./queries.ts";
import { stream } from "./request.ts";

// The wait after a `changed`, and the waits before the stream opens again.
const waitMs = 300;
const reopenMs = [1000, 2000, 5000, 10_000, 30_000];

type Options = {
  reads: Pick<ReturnType<typeof createReads>, "refresh" | "refreshFailed">;
  health: Pick<HealthStore, "checkNow" | "onCheck" | "requestUnreachable">;
  // A 401 of the stream: the login session ended.
  onUnauthorized: () => void;
};

// The stream `followEvents`. It is open only for a visible page with a user.
export function createLive({ reads, health, onUnauthorized }: Options) {
  let hasUser = false;
  // `replaced` keeps the stream closed until the person returns to the tab.
  let replaced = false;
  let control: AbortController | undefined;
  let open = false;
  let attempt = 0;
  let gotEvent = false;
  let reopen: ReturnType<typeof setTimeout> | undefined;
  let wait: ReturnType<typeof setTimeout> | undefined;

  const visible = () => document.visibilityState === "visible";
  const shouldRun = () => hasUser && visible() && !replaced;

  function refreshNow() {
    clearTimeout(wait);
    wait = undefined;
    reads.refresh();
    health.checkNow();
  }

  // A `changed` inside a wait does not extend it.
  function changed() {
    wait ??= setTimeout(refreshNow, waitMs);
  }

  function close() {
    clearTimeout(reopen);
    reopen = undefined;
    control?.abort();
    control = undefined;
    open = false;
  }

  function openStream() {
    if (open || !shouldRun()) return;
    clearTimeout(reopen);
    reopen = undefined;
    const mine = new AbortController();
    control = mine;
    open = true;
    gotEvent = false;
    void stream(
      "followEvents",
      {},
      (item) => {
        if (item.event === "changed") {
          gotEvent = true;
          changed();
        } else {
          replaced = true;
          close();
        }
      },
      mine.signal,
    ).then((result) => {
      // We closed it ourselves: nothing more to do.
      if (control !== mine) return;
      open = false;
      if (typeof result === "object" && result.kind === "problem" && result.status === 401) {
        onUnauthorized();
        return;
      }
      if (typeof result === "object" && result.kind === "unreachable") health.requestUnreachable();
      if (gotEvent) attempt = 0;
      const delay = reopenMs[Math.min(attempt, reopenMs.length - 1)] as number;
      attempt++;
      reopen = setTimeout(openStream, delay);
    });
  }

  function sync() {
    if (shouldRun()) openStream();
    else close();
  }

  function onVisibility() {
    // The person returns to the tab: a `replaced` ends here.
    if (visible()) replaced = false;
    sync();
  }
  document.addEventListener("visibilitychange", onVisibility);

  const stopChecks = health.onCheck((check: Check) => {
    if (!hasUser) return;
    if (check.recovered) {
      reads.refresh();
      // At once: the wait of the delay does not apply after a return.
      attempt = 0;
      openStream();
    } else if (check.state === "ok" || check.state === "core") {
      if (open) reads.refreshFailed();
      else reads.refresh();
    }
  });

  return {
    setUser(value: boolean) {
      hasUser = value;
      if (!value) {
        replaced = false;
        attempt = 0;
        clearTimeout(wait);
        wait = undefined;
      }
      sync();
    },
    isOpen: () => open,
    refreshNow,
    dispose() {
      document.removeEventListener("visibilitychange", onVisibility);
      stopChecks();
      clearTimeout(wait);
      wait = undefined;
      close();
    },
  };
}
