import { describe, expect, it } from "vitest";
import type { components } from "../api/generated/types.ts";
import {
  type Draft,
  draftOf,
  fieldErrors,
  formOf,
  keepRefused,
  sameList,
  secretsOf,
  unsavedSecret,
  withMode,
} from "./webhooks-draft.ts";

type Webhook = components["schemas"]["Webhook"];

const a = "https://a.example/hook";
const b = "https://b.example/hook";
const hook = (event: string, url: string, hasSecret = false): Webhook => ({
  event,
  url,
  has_secret: hasSecret,
});

const draft = (extra: Partial<Draft>): Draft => ({
  mode: "one",
  url: a,
  urls: {},
  events: ["message.received"],
  one: { kind: "keep" },
  each: {},
  ...extra,
});

describe("draftOf and formOf", () => {
  it("opens in one URL for all, and gives the rows in the order of the page", () => {
    const d = draftOf([hook("message.status", a), hook("connection.update", a)], "WHATSAPP_WEB");
    expect(d.mode).toBe("one");
    expect(formOf(d, "WHATSAPP_WEB")).toEqual({
      urlMode: "one",
      url: a,
      events: [{ event: "connection.update" }, { event: "message.status" }],
    });
  });

  it("sends the URLs without spaces at the start and the end", () => {
    expect(formOf(draft({ url: `  ${a} ` }), "WHATSAPP_WEB").url).toBe(a);
    const each = draft({ mode: "each", urls: { "message.received": ` ${b}\t` } });
    expect(formOf(each, "WHATSAPP_WEB").events).toEqual([{ event: "message.received", url: b }]);
  });

  it("opens in one URL for each event when the URLs differ", () => {
    const d = draftOf([hook("message.status", a), hook("connection.update", b)], "WHATSAPP_WEB");
    expect(formOf(d, "WHATSAPP_WEB")).toEqual({
      urlMode: "each",
      events: [
        { event: "connection.update", url: b },
        { event: "message.status", url: a },
      ],
    });
  });
});

describe("withMode", () => {
  it("gives the one URL to a row with no URL, and keeps the URL of a row", () => {
    const d = withMode(
      draft({ events: ["message.received", "message.status"], urls: { "message.status": b } }),
      "each",
    );
    expect(d.urls).toEqual({ "message.received": a, "message.status": b });
  });

  it("takes the URL of the first row when the one URL is empty", () => {
    const d = draft({ mode: "each", url: "", urls: { "message.received": b } });
    expect(withMode(d, "one").url).toBe(b);
  });
});

describe("secretsOf and unsavedSecret", () => {
  it("sends a typed or generated value as a set action", () => {
    expect(secretsOf(draft({ one: { kind: "type", value: "s" } }))).toEqual({
      one: { kind: "set", value: "s" },
    });
    const each = draft({
      mode: "each",
      urls: { "message.received": a },
      each: { "message.received": { kind: "generate", value: "g", saved: true } },
    });
    expect(secretsOf(each)).toEqual({ each: { "message.received": { kind: "set", value: "g" } } });
  });

  it("counts only a generated secret with no I saved it, on a control of the page", () => {
    const generated = { kind: "generate", value: "g", saved: false } as const;
    expect(unsavedSecret(draft({ one: generated }))).toBe(true);
    expect(unsavedSecret(draft({ one: { ...generated, saved: true } }))).toBe(false);
    // The control of a cleared row is not on the page.
    expect(unsavedSecret(draft({ mode: "each", events: [], each: { x: generated } }))).toBe(false);
  });
});

describe("fieldErrors", () => {
  it("accepts http and https with a host", () => {
    expect(fieldErrors(draft({ url: "http://n8n.local:5678/webhook" })).url).toEqual({});
  });

  it("refuses another scheme, no host, and more than 2048 bytes", () => {
    for (const url of ["ftp://a.example", "https://", "a.example/hook", `${a}?${"é".repeat(1020)}`])
      expect(fieldErrors(draft({ url })).url.one).toBeDefined();
  });

  it("checks the URL without spaces at the start and the end", () => {
    expect(fieldErrors(draft({ url: ` ${a} ` })).url).toEqual({});
    expect(fieldErrors(draft({ url: `   ${"x".repeat(2040)}` })).url.one).toBeDefined();
    // 2048 bytes after the trim pass, with spaces around them.
    const long = `https://a.example/${"x".repeat(2048 - 18)}`;
    expect(fieldErrors(draft({ url: `  ${long}  ` })).url).toEqual({});
  });

  it("needs no URL when no event is selected", () => {
    expect(fieldErrors(draft({ url: "", events: [] })).url).toEqual({});
  });

  it("checks the URL of each row", () => {
    const d = draft({ mode: "each", urls: { "message.received": "nope" } });
    expect(Object.keys(fieldErrors(d).url)).toEqual(["message.received"]);
  });

  it("counts a typed secret in UTF-8 bytes, from 1 to 256", () => {
    const typed = (value: string) => fieldErrors(draft({ one: { kind: "type", value } })).secret;
    expect(typed("")).toHaveProperty("one");
    expect(typed("x".repeat(256))).toEqual({});
    expect(typed("é".repeat(129))).toHaveProperty("one");
  });
});

describe("keepRefused", () => {
  it("names the one control when a new event joins a signed webhook", () => {
    const d = draft({ events: ["message.received", "message.status"] });
    expect(keepRefused([hook("message.received", a, true)], "WHATSAPP_WEB", d)).toEqual(
      new Set(["one"]),
    );
  });

  it("names the row of a signed webhook with a new URL in one URL for each event", () => {
    const d = draft({ mode: "each", urls: { "message.received": b } });
    expect(keepRefused([hook("message.received", a, true)], "WHATSAPP_WEB", d)).toEqual(
      new Set(["message.received"]),
    );
  });

  it("is empty when Keep the secret is safe", () => {
    expect(keepRefused([hook("message.received", a, true)], "WHATSAPP_WEB", draft({}))).toEqual(
      new Set(),
    );
  });
});

describe("sameList", () => {
  it("compares the events, the URLs and has_secret, in any order", () => {
    const list = [hook("message.received", a), hook("message.status", b, true)];
    expect(sameList(list, [...list].reverse())).toBe(true);
    expect(sameList(list, [hook("message.received", a), hook("message.status", b)])).toBe(false);
    expect(sameList(list, list.slice(1))).toBe(false);
  });
});
