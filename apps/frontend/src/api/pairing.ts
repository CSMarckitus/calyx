import { dialCode } from "../domain/countries.ts";
import {
  clearStarted,
  type Draft,
  endDraft,
  markStarted,
  moveDraft,
  readDraft,
  saveDraft,
  startedRecently,
  type Where,
} from "../domain/draft.ts";
import type { PairingFailure } from "../domain/errors.ts";
import { autoLocale } from "../domain/locale.ts";
import { fullNumber } from "../domain/phone.ts";
import type { components } from "./generated/types.ts";
import type { JsonOperationId } from "./operations.ts";
import { type Failure, request, stream } from "./request.ts";

type Instance = components["schemas"]["Instance"];
type Update = components["schemas"]["Pairing"];
type CreateBody = components["schemas"]["CreateInstanceRequest"];
type PairBody = components["schemas"]["PairInstanceRequest"];

// What the step "Link the phone" shows. The screens hold the texts.
export type PairingView =
  // No attempt: the steps before Start, also after "Try again" and the other method.
  | { step: "ready" }
  | { step: "starting" }
  | { step: "waiting"; message: string }
  | { step: "code"; code: string; message: string }
  | { step: "qr"; qr: string; message: string }
  // "Checking the link": no answer of the stream. It attaches again, and never says "failed".
  | { step: "checking" }
  | { step: "connected" }
  | { step: "failed"; failure: PairingFailure }
  // A 404 of the stream, and then:
  | { step: "notLinked" } // a new instance not found: "The phone was not linked. Nothing was saved."
  | { step: "removed" } // a reconnection not found: "This number was removed."
  | { step: "linked"; instance: Instance } // "The phone is linked", with the word of describe()
  | { step: "ending" } // creating: "Calyx ends this attempt. It can take 5 minutes"
  | { step: "instance"; instance: Instance } // the instance as describe() says
  // createInstance gave no answer: the screen reads the list and says "Calyx did not confirm the start…".
  | { step: "lostStart" }
  | { step: "refused"; failure: Failure; operationId: JsonOperationId }
  | { step: "cancelling" }
  | { step: "cancelled" };

export type PairingOptions = {
  userId: string;
  // The client of the address. Absent when the page does not know it (a reload of an instance
  // that is gone): the draft then gives it.
  clientId: string | undefined;
  // Absent for a new instance before Start.
  instanceId?: string;
  reconnect: boolean;
  // The stored locale of the instance, for a reconnection. null: Start sends the automatic one.
  // Absent: Start sends none, so the backend keeps the stored one.
  instanceLocale?: string | null;
  // A 401: the login session ended.
  onUnauthorized: () => void;
  // The screen replaces the address with the one of the instance, or of the client.
  navigate: (to: Where) => void;
};

// The waits before the stream opens again.
const reopenMs = [1000, 2000, 5000, 10_000, 30_000];
// A drawer that shows an instance in `creating` attaches at most once in this time.
const creatingEveryMs = 15_000;

const linkedStatuses = new Set(["CONNECTED", "CONNECTING", "DISCONNECTED"]);

const is401 = (f: Failure) => f.kind === "problem" && f.status === 401;
const is404 = (f: Failure) => f.kind === "problem" && f.status === 404;
// The change can have happened: the page must not say that it did not.
const isLost = (f: Failure) => f.kind !== "problem" || f.code.endsWith("_outcome_unknown");

// A coarse pointer is a phone with WhatsApp on it: a code. Any other: a QR.
export function autoMethod(): "code" | "qr" {
  return globalThis.matchMedia?.("(pointer: coarse)").matches ? "code" : "qr";
}

function method(draft: Draft): Pick<PairBody, "pairing" | "phone"> {
  const pairing = draft.method ?? autoMethod();
  if (pairing === "qr" || draft.phone === null) return { pairing };
  return { pairing, phone: fullNumber(draft.phone.text, dialCode(draft.phone.country)) };
}

export function createBody(
  draft: Draft,
  clientId: string,
  preferred?: readonly string[],
): CreateBody {
  return {
    provider: "WHATSAPP_WEB",
    client_id: clientId,
    display_name: draft.name,
    send_only: draft.sendOnly,
    locale: draft.locale ?? autoLocale(preferred),
    ...(draft.device ? { device_identity: draft.device } : {}),
    ...method(draft),
  };
}

// Without a locale the core keeps the stored one. An instance with none (null) needs one.
// undefined: the page does not know the stored one, so it sends none.
export function pairBody(
  draft: Draft,
  instanceLocale: string | null | undefined,
  preferred?: readonly string[],
): PairBody {
  return {
    ...method(draft),
    ...(instanceLocale === null ? { locale: autoLocale(preferred) } : {}),
  };
}

// The pairing of one drawer: Start, Attach, Cancel and the other method. The screen calls
// open() when the drawer opens and close() when it closes. Close does not stop the pairing.
export function createPairing(options: PairingOptions) {
  const { userId, reconnect, onUnauthorized, navigate } = options;
  let id = options.instanceId;
  let view: PairingView = { step: "ready" };
  const listeners = new Set<() => void>();
  let closed = true;
  // A start or a cancel runs: no other rule attaches.
  let busy = false;
  let control: AbortController | undefined;
  let reopen: ReturnType<typeof setTimeout> | undefined;
  let attempt = 0;
  let lastCreatingAttach = Number.NEGATIVE_INFINITY;

  const kind = reconnect ? "reconnect" : "new";
  const here = (): Where | undefined => (id === undefined ? undefined : { instance: id });
  const clientOf = () => {
    const where = here();
    return (where && readDraft(where, userId)?.clientId) ?? options.clientId;
  };
  const visible = () => document.visibilityState === "visible";

  function set(next: PairingView) {
    view = next;
    for (const listener of listeners) listener();
  }

  function stopStream() {
    clearTimeout(reopen);
    reopen = undefined;
    control?.abort();
    control = undefined;
  }

  function unauthorized() {
    stopStream();
    onUnauthorized();
  }

  function started() {
    const where = here();
    const clientId = clientOf();
    if (where && clientId !== undefined) markStarted(where, { userId, clientId, kind }, Date.now());
  }

  function unstart() {
    const where = here();
    if (where) clearStarted(where);
  }

  function recent() {
    const where = here();
    return where !== undefined && startedRecently(where, userId, Date.now());
  }

  function connected() {
    const where = here();
    if (where) endDraft(where);
    set({ step: "connected" });
  }

  // "Connected" ends the draft, from the first update or from the stream.
  function show(update: Update) {
    if (update.status === "CONNECTED") connected();
    else if (update.pairing_code !== null)
      set({ step: "code", code: update.pairing_code, message: update.message });
    else if (update.qr_text !== null)
      set({ step: "qr", qr: update.qr_text, message: update.message });
    else set({ step: "waiting", message: update.message });
  }

  function later(gotEvent: boolean) {
    if (gotEvent) attempt = 0;
    const delay = reopenMs[Math.min(attempt, reopenMs.length - 1)] as number;
    attempt++;
    reopen = setTimeout(attach, delay);
  }

  function attach() {
    if (closed || id === undefined || control !== undefined || !visible()) return;
    clearTimeout(reopen);
    reopen = undefined;
    const mine = new AbortController();
    control = mine;
    const instanceId = id;
    let gotEvent = false;
    let final = false;
    void stream(
      "followPairing",
      { path: { id: instanceId } },
      (item) => {
        if (control !== mine) return;
        gotEvent = true;
        if (item.event === "update" && item.data.status === "CONNECTED") {
          // The draft ended: an end of the stream must not start it again.
          final = true;
          show(item.data);
        } else if (item.event === "update") {
          started();
          show(item.data);
        } else if (item.event === "done") {
          final = true;
          connected();
        } else {
          final = true;
          unstart();
          set({ step: "failed", failure: { kind: "pairing", ...item.data } });
        }
      },
      mine.signal,
    ).then((result) => {
      // We closed it ourselves: nothing more to do.
      if (control !== mine) return;
      control = undefined;
      if (final) return;
      if (result === "ended") {
        // The server answered 200, and the pairing goes on.
        started();
        later(gotEvent);
      } else if (is401(result)) unauthorized();
      else if (is404(result)) void readAfter404(instanceId);
      else {
        set({ step: "checking" });
        later(gotEvent);
      }
    });
  }

  // No pairing runs, and the backend keeps no failure. One read says what happened. It does not
  // attach, unless it gets no answer.
  async function readAfter404(instanceId: string) {
    const outcome = await request("getInstance", { path: { id: instanceId } });
    if (closed || id !== instanceId) return;
    if (!outcome.ok) {
      if (is401(outcome.failure)) unauthorized();
      else if (is404(outcome.failure)) {
        unstart();
        set({ step: reconnect ? "removed" : "notLinked" });
      } else {
        set({ step: "checking" });
        later(false);
      }
      return;
    }
    const instance = outcome.data as Instance;
    const wasRecent = recent();
    unstart();
    if (instance.state === "creating") set({ step: "ending" });
    else if (
      wasRecent &&
      instance.state === "active" &&
      instance.enabled &&
      !instance.status_stale &&
      linkedStatuses.has(instance.status ?? "")
    )
      set({ step: "linked", instance });
    else set({ step: "instance", instance });
  }

  function onVisibility() {
    if (!visible()) stopStream();
    else if (!busy && recent()) {
      attempt = 0;
      attach();
    }
  }

  // Cancels the attempt. True when it is cancelled. Otherwise the view says what is true.
  async function stopAttempt(): Promise<boolean> {
    const instanceId = id;
    if (instanceId === undefined) return false;
    busy = true;
    stopStream();
    set({ step: "cancelling" });
    try {
      const cancel = await request("cancelPairing", { path: { id: instanceId } });
      if (!cancel.ok && is401(cancel.failure)) {
        unauthorized();
        return false;
      }
      // Any other answer: the read says what happened.
      const read = await request("getInstance", { path: { id: instanceId } });
      if (read.ok) {
        const instance = read.data as Instance;
        if (instance.status === "NEEDS_AUTH") return true;
        set({ step: "instance", instance });
        return false;
      }
      if (is401(read.failure)) unauthorized();
      else if (is404(read.failure)) return true;
      else {
        set({ step: "checking" });
        attempt = 0;
        attach();
      }
      return false;
    } finally {
      busy = false;
    }
  }

  // The steps again, with the draft. A new instance goes back to the key and the address of its
  // client, so a new start never creates in another client.
  function back() {
    stopStream();
    unstart();
    const where = here();
    if (!reconnect && where) {
      const clientId = clientOf();
      if (clientId !== undefined) {
        moveDraft(where, { client: clientId });
        if (!closed) navigate({ client: clientId });
      }
      id = undefined;
    }
    set({ step: "ready" });
  }

  async function startNew(draft: Draft) {
    const clientId = options.clientId ?? draft.clientId;
    // The stored draft is the one that moves, so "Try again" keeps each choice.
    saveDraft({ client: clientId }, draft);
    const outcome = await request("createInstance", { body: createBody(draft, clientId) });
    if (!outcome.ok) {
      if (is401(outcome.failure)) unauthorized();
      else if (isLost(outcome.failure)) set({ step: "lostStart" });
      else set({ step: "refused", failure: outcome.failure, operationId: "createInstance" });
      return;
    }
    const answer = outcome.data as components["schemas"]["CreateInstanceResult"];
    moveDraft({ client: clientId }, { instance: answer.instance.id });
    id = answer.instance.id;
    started();
    if (!closed) navigate({ instance: id });
    show(answer.pairing);
  }

  async function startReconnect(draft: Draft, instanceId: string) {
    saveDraft({ instance: instanceId }, draft);
    const outcome = await request("pairInstance", {
      path: { id: instanceId },
      body: pairBody(draft, options.instanceLocale),
    });
    if (!outcome.ok) {
      const f = outcome.failure;
      if (is401(f)) unauthorized();
      else if (isLost(f) || (f.kind === "problem" && f.code === "pairing_in_progress"))
        set({ step: "checking" });
      else set({ step: "refused", failure: f, operationId: "pairInstance" });
      return;
    }
    const answer = outcome.data as components["schemas"]["PairResult"];
    started();
    show(answer.pairing);
  }

  return {
    view: () => view,
    instanceId: () => id,
    // For useSyncExternalStore.
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },

    // The drawer opens: it attaches when the draft says "started" less than 10 minutes ago.
    // An old code or QR does not show again: it is gone at the end of the step.
    open() {
      closed = false;
      document.addEventListener("visibilitychange", onVisibility);
      if (recent()) attach();
      else if (["code", "qr", "waiting", "checking"].includes(view.step)) set({ step: "ready" });
    },

    close() {
      closed = true;
      document.removeEventListener("visibilitychange", onVisibility);
      stopStream();
    },

    // Also after a 409 `pairing_in_progress` of any operation of the screen.
    attach() {
      attempt = 0;
      attach();
    },

    // The screen calls it when a read of the list ends, with the row of this instance or
    // undefined when the list does not have it.
    listRead(row: Instance | undefined) {
      if (view.step === "ending" && row === undefined) {
        set({ step: reconnect ? "removed" : "notLinked" });
        return;
      }
      if (busy || row?.state !== "creating" || control !== undefined) return;
      const now = Date.now();
      if (now - lastCreatingAttach < creatingEveryMs) return;
      lastCreatingAttach = now;
      attempt = 0;
      attach();
    },

    // createInstance for a new instance, pairInstance for a reconnection.
    async start(draft: Draft) {
      busy = true;
      stopStream();
      set({ step: "starting" });
      try {
        if (id === undefined) await startNew(draft);
        else await startReconnect(draft, id);
      } finally {
        busy = false;
      }
      if (!closed && view.step !== "connected" && control === undefined && id !== undefined) {
        if (["code", "qr", "waiting", "checking"].includes(view.step)) attach();
      }
    },

    async cancel(): Promise<boolean> {
      const cancelled = await stopAttempt();
      if (cancelled) {
        const where = here();
        if (where) endDraft(where);
        set({ step: "cancelled" });
      }
      return cancelled;
    },

    // "Use a QR code" or "Use a code" while a code waits.
    async switchMethod(): Promise<boolean> {
      const cancelled = await stopAttempt();
      if (cancelled) back();
      return cancelled;
    },

    // "Try again" after a failure.
    retry() {
      back();
    },
  };
}
