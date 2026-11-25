import { describe, expect, it } from "vitest";
import type { Health } from "../api/health.ts";
import { describe as describeInstance } from "./labels.ts";

type Row = Parameters<typeof describeInstance>[0];

const now = Date.UTC(2026, 10, 23, 12, 0, 0);
const ago = (ms: number) => new Date(now - ms).toISOString();
const s = 1000;
const min = 60 * s;

const okHealth: Pick<Health, "state" | "dataIsFresh" | "lastGoodAt"> = {
  state: "ok",
  dataIsFresh: true,
  lastGoodAt: now - 10 * s,
};

const base: Row = {
  state: "active",
  status: "CONNECTED",
  enabled: true,
  send_only: false,
  provider: "WHATSAPP_WEB",
  status_stale: false,
  status_changed_at: ago(2 * 60 * min),
  alert: null,
};
const client = { viewer: "client", manager: "Bakery Lluna" } as const;
const row = (patch: Partial<Row>): Row => ({ ...base, ...patch });
const run = (patch: Partial<Row>, health = okHealth) =>
  describeInstance(row(patch), health, now, client);
const brief = (patch: Partial<Row>, health = okHealth) => {
  const d = run(patch, health);
  return [d.label, d.color, d.action, d.filter];
};
const alert = (kind: string, since: string | null) => ({ kind, since, emailed_at: null });

describe("describe: the words of each state", () => {
  it.each([
    [{ state: "creating" }, ["Linking", "wait", "continue", "wait"]],
    [{ state: "removing" }, ["Removing", "idle", null, null]],
    [{ state: "missing" }, ["Not found", "bad", "remove", "person"]],
    [{ state: "other" }, ["Checking", "idle", null, null]],
    [{ enabled: false, status: "NEEDS_AUTH" }, ["Disconnected", "bad", "enable", "off"]],
    [{ enabled: false, status: "CONNECTED" }, ["Not in use", "idle", "enable", "off"]],
    [{ enabled: false, status: null }, ["Not in use", "idle", "enable", "off"]],
    [{ enabled: false, status: "ERROR" }, ["Not in use", "idle", "enable", "off"]],
    [{ status: "CONNECTED", send_only: true }, ["Ready", "ok", null, "ok"]],
    [{ status: "CONNECTED" }, ["Connected", "ok", null, "ok"]],
    [{ status: "CONNECTING" }, ["Reconnecting", "wait", null, "wait"]],
    [{ status: "DISCONNECTED" }, ["Reconnecting", "wait", null, "wait"]],
    [
      { status: "NEEDS_AUTH", provider: "META_CLOUD" },
      ["New credentials needed", "bad", null, "person"],
    ],
    [{ status: "NEEDS_AUTH" }, ["Disconnected", "bad", "reconnect", "person"]],
    [{ status: "ERROR" }, ["Problem", "bad", "remove", "person"]],
    [{ status: null }, ["Checking", "idle", null, null]],
    [{ status: "SOMETHING_NEW" }, ["Checking", "idle", null, null]],
  ] as [Partial<Row>, unknown[]][])("%j", (patch, expected) => {
    expect(brief(patch)).toEqual(expected);
  });

  it("an unknown state wins over every other field", () => {
    expect(brief({ state: "other", enabled: false, status: "ERROR" })[0]).toBe("Checking");
  });

  it("covers every combination without throwing, and always gives a text", () => {
    const states = ["creating", "active", "removing", "missing", "future"];
    const statuses = [
      "CONNECTED",
      "CONNECTING",
      "NEEDS_AUTH",
      "DISCONNECTED",
      "ERROR",
      null,
      "FUTURE",
    ];
    const flags = [true, false];
    const providers = ["WHATSAPP_WEB", "META_CLOUD", "OTHER"];
    const healths = [
      okHealth,
      { state: "checking" as const, dataIsFresh: true },
      { state: "core" as const, dataIsFresh: true },
      { state: "database" as const, dataIsFresh: false },
      { state: "backend" as const, dataIsFresh: false },
      { state: "ok" as const, dataIsFresh: false },
    ];
    for (const state of states)
      for (const status of statuses)
        for (const enabled of flags)
          for (const send_only of flags)
            for (const provider of providers)
              for (const status_stale of flags)
                for (const h of healths) {
                  const d = run(
                    { state, status, enabled, send_only, provider, status_stale } as Partial<Row>,
                    h,
                  );
                  expect(d.label).not.toBe("");
                  expect(d.cause).not.toBe("");
                  expect(["ok", "wait", "bad", "idle"]).toContain(d.color);
                }
  });
});

describe("describe: Unknown data", () => {
  const lastGoodAt = now - 90 * s;
  it("status_stale gives Unknown, with the last known state", () => {
    const d = run({ status_stale: true });
    expect([d.label, d.color, d.action, d.filter]).toEqual(["Unknown", "idle", null, null]);
    expect(d.cause).toBe("Calyx cannot check this number now.");
    expect(d.last).toEqual({ label: "Connected", at: okHealth.lastGoodAt });
    expect(d.since).toBeUndefined();
  });
  it.each(["backend", "database"] as const)("health state %s gives Unknown", (state) => {
    const d = run({ status: "ERROR" }, { state, dataIsFresh: false, lastGoodAt });
    expect(d.label).toBe("Unknown");
    expect(d.last).toEqual({ label: "Problem", at: lastGoodAt });
  });
  it("old data (dataIsFresh false) gives Unknown even when the health is ok", () => {
    expect(run({}, { state: "ok", dataIsFresh: false, lastGoodAt }).label).toBe("Unknown");
  });
  it("state core with fresh data does not give Unknown", () => {
    expect(run({}, { state: "core", dataIsFresh: true, lastGoodAt }).label).toBe("Connected");
  });
  it("state checking alone does not give Unknown", () => {
    expect(run({}, { state: "checking", dataIsFresh: true }).label).toBe("Connected");
  });
  it("with no lastGoodAt there is no time", () => {
    const d = run({}, { state: "backend", dataIsFresh: false });
    expect(d.last).toEqual({ label: "Connected" });
  });
  it("a row in another state keeps its last label", () => {
    expect(run({ state: "removing" }, { state: "backend", dataIsFresh: false }).last?.label).toBe(
      "Removing",
    );
  });
  it("only Unknown has `last`", () => {
    expect(run({}).last).toBeUndefined();
  });
});

describe("describe: the 60 s of a number that only sends", () => {
  const sendOnly = { send_only: true } as const;
  it.each(["CONNECTING", "DISCONNECTED"] as const)("%s: Ready under 60 s", (status) => {
    expect(brief({ ...sendOnly, status, status_changed_at: ago(59 * s) })).toEqual([
      "Ready",
      "ok",
      null,
      "ok",
    ]);
  });
  it.each(["CONNECTING", "DISCONNECTED"] as const)("%s: Reconnecting from 60 s", (status) => {
    expect(brief({ ...sendOnly, status, status_changed_at: ago(60 * s) })[0]).toBe("Reconnecting");
  });
  it("counts from alert.since of each kind, not from status_changed_at", () => {
    for (const kind of ["needs_auth", "error", "disconnected", "missing"]) {
      const old = { status: "CONNECTING" as const, status_changed_at: ago(10 * s), ...sendOnly };
      expect(run({ ...old, alert: alert(kind, ago(2 * min)) }).label).toBe("Reconnecting");
      expect(run({ ...old, alert: alert(kind, ago(30 * s)) }).label).toBe("Ready");
    }
  });
  it("counts from status_changed_at when there is no alert", () => {
    expect(run({ ...sendOnly, status: "CONNECTING", status_changed_at: ago(2 * min) }).label).toBe(
      "Reconnecting",
    );
  });
  it("falls back to status_changed_at when the alert has no time", () => {
    expect(
      run({
        ...sendOnly,
        status: "CONNECTING",
        status_changed_at: ago(10 * s),
        alert: alert("disconnected", null),
      }).label,
    ).toBe("Ready");
  });
  it("an organization admin is sent to the server admin", () => {
    const d = describeInstance(
      row({ status: "CONNECTING", status_changed_at: ago(5 * min) }),
      okHealth,
      now,
      { viewer: "owner", manager: "Bakery Lluna" },
    );
    expect(d.cause).toContain("If it continues for more than 5 minutes, contact the server admin.");
    expect(d.cause).not.toContain("Bakery Lluna");
  });
  it("with no known start it does not promise Ready", () => {
    expect(run({ ...sendOnly, status: "CONNECTING", status_changed_at: null }).label).toBe(
      "Reconnecting",
    );
  });
  it("a number that does not send only is Reconnecting at once", () => {
    expect(run({ status: "CONNECTING", status_changed_at: ago(5 * s) }).label).toBe("Reconnecting");
  });
  it("uses the given time: the label changes with `now`", () => {
    const r = row({ ...sendOnly, status: "CONNECTING", status_changed_at: ago(0) });
    expect(describeInstance(r, okHealth, now + 59 * s, client).label).toBe("Ready");
    expect(describeInstance(r, okHealth, now + 61 * s, client).label).toBe("Reconnecting");
  });
});

describe("describe: the 5 minutes of Reconnecting", () => {
  const contact = "If it continues for more than 5 minutes, contact Bakery Lluna.";
  it("asks to contact after 5 minutes, from the alert or from status_changed_at", () => {
    const short = run({ status: "CONNECTING", status_changed_at: ago(4 * min) });
    expect(short.cause).not.toContain(contact);
    const long = run({ status: "CONNECTING", status_changed_at: ago(5 * min) });
    expect(long.cause).toContain(contact);
    for (const kind of ["needs_auth", "error", "disconnected", "missing"]) {
      const d = run({
        status: "DISCONNECTED",
        status_changed_at: ago(1 * min),
        alert: alert(kind, ago(6 * min)),
      });
      expect(d.cause).toContain(contact);
    }
  });
  it("status_changed_at alone restarts at each change, an alert does not", () => {
    const restarted = run({ status: "CONNECTING", status_changed_at: ago(1 * min) });
    expect(restarted.cause).not.toContain(contact);
  });
  it("with no known start there is no contact text", () => {
    expect(run({ status: "CONNECTING", status_changed_at: null }).cause).not.toContain(contact);
  });
});

describe("describe: since", () => {
  it("is the alert time, or the time of the last change with no alert", () => {
    const at = now - 7 * min;
    expect(run({ status_changed_at: new Date(at).toISOString() }).since).toBe(at);
    const alertAt = now - 3 * min;
    expect(
      run({ status: "ERROR", alert: alert("error", new Date(alertAt).toISOString()) }).since,
    ).toBe(alertAt);
  });
  it("is absent when the start is unknown or the row has no connection", () => {
    expect(run({ status_changed_at: null }).since).toBeUndefined();
    expect(run({ state: "removing" }).since).toBeUndefined();
  });
});

describe("describe: texts", () => {
  it.each([
    [{ send_only: true }, "This number sends when there is a message. It rests in between."],
    [{ state: "removing" }, "Calyx is removing this number. This can take some seconds."],
    [{ state: "missing" }, "Calyx cannot find this number. Remove it, and connect it again."],
    [
      { status: "NEEDS_AUTH", provider: "META_CLOUD" },
      "The credentials of Meta expired or changed. Change them with the API.",
    ],
    [{ status: null }, "Calyx has no state for this number yet."],
    [
      { status: "ERROR" },
      "WhatsApp refused this session. WhatsApp may have banned the number for a time. A new connection does not remove a ban. To use the number again, remove it and connect it. Its webhooks and bulk jobs are lost.",
    ],
  ] as [Partial<Row>, string][])("%j", (patch, text) => {
    expect(run(patch).cause).toBe(text);
  });
});
