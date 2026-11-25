import { describe, expect, it } from "vitest";
import type { components } from "../api/generated/types.ts";
import {
  calyxEvents,
  type Effect,
  generateSecret,
  initialForm,
  matchesPlan,
  pageEvents,
  planWebhooks,
  rawEvents,
  secretBytes,
  type WebhookForm,
} from "./webhooks.ts";

type Webhook = components["schemas"]["Webhook"];
type WebhookInput = components["schemas"]["WebhookInput"];

const a = "https://a.example/hook";
const b = "https://b.example/hook";

const hook = (event: string, url: string, hasSecret = false): Webhook => ({
  event,
  url,
  has_secret: hasSecret,
});

const one = (url: string, ...events: string[]): WebhookForm => ({
  urlMode: "one",
  url,
  events: events.map((event) => ({ event })),
});

const each = (rows: Record<string, string>): WebhookForm => ({
  urlMode: "each",
  events: Object.entries(rows).map(([event, url]) => ({ event, url })),
});

const keep = { one: { kind: "keep" } } as const;

// The plan must succeed in these cases. The helper gives its body and effects.
function planned(...args: Parameters<typeof planWebhooks>) {
  const plan = planWebhooks(...args);
  if (!plan.ok) throw new Error(`refused: ${plan.rows.join(", ")}`);
  return plan;
}

const bodyOf = (body: WebhookInput[], event: string) => body.find((h) => h.event === event);
const effectOf = (effects: Effect[], event: string) => effects.find((e) => e.event === event);

describe("the event lists", () => {
  it("has the four Calyx events and the raw events of each provider", () => {
    expect(calyxEvents).toEqual([
      "connection.update",
      "message.received",
      "message.status",
      "bulk.finished",
    ]);
    expect(rawEvents("WHATSAPP_WEB")).toHaveLength(10);
    expect(rawEvents("WHATSAPP_WEB")).toContain("whatsmeow.Receipt");
    expect(rawEvents("META_CLOUD")).toEqual(["meta.webhook"]);
    expect(pageEvents("META_CLOUD")).toEqual([...calyxEvents, "meta.webhook"]);
  });
});

describe("planWebhooks", () => {
  it("sends an unknown stored event back unchanged, with secret absent", () => {
    const stored = [hook("message.received", a), hook("meta.webhook", b, true)];
    const plan = planned(stored, "WHATSAPP_WEB", one(a, "message.received"), keep);
    expect(bodyOf(plan.body, "meta.webhook")).toEqual({ event: "meta.webhook", url: b });
    expect(effectOf(plan.effects, "meta.webhook")).toEqual({
      event: "meta.webhook",
      url: b,
      wasSigned: true,
      signed: true,
      newSecret: false,
      change: "kept-unknown",
    });
  });

  // A signed unknown webhook beside a new event would refuse the save if a rule counted it.
  it("counts no rule on an unknown webhook", () => {
    const stored = [hook("whatsmeow.Receipt", b, true)];
    const form = one(a, "message.received");
    expect(planWebhooks(stored, "META_CLOUD", form, keep).ok).toBe(true);
  });

  it("sends secret absent for the same event and URL with keep", () => {
    const stored = [hook("message.received", a, true)];
    const plan = planned(stored, "WHATSAPP_WEB", one(a, "message.received"), keep);
    expect(plan.body).toEqual([{ event: "message.received", url: a }]);
    expect(effectOf(plan.effects, "message.received")).toMatchObject({
      change: "same",
      wasSigned: true,
      signed: true,
    });
  });

  it("gives no stored secret to a new URL", () => {
    const stored = [hook("message.received", a)];
    const plan = planned(stored, "WHATSAPP_WEB", one(b, "message.received"), keep);
    expect(plan.body).toEqual([{ event: "message.received", url: b }]);
    expect(effectOf(plan.effects, "message.received")).toMatchObject({
      change: "changed-url",
      wasSigned: false,
      signed: false,
    });
  });

  it("gives no stored secret to a new event", () => {
    const stored = [hook("message.received", a)];
    const form = one(a, "message.received", "message.status");
    const plan = planned(stored, "WHATSAPP_WEB", form, keep);
    expect(bodyOf(plan.body, "message.status")).toEqual({ event: "message.status", url: a });
    expect(effectOf(plan.effects, "message.status")).toMatchObject({
      change: "new",
      wasSigned: false,
      signed: false,
    });
  });

  it("refuses a changed URL of a signed webhook with keep", () => {
    const stored = [hook("message.received", a, true), hook("message.status", a)];
    const form = each({ "message.received": b, "message.status": a });
    expect(planWebhooks(stored, "WHATSAPP_WEB", form, { each: {} })).toEqual({
      ok: false,
      reason: "secret_needed",
      rows: ["message.received"],
    });
  });

  it("refuses a change of the one URL when a webhook is signed, with keep", () => {
    const stored = [hook("message.received", a, true), hook("message.status", a)];
    const form = one(b, "message.received", "message.status");
    expect(planWebhooks(stored, "WHATSAPP_WEB", form, keep)).toEqual({
      ok: false,
      reason: "secret_needed",
      rows: ["message.received"],
    });
  });

  it("refuses a new event in a scope with a signed webhook, with keep", () => {
    const stored = [hook("message.received", a, true), hook("message.status", a, true)];
    const form = one(a, "message.received", "message.status", "connection.update");
    expect(planWebhooks(stored, "WHATSAPP_WEB", form, keep)).toEqual({
      ok: false,
      reason: "secret_needed",
      rows: ["connection.update"],
    });
  });

  // In "One URL for each event", each row is its own scope.
  it("accepts a new row beside a signed row in one URL for each event", () => {
    const stored = [hook("message.received", a, true)];
    const form = each({ "message.received": a, "message.status": b });
    expect(planWebhooks(stored, "WHATSAPP_WEB", form, { each: {} }).ok).toBe(true);
  });

  it("gives no refusal for a stored mix that the save does not change", () => {
    const stored = [hook("message.received", a, true), hook("message.status", a)];
    const form = one(a, "message.received", "message.status");
    const plan = planned(stored, "WHATSAPP_WEB", form, keep);
    expect(plan.body).toEqual([
      { event: "message.received", url: a },
      { event: "message.status", url: a },
    ]);
  });

  it("puts a set value on every webhook of its scope, and not on an unknown webhook", () => {
    const stored = [hook("message.received", a, true), hook("meta.webhook", b)];
    const form = one(b, "message.received", "message.status");
    const plan = planned(stored, "WHATSAPP_WEB", form, { one: { kind: "set", value: "s1" } });
    expect(plan.body).toEqual([
      { event: "message.received", url: b, secret: "s1" },
      { event: "message.status", url: b, secret: "s1" },
      { event: "meta.webhook", url: b },
    ]);
    expect(effectOf(plan.effects, "message.status")).toMatchObject({
      signed: true,
      newSecret: true,
    });
  });

  it("puts a set value of a row on that row only", () => {
    const stored = [hook("message.received", a, true)];
    const form = each({ "message.received": b, "message.status": b });
    const secrets = { each: { "message.received": { kind: "set", value: "s1" } } } as const;
    const plan = planned(stored, "WHATSAPP_WEB", form, secrets);
    expect(bodyOf(plan.body, "message.received")).toEqual({
      event: "message.received",
      url: b,
      secret: "s1",
    });
    expect(bodyOf(plan.body, "message.status")).toEqual({ event: "message.status", url: b });
  });

  it('sends "" on every webhook of the scope of none', () => {
    const stored = [hook("message.received", a, true)];
    const form = one(b, "message.received", "message.status");
    const plan = planned(stored, "WHATSAPP_WEB", form, { one: { kind: "none" } });
    expect(plan.body).toEqual([
      { event: "message.received", url: b, secret: "" },
      { event: "message.status", url: b, secret: "" },
    ]);
    expect(plan.effects.every((e) => !e.signed)).toBe(true);
  });

  it('leaves a cleared event out of the body, with the effect "removed"', () => {
    const stored = [hook("message.received", a, true), hook("message.status", a)];
    const plan = planned(stored, "WHATSAPP_WEB", one(a, "message.status"), keep);
    expect(plan.body).toEqual([{ event: "message.status", url: a }]);
    expect(effectOf(plan.effects, "message.received")).toEqual({
      event: "message.received",
      url: a,
      wasSigned: true,
      signed: false,
      newSecret: false,
      change: "removed",
    });
  });
});

describe("initialForm", () => {
  it('gives "one" when all the stored webhooks of the form have the same URL', () => {
    const stored = [hook("message.received", a, true), hook("whatsmeow.Receipt", a)];
    expect(initialForm(stored, "WHATSAPP_WEB")).toEqual({
      form: {
        urlMode: "one",
        url: a,
        events: [{ event: "message.received" }, { event: "whatsmeow.Receipt" }],
      },
      secrets: { one: { kind: "keep" } },
    });
  });

  it('gives "one" when there is no webhook', () => {
    expect(initialForm([], "META_CLOUD")).toEqual({
      form: { urlMode: "one", url: "", events: [] },
      secrets: { one: { kind: "keep" } },
    });
  });

  it('gives "each" when the URLs are different, with every control on keep', () => {
    const stored = [hook("message.received", a), hook("message.status", b)];
    expect(initialForm(stored, "WHATSAPP_WEB")).toEqual({
      form: {
        urlMode: "each",
        events: [
          { event: "message.received", url: a },
          { event: "message.status", url: b },
        ],
      },
      secrets: {
        each: { "message.received": { kind: "keep" }, "message.status": { kind: "keep" } },
      },
    });
  });

  it("ignores an unknown webhook with another URL", () => {
    const stored = [hook("message.received", a), hook("meta.webhook", b)];
    expect(initialForm(stored, "WHATSAPP_WEB").form).toEqual({
      urlMode: "one",
      url: a,
      events: [{ event: "message.received" }],
    });
  });
});

describe("matchesPlan", () => {
  const stored = [
    hook("message.received", a, true),
    hook("message.status", a),
    hook("meta.webhook", b, true),
  ];
  const form = one(a, "message.received", "connection.update");

  it("gives true for the answer of the plan", () => {
    const plan = planned(stored, "WHATSAPP_WEB", form, { one: { kind: "none" } });
    const answer = [
      hook("connection.update", a),
      hook("meta.webhook", b, true),
      hook("message.received", a),
    ];
    expect(matchesPlan(answer, plan.effects)).toBe(true);
  });

  it("gives false for a different has_secret", () => {
    const plan = planned(stored, "WHATSAPP_WEB", form, { one: { kind: "none" } });
    const answer = [
      hook("message.received", a, true),
      hook("connection.update", a),
      hook("meta.webhook", b, true),
    ];
    expect(matchesPlan(answer, plan.effects)).toBe(false);
  });

  it("gives false for a missing, an extra or a moved webhook", () => {
    const plan = planned(stored, "WHATSAPP_WEB", form, { one: { kind: "none" } });
    const answer = [
      hook("message.received", a),
      hook("connection.update", a),
      hook("meta.webhook", b, true),
    ];
    expect(matchesPlan(answer.slice(1), plan.effects)).toBe(false);
    expect(matchesPlan([...answer, hook("message.status", a)], plan.effects)).toBe(false);
    expect(matchesPlan([hook("message.received", b), ...answer.slice(1)], plan.effects)).toBe(
      false,
    );
  });

  // has_secret cannot show a new secret on a webhook that was signed.
  it("gives false when the plan puts a new secret on a signed webhook", () => {
    const plan = planned(stored, "WHATSAPP_WEB", form, { one: { kind: "set", value: "s1" } });
    const answer = [
      hook("message.received", a, true),
      hook("connection.update", a, true),
      hook("meta.webhook", b, true),
    ];
    expect(matchesPlan(answer, plan.effects)).toBe(false);
  });

  it("gives true when the plan puts a new secret on an unsigned webhook", () => {
    const unsigned = [hook("message.received", a)];
    const plan = planned(unsigned, "WHATSAPP_WEB", one(a, "message.received"), {
      one: { kind: "set", value: "s1" },
    });
    expect(matchesPlan([hook("message.received", a, true)], plan.effects)).toBe(true);
  });
});

describe("generateSecret", () => {
  it("gives 64 hex characters, new each time", () => {
    const secret = generateSecret();
    expect(secret).toMatch(/^[0-9a-f]{64}$/);
    expect(generateSecret()).not.toBe(secret);
  });
});

describe("secretBytes", () => {
  it("counts UTF-8 bytes", () => {
    expect(secretBytes("abc")).toBe(3);
    expect(secretBytes("ñ")).toBe(2);
    expect(secretBytes("😀")).toBe(4);
  });
});
