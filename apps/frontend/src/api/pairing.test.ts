import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type Draft, newDraft, readDraft, saveDraft } from "../domain/draft.ts";
import type { components } from "./generated/types.ts";
import type { Failure } from "./request.ts";

type Instance = components["schemas"]["Instance"];
type Update = components["schemas"]["Pairing"];
type Item =
  | { event: "update"; data: Update }
  | { event: "done"; data: { status: "CONNECTED" } }
  | { event: "failed"; data: { code: string; detail: string } };
type Open = {
  id: string;
  signal: AbortSignal;
  send: (item: Item) => void;
  end: (result: "ended" | Failure) => void;
};
type Answer = { ok: true; data: unknown } | { ok: false; failure: Failure };

const opens: Open[] = [];
const stream = vi.hoisted(() => vi.fn());
const request = vi.hoisted(() => vi.fn());
vi.mock("./request.ts", () => ({ stream, request }));

import { createBody, createPairing, type PairingOptions, pairBody } from "./pairing.ts";

const user = "user-1";
const clientId = "client-a";
const id = "instance-1";
const minute = 60_000;

const problem = (status: number, code: string): Failure => ({
  kind: "problem",
  status,
  code,
  detail: "",
  fields: [],
  requestId: "r1",
});
const unauthorized = problem(401, "unauthenticated");
const notFound = problem(404, "not_found");

const row = (over: Partial<Instance> = {}): Instance => ({
  alert: null,
  client_id: clientId,
  device_identity: null,
  display_name: "Shop",
  enabled: true,
  id,
  locale: "es-ES",
  phone_number_id: null,
  provider: "WHATSAPP_WEB",
  send_only: false,
  state: "active",
  status: "CONNECTED",
  status_changed_at: null,
  status_stale: false,
  webhooks: [],
  ...over,
});
const code = (c = "ABCD-2345"): Update => ({
  status: "NEEDS_AUTH",
  pairing_code: c,
  qr_text: null,
  message: "",
});
const qr = (q: string): Update => ({
  status: "NEEDS_AUTH",
  pairing_code: null,
  qr_text: q,
  message: "",
});

// The answers of `request`, by operation. Each call takes the next answer of its operation.
let answers: Record<string, Answer[]>;
const answer = (op: string, ...list: Answer[]) => {
  answers[op] = [...(answers[op] ?? []), ...list];
};
const ok = (data: unknown): Answer => ({ ok: true, data });
const fail = (failure: Failure): Answer => ({ ok: false, failure });

let visible = true;
function setVisible(value: boolean) {
  visible = value;
  document.dispatchEvent(new Event("visibilitychange"));
}

const tick = (ms: number) => vi.advanceTimersByTimeAsync(ms);
const last = () => opens[opens.length - 1] as Open;
const calls = (op: string) => request.mock.calls.filter((c) => c[0] === op);

const instanceKey = { instance: id };
const clientKey = { client: clientId };
const draft = (over: Partial<Draft> = {}): Draft => ({
  ...newDraft({ userId: user, clientId, kind: "new" }),
  name: "Shop",
  method: "code",
  phone: { country: "ES", text: "600 123 456" },
  ...over,
});

const onUnauthorized = vi.fn();
const navigate = vi.fn();
let pairing: ReturnType<typeof createPairing> | undefined;
function make(over: Partial<PairingOptions> = {}) {
  pairing = createPairing({
    userId: user,
    clientId,
    instanceId: id,
    reconnect: false,
    onUnauthorized,
    navigate,
    ...over,
  });
  return pairing;
}
const view = () => pairing?.view();

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(100 * minute);
  sessionStorage.clear();
  opens.length = 0;
  answers = {};
  visible = true;
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => (visible ? "visible" : "hidden"),
  });
  onUnauthorized.mockClear();
  navigate.mockClear();
  stream.mockReset();
  stream.mockImplementation(
    (
      _op: string,
      params: { path: { id: string } },
      onEvent: (i: Item) => void,
      signal: AbortSignal,
    ) =>
      new Promise((resolve) => {
        opens.push({ id: params.path.id, signal, send: onEvent, end: resolve });
        signal.addEventListener("abort", () => resolve("ended"));
      }),
  );
  request.mockReset();
  request.mockImplementation(async (op: string) => {
    const next = answers[op]?.shift();
    if (!next) throw new Error(`no answer for ${op}`);
    return next;
  });
});
afterEach(() => {
  pairing?.close();
  pairing = undefined;
  vi.useRealTimers();
});

// A pairing that runs: the draft says "started" (now by default), the drawer is open, and the
// stream is open.
function running(over: Partial<PairingOptions> = {}, d: Partial<Draft> = {}) {
  saveDraft(instanceKey, draft({ startedAt: Date.now(), ...d }));
  const p = make(over);
  p.open();
  p.attach();
  return p;
}

describe("Attach, 200", () => {
  it("shows the code of an update, and sets started with its time", async () => {
    running({}, { startedAt: 95 * minute });
    expect(opens).toHaveLength(1);
    last().send({ event: "update", data: { ...code(), message: "Type it" } });
    expect(view()).toEqual({ step: "code", code: "ABCD-2345", message: "Type it" });
    expect(readDraft(instanceKey, user)?.startedAt).toBe(100 * minute);
  });

  it("shows each new QR in place of the old one", async () => {
    running();
    last().send({ event: "update", data: qr("first") });
    expect(view()).toMatchObject({ step: "qr", qr: "first" });
    last().send({ event: "update", data: qr("second") });
    expect(view()).toMatchObject({ step: "qr", qr: "second" });
  });

  it("shows an update with no code as a wait", async () => {
    running();
    last().send({
      event: "update",
      data: { status: "CONNECTING", pairing_code: null, qr_text: null, message: "Waiting" },
    });
    expect(view()).toEqual({ step: "waiting", message: "Waiting" });
  });

  it("an update with CONNECTED is Connected, ends the draft, and no end attaches again", async () => {
    running();
    last().send({ event: "update", data: { ...code(), status: "CONNECTED" } });
    expect(view()).toEqual({ step: "connected" });
    expect(readDraft(instanceKey, user)).toBeNull();
    last().end("ended");
    await tick(60_000);
    expect(readDraft(instanceKey, user)).toBeNull();
    expect(opens).toHaveLength(1);
  });

  it("done is Connected, and ends the draft", async () => {
    running();
    last().send({ event: "done", data: { status: "CONNECTED" } });
    last().end("ended");
    await tick(60_000);
    expect(view()).toEqual({ step: "connected" });
    expect(readDraft(instanceKey, user)).toBeNull();
    expect(opens).toHaveLength(1);
  });

  it("failed gives its reason, removes started and keeps the draft", async () => {
    running();
    last().send({ event: "failed", data: { code: "pairing_expired", detail: "Too late" } });
    last().end("ended");
    await tick(60_000);
    expect(view()).toEqual({
      step: "failed",
      failure: { kind: "pairing", code: "pairing_expired", detail: "Too late" },
    });
    expect(readDraft(instanceKey, user)).toMatchObject({ name: "Shop", startedAt: null });
    expect(opens).toHaveLength(1);
  });

  it("an end with no final event attaches again after 1, 2, 5, 10 and then 30 s", async () => {
    running();
    for (const wait of [1000, 2000, 5000, 10_000, 30_000, 30_000]) {
      const count = opens.length;
      last().end("ended");
      await tick(wait - 1);
      expect(opens).toHaveLength(count);
      await tick(1);
      expect(opens).toHaveLength(count + 1);
    }
  });

  it("a stream that gave an event attaches again after 1 s", async () => {
    running();
    last().end("ended");
    await tick(1000);
    last().end("ended");
    await tick(2000);
    last().send({ event: "update", data: code() });
    last().end("ended");
    await tick(1000);
    expect(opens).toHaveLength(4);
    expect(view()).toMatchObject({ step: "code" });
  });
});

describe("Attach, 404", () => {
  it("reads the instance one time, and does not attach", async () => {
    running();
    answer("getInstance", ok(row({ status: "NEEDS_AUTH" })));
    last().end(notFound);
    await tick(0);
    expect(calls("getInstance")).toHaveLength(1);
    await tick(60_000);
    expect(opens).toHaveLength(1);
    expect(calls("getInstance")).toHaveLength(1);
  });

  it("not found, a new instance: the phone was not linked", async () => {
    running();
    answer("getInstance", fail(notFound));
    last().end(notFound);
    await tick(0);
    expect(view()).toEqual({ step: "notLinked" });
    expect(readDraft(instanceKey, user)?.startedAt).toBeNull();
  });

  it("not found, a reconnection: the number was removed", async () => {
    running({ reconnect: true });
    answer("getInstance", fail(notFound));
    last().end(notFound);
    await tick(0);
    expect(view()).toEqual({ step: "removed" });
  });

  it.each(["CONNECTED", "CONNECTING", "DISCONNECTED"] as const)(
    "%s, active, enabled, not stale, started less than 10 minutes ago: the phone is linked",
    async (status) => {
      running({}, { startedAt: 100 * minute - 9 * minute });
      const instance = row({ status });
      answer("getInstance", ok(instance));
      last().end(notFound);
      await tick(0);
      expect(view()).toEqual({ step: "linked", instance });
      expect(readDraft(instanceKey, user)?.startedAt).toBeNull();
    },
  );

  it.each([
    ["started 10 minutes ago", {}, { startedAt: 90 * minute }],
    ["disabled", { enabled: false }, {}],
    ["stale", { status_stale: true }, {}],
    ["NEEDS_AUTH", { status: "NEEDS_AUTH" }, {}],
    ["ERROR", { status: "ERROR" }, {}],
    ["missing", { state: "missing" }, {}],
    ["removing", { state: "removing" }, {}],
  ] as const)("%s: the instance as describe() says", async (_name, over, d) => {
    running({}, d);
    const instance = row(over as Partial<Instance>);
    answer("getInstance", ok(instance));
    last().end(notFound);
    await tick(0);
    expect(view()).toEqual({ step: "instance", instance });
    expect(readDraft(instanceKey, user)?.startedAt).toBeNull();
  });

  it("with no started in this tab: the instance as describe() says", async () => {
    const p = make();
    p.open();
    p.attach();
    const instance = row();
    answer("getInstance", ok(instance));
    last().end(notFound);
    await tick(0);
    expect(view()).toEqual({ step: "instance", instance });
  });

  it("creating: Calyx ends this attempt, and when the row goes away, not found", async () => {
    const p = running();
    answer("getInstance", ok(row({ state: "creating", status: null })));
    last().end(notFound);
    await tick(0);
    expect(view()).toEqual({ step: "ending" });
    p.listRead(row({ state: "creating", status: null }));
    expect(view()).toEqual({ step: "ending" });
    p.listRead(undefined);
    expect(view()).toEqual({ step: "notLinked" });
  });

  it("a read with no answer says Checking the link, and attaches again", async () => {
    running();
    answer("getInstance", fail({ kind: "unreachable" }));
    last().end(notFound);
    await tick(0);
    expect(view()).toEqual({ step: "checking" });
    await tick(1000);
    expect(opens).toHaveLength(2);
  });

  it("a 401 of the read ends the login session", async () => {
    running();
    answer("getInstance", fail(unauthorized));
    last().end(notFound);
    await tick(0);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });
});

describe("Attach, any other failure", () => {
  it.each([
    ["unreachable (also a 502 in place of a 404)", { kind: "unreachable" }],
    ["timeout", { kind: "timeout" }],
    ["a 500", problem(500, "internal")],
    ["a 409", problem(409, "conflict")],
  ] as const)("%s: Checking the link, and it attaches again with the same waits", async (_n, f) => {
    running();
    last().send({ event: "update", data: code() });
    last().end(f as Failure);
    await tick(0);
    expect(view()).toEqual({ step: "checking" });
    await tick(1000);
    expect(opens).toHaveLength(2);
    last().end(f as Failure);
    await tick(1999);
    expect(opens).toHaveLength(2);
    await tick(1);
    expect(opens).toHaveLength(3);
    expect(calls("getInstance")).toHaveLength(0);
  });

  it("401: the login session ends, and it does not attach again", async () => {
    running();
    last().end(unauthorized);
    await tick(60_000);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(opens).toHaveLength(1);
  });
});

describe("when it attaches", () => {
  it("after createInstance answers: the draft moves to the instance, started, a new address", async () => {
    saveDraft(clientKey, draft());
    const p = make({ instanceId: undefined });
    p.open();
    answer(
      "createInstance",
      ok({ instance: row({ state: "creating", status: null }), pairing: code() }),
    );
    await p.start(draft());
    expect(view()).toMatchObject({ step: "code", code: "ABCD-2345" });
    expect(readDraft(clientKey, user)).toBeNull();
    expect(readDraft(instanceKey, user)).toMatchObject({ name: "Shop", startedAt: 100 * minute });
    expect(navigate).toHaveBeenCalledWith({ instance: id });
    expect(p.instanceId()).toBe(id);
    expect(opens).toHaveLength(1);
    expect(last().id).toBe(id);
  });

  it("a first update that is CONNECTED is Connected, with no stream", async () => {
    saveDraft(clientKey, draft());
    const p = make({ instanceId: undefined });
    p.open();
    answer("createInstance", ok({ instance: row(), pairing: { ...code(), status: "CONNECTED" } }));
    await p.start(draft());
    expect(view()).toEqual({ step: "connected" });
    expect(opens).toHaveLength(0);
    expect(readDraft(instanceKey, user)).toBeNull();
  });

  it("after pairInstance answers", async () => {
    saveDraft(instanceKey, draft({ kind: "reconnect" }));
    const p = make({ reconnect: true, instanceLocale: "es-ES" });
    p.open();
    expect(opens).toHaveLength(0);
    answer("pairInstance", ok({ pairing: qr("q") }));
    await p.start(draft({ kind: "reconnect" }));
    expect(view()).toMatchObject({ step: "qr", qr: "q" });
    expect(readDraft(instanceKey, user)?.startedAt).toBe(100 * minute);
    expect(opens).toHaveLength(1);
    expect(navigate).not.toHaveBeenCalled();
  });

  it("when a drawer opens and the draft says started less than 10 minutes ago", () => {
    saveDraft(instanceKey, draft({ startedAt: 100 * minute - 9 * minute }));
    make().open();
    expect(opens).toHaveLength(1);
  });

  it("not when started is 10 minutes old, or absent", () => {
    saveDraft(instanceKey, draft({ startedAt: 90 * minute }));
    make().open();
    pairing?.close();
    saveDraft(instanceKey, draft());
    make().open();
    expect(opens).toHaveLength(0);
  });

  it("not for the draft of another user", () => {
    saveDraft(instanceKey, draft({ userId: "user-2", startedAt: Date.now() }));
    make().open();
    expect(opens).toHaveLength(0);
  });

  it("the stream closes with a hidden page, and a return attaches again", async () => {
    running();
    last().send({ event: "update", data: code() });
    setVisible(false);
    expect(last().signal.aborted).toBe(true);
    await tick(60_000);
    expect(opens).toHaveLength(1);
    expect(view()).toMatchObject({ step: "code" });
    setVisible(true);
    expect(opens).toHaveLength(2);
  });

  it("a return does not attach when the draft is not started", async () => {
    const p = make();
    p.open();
    setVisible(false);
    setVisible(true);
    expect(opens).toHaveLength(0);
  });

  it("an instance in creating, with no stream, after a read of the list: at most once each 15 s", async () => {
    const p = make();
    p.open();
    const creating = row({ state: "creating", status: null });
    p.listRead(creating);
    expect(opens).toHaveLength(1);
    p.listRead(creating);
    expect(opens).toHaveLength(1);
    answer("getInstance", ok(creating));
    last().end(notFound);
    await tick(0);
    p.listRead(creating);
    expect(opens).toHaveLength(1);
    await tick(15_000);
    p.listRead(creating);
    expect(opens).toHaveLength(2);
  });

  it("not for an instance that is not in creating", () => {
    const p = make();
    p.open();
    p.listRead(row({ status: "NEEDS_AUTH" }));
    expect(opens).toHaveLength(0);
  });

  it.each([
    ["timeout", { kind: "timeout" }],
    ["unreachable", { kind: "unreachable" }],
    ["change_outcome_unknown", problem(504, "change_outcome_unknown")],
  ] as const)("after a lost answer of pairInstance (%s)", async (_n, f) => {
    saveDraft(instanceKey, draft({ kind: "reconnect" }));
    const p = make({ reconnect: true, instanceLocale: "es-ES" });
    p.open();
    answer("pairInstance", fail(f as Failure));
    await p.start(draft({ kind: "reconnect" }));
    expect(view()).toEqual({ step: "checking" });
    expect(opens).toHaveLength(1);
  });

  it("after a 409 pairing_in_progress of pairInstance", async () => {
    const p = make({ reconnect: true, instanceLocale: "es-ES" });
    p.open();
    answer("pairInstance", fail(problem(409, "pairing_in_progress")));
    await p.start(draft({ kind: "reconnect" }));
    expect(opens).toHaveLength(1);
  });

  it("attach opens one stream, also when the screen calls it two times", () => {
    const p = make();
    p.open();
    p.attach();
    p.attach();
    expect(opens).toHaveLength(1);
  });

  it("never after close", async () => {
    const p = running();
    p.close();
    expect(last().signal.aborted).toBe(true);
    await tick(60_000);
    p.attach();
    expect(opens).toHaveLength(1);
  });
});

describe("Start", () => {
  it("saves the draft that it gets, so Try again keeps each choice", async () => {
    saveDraft(clientKey, draft({ name: "Old" }));
    const p = make({ instanceId: undefined });
    p.open();
    answer("createInstance", ok({ instance: row(), pairing: code() }));
    await p.start(draft({ name: "New", method: "qr" }));
    last().send({ event: "failed", data: { code: "pairing_expired", detail: "" } });
    p.retry();
    expect(readDraft(clientKey, user)).toMatchObject({ name: "New", method: "qr" });
  });

  it("saves the draft of a reconnection under the instance", async () => {
    const p = make({ reconnect: true, instanceLocale: "es-ES" });
    p.open();
    answer("pairInstance", ok({ pairing: code() }));
    await p.start(draft({ kind: "reconnect", method: "qr" }));
    expect(readDraft(instanceKey, user)).toMatchObject({ method: "qr", startedAt: 100 * minute });
  });

  it("a reconnection with no known locale sends none", async () => {
    const p = make({ reconnect: true });
    p.open();
    answer("pairInstance", ok({ pairing: code() }));
    await p.start(draft({ kind: "reconnect" }));
    expect(calls("pairInstance")[0]?.[1].body).not.toHaveProperty("locale");
  });

  it("a lost answer of createInstance goes back to the step, with no stream", async () => {
    saveDraft(clientKey, draft());
    const p = make({ instanceId: undefined });
    p.open();
    answer("createInstance", fail({ kind: "timeout" }));
    await p.start(draft());
    expect(view()).toEqual({ step: "lostStart" });
    expect(opens).toHaveLength(0);
    expect(readDraft(clientKey, user)).not.toBeNull();
    expect(navigate).not.toHaveBeenCalled();
  });

  it("a refusal gives the failure and its operation", async () => {
    const p = make({ instanceId: undefined });
    p.open();
    const f = problem(403, "quota_exceeded");
    answer("createInstance", fail(f));
    await p.start(draft());
    expect(view()).toEqual({ step: "refused", failure: f, operationId: "createInstance" });
  });

  it("a 401 ends the login session", async () => {
    const p = make({ instanceId: undefined });
    p.open();
    answer("createInstance", fail(unauthorized));
    await p.start(draft());
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it("creates in the client of the address", async () => {
    const p = make({ instanceId: undefined });
    p.open();
    answer("createInstance", ok({ instance: row(), pairing: code() }));
    await p.start(draft());
    expect(calls("createInstance")[0]?.[1].body.client_id).toBe(clientId);
  });
});

describe("the bodies", () => {
  it("of createInstance: the choices, the automatic locale, the complete number", () => {
    expect(createBody(draft(), clientId, ["es"])).toEqual({
      provider: "WHATSAPP_WEB",
      client_id: clientId,
      display_name: "Shop",
      send_only: false,
      locale: "es-ES",
      pairing: "code",
      phone: "34600123456",
    });
    expect(
      createBody(
        draft({ method: "qr", locale: "pt-BR", device: "FIREFOX_LINUX", sendOnly: true }),
        clientId,
        ["es"],
      ),
    ).toEqual({
      provider: "WHATSAPP_WEB",
      client_id: clientId,
      display_name: "Shop",
      send_only: true,
      locale: "pt-BR",
      device_identity: "FIREFOX_LINUX",
      pairing: "qr",
    });
  });

  it("of pairInstance: no locale when the stored one is not known", () => {
    expect(pairBody(draft(), undefined, ["es"])).toEqual({ pairing: "code", phone: "34600123456" });
  });

  it("of pairInstance: the automatic locale only when the instance has none", () => {
    expect(pairBody(draft(), "it-IT", ["es"])).toEqual({ pairing: "code", phone: "34600123456" });
    expect(pairBody(draft({ method: "qr" }), null, ["es"])).toEqual({
      pairing: "qr",
      locale: "es-ES",
    });
  });

  it("with no method chosen: a code for a coarse pointer, a QR for any other", () => {
    const media = (coarse: boolean) =>
      vi.fn(() => ({ matches: coarse }) as unknown as MediaQueryList);
    vi.stubGlobal("matchMedia", media(true));
    expect(pairBody(draft({ method: null }), "es-ES").pairing).toBe("code");
    vi.stubGlobal("matchMedia", media(false));
    expect(pairBody(draft({ method: null }), "es-ES").pairing).toBe("qr");
    vi.unstubAllGlobals();
  });
});

describe("Cancel", () => {
  it("closes the stream, calls cancelPairing, reads the instance: not found is cancelled", async () => {
    const p = running();
    answer("cancelPairing", ok(undefined));
    answer("getInstance", fail(notFound));
    expect(await p.cancel()).toBe(true);
    expect(opens[0]?.signal.aborted).toBe(true);
    expect(calls("cancelPairing")).toHaveLength(1);
    expect(view()).toEqual({ step: "cancelled" });
    expect(readDraft(instanceKey, user)).toBeNull();
    await tick(60_000);
    expect(opens).toHaveLength(1);
  });

  it("NEEDS_AUTH is cancelled, also after a cancelPairing with no answer", async () => {
    const p = running({ reconnect: true });
    answer("cancelPairing", fail({ kind: "unreachable" }));
    answer("getInstance", ok(row({ status: "NEEDS_AUTH" })));
    expect(await p.cancel()).toBe(true);
    expect(view()).toEqual({ step: "cancelled" });
  });

  it("anything else is the instance as describe() says: the phone can link before the cancel", async () => {
    const p = running();
    const instance = row({ status: "CONNECTED" });
    answer("cancelPairing", fail(problem(404, "not_found")));
    answer("getInstance", ok(instance));
    expect(await p.cancel()).toBe(false);
    expect(view()).toEqual({ step: "instance", instance });
  });

  it("a 401 ends the login session", async () => {
    const p = running();
    answer("cancelPairing", fail(unauthorized));
    expect(await p.cancel()).toBe(false);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(calls("getInstance")).toHaveLength(0);
  });

  it("a read with no answer says Checking the link, and attaches again", async () => {
    const p = running();
    answer("cancelPairing", ok(undefined));
    answer("getInstance", fail({ kind: "timeout" }));
    expect(await p.cancel()).toBe(false);
    expect(view()).toEqual({ step: "checking" });
    expect(opens).toHaveLength(2);
  });

  it("no attach from a read of the list while the cancel runs", async () => {
    const p = running();
    answer("cancelPairing", ok(undefined));
    answer("getInstance", fail(notFound));
    const done = p.cancel();
    p.listRead(row({ state: "creating", status: null }));
    await done;
    expect(opens).toHaveLength(1);
  });
});

describe("the other method, while a code waits", () => {
  it("a new instance: back to the step with the draft, at the address of its client", async () => {
    const p = running();
    answer("cancelPairing", ok(undefined));
    answer("getInstance", fail(notFound));
    expect(await p.switchMethod()).toBe(true);
    expect(view()).toEqual({ step: "ready" });
    expect(readDraft(clientKey, user)).toMatchObject({ name: "Shop", startedAt: null });
    expect(readDraft(instanceKey, user)).toBeNull();
    expect(navigate).toHaveBeenCalledWith(clientKey);
    expect(p.instanceId()).toBeUndefined();
  });

  it("a reconnection: back to the step, and the draft stays with the instance", async () => {
    const p = running({ reconnect: true }, { kind: "reconnect" });
    answer("cancelPairing", ok(undefined));
    answer("getInstance", ok(row({ status: "NEEDS_AUTH" })));
    expect(await p.switchMethod()).toBe(true);
    expect(readDraft(instanceKey, user)).toMatchObject({ startedAt: null });
    expect(navigate).not.toHaveBeenCalled();
  });

  it("when the phone linked first: the instance", async () => {
    const p = running();
    answer("cancelPairing", ok(undefined));
    answer("getInstance", ok(row()));
    expect(await p.switchMethod()).toBe(false);
    expect(view()).toMatchObject({ step: "instance" });
  });
});

describe("Try again", () => {
  it("after a reload keeps the client of the draft", async () => {
    // The tab of the address of another client cannot change the client of this draft.
    saveDraft(instanceKey, draft({ startedAt: Date.now() }));
    const p = make({ clientId: undefined });
    p.open();
    last().send({ event: "failed", data: { code: "pairing_expired", detail: "" } });
    p.retry();
    expect(view()).toEqual({ step: "ready" });
    expect(navigate).toHaveBeenCalledWith(clientKey);
    expect(readDraft(clientKey, user)?.name).toBe("Shop");
    answer("createInstance", ok({ instance: row(), pairing: code() }));
    await p.start(draft());
    expect(calls("createInstance")[0]?.[1].body.client_id).toBe(clientId);
  });

  it("a reconnection keeps its draft at the instance", () => {
    saveDraft(instanceKey, draft({ kind: "reconnect", startedAt: Date.now() }));
    const p = make({ reconnect: true });
    p.open();
    last().send({ event: "failed", data: { code: "pairing_expired", detail: "" } });
    p.retry();
    expect(view()).toEqual({ step: "ready" });
    expect(readDraft(instanceKey, user)).not.toBeNull();
    expect(navigate).not.toHaveBeenCalled();
  });
});

describe("open", () => {
  it("does not show an old code when the drawer opens again after 10 minutes", async () => {
    const p = running();
    last().send({ event: "update", data: code() });
    p.close();
    await tick(10 * minute);
    p.open();
    expect(view()).toEqual({ step: "ready" });
    expect(opens).toHaveLength(1);
  });

  it("keeps the result of an attempt that ended", async () => {
    const p = running();
    last().send({ event: "failed", data: { code: "pairing_expired", detail: "" } });
    p.close();
    p.open();
    expect(view()).toMatchObject({ step: "failed" });
  });
});

describe("subscribe", () => {
  it("tells each change of the view, until the stop function", () => {
    const p = running();
    const listener = vi.fn();
    const stop = p.subscribe(listener);
    last().send({ event: "update", data: code() });
    expect(listener).toHaveBeenCalledTimes(1);
    stop();
    last().send({ event: "update", data: code("WXYZ-9876") });
    expect(listener).toHaveBeenCalledTimes(1);
  });
});
