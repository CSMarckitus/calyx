import type { components } from "../api/generated/types.ts";
import {
  type Action,
  initialForm,
  type Provider,
  pageEvents,
  planWebhooks,
  type SecretControls,
  secretBytes,
  type WebhookForm,
} from "./webhooks.ts";

// The state of the form of the webhooks view, and the field checks of the screen.
// The rules of a save stay in domain/webhooks.ts.

type Webhook = components["schemas"]["Webhook"];

export const maxWebhooks = 20;
const maxUrlBytes = 2048;
const urlText = "Type an address that starts with https:// or http://, with at most 2048 bytes.";
const secretText = "Use 1 to 256 bytes.";

// The choice of one secret control. A generated value lives only in the
// state of the view.
export type SecretChoice =
  | { kind: "keep" }
  | { kind: "type"; value: string }
  | { kind: "generate"; value: string; saved: boolean }
  | { kind: "none" };

// The state of the form. It keeps the URL of each event and both kinds of secret control, so a
// change of the URL mode loses nothing. A secret control has the key "one" or an event.
export type Draft = {
  mode: "one" | "each";
  url: string;
  urls: Record<string, string>;
  events: string[];
  one: SecretChoice;
  each: Record<string, SecretChoice>;
};

const keep: SecretChoice = { kind: "keep" };

// Every secret control of `initialForm` is "keep".
export function draftOf(stored: Webhook[], provider: Provider): Draft {
  const { form } = initialForm(stored, provider);
  return {
    mode: form.urlMode,
    url: form.url ?? "",
    urls: Object.fromEntries(form.events.map((r) => [r.event, r.url ?? ""])),
    events: form.events.map((r) => r.event),
    one: keep,
    each: {},
  };
}

// The URLs go without spaces at the start and the end, as the field checks see them.
export function formOf(d: Draft, provider: Provider): WebhookForm {
  const order = pageEvents(provider);
  const events = [...d.events].sort((a, b) => order.indexOf(a) - order.indexOf(b));
  if (d.mode === "one")
    return { urlMode: "one", url: d.url.trim(), events: events.map((event) => ({ event })) };
  return {
    urlMode: "each",
    events: events.map((event) => ({ event, url: (d.urls[event] ?? "").trim() })),
  };
}

export const choiceOf = (d: Draft, scope: string) =>
  scope === "one" ? d.one : (d.each[scope] ?? keep);
// The secret controls on the page: one, or one on each selected row.
export const scopesOf = (d: Draft) => (d.mode === "one" ? ["one"] : d.events);

function actionOf(c: SecretChoice): Action {
  if (c.kind === "type" || c.kind === "generate") return { kind: "set", value: c.value };
  return { kind: c.kind };
}

export function secretsOf(d: Draft): SecretControls {
  if (d.mode === "one") return { one: actionOf(d.one) };
  return { each: Object.fromEntries(d.events.map((e) => [e, actionOf(choiceOf(d, e))])) };
}

// A change of the URL mode keeps the URLs: a new row takes the one URL, and the one URL takes
// the URL of the first row.
export function withMode(d: Draft, mode: Draft["mode"]): Draft {
  if (mode === d.mode) return d;
  if (mode === "each") {
    const urls = { ...d.urls };
    for (const e of d.events) urls[e] ||= d.url;
    return { ...d, mode, urls };
  }
  return { ...d, mode, url: d.url || (d.events.map((e) => d.urls[e]).find(Boolean) ?? "") };
}

// A generated secret with no "I saved it".
export const unsavedSecret = (d: Draft) =>
  scopesOf(d).some((s) => {
    const c = choiceOf(d, s);
    return c.kind === "generate" && !c.saved;
  });

// The URL rule of the backend: http or https, a host, at most 2048 bytes.
function urlOk(text: string): boolean {
  const url = text.trim();
  if (secretBytes(url) > maxUrlBytes) return false;
  try {
    const u = new URL(url);
    return (u.protocol === "http:" || u.protocol === "https:") && u.host !== "";
  } catch {
    return false;
  }
}

// The field checks of the screen. The keys are the keys of the secret controls.
export function fieldErrors(d: Draft) {
  const url: Record<string, string> = {};
  const secret: Record<string, string> = {};
  if (d.mode === "one" && d.events.length > 0 && !urlOk(d.url)) url.one = urlText;
  if (d.mode === "each") for (const e of d.events) if (!urlOk(d.urls[e] ?? "")) url[e] = urlText;
  for (const s of scopesOf(d)) {
    const c = choiceOf(d, s);
    if (c.kind === "type" && (secretBytes(c.value) < 1 || secretBytes(c.value) > 256))
      secret[s] = secretText;
  }
  return { url, secret };
}

// The secret controls where "Keep the secret" makes the plan refuse the save.
export function keepRefused(base: Webhook[], provider: Provider, d: Draft): Set<string> {
  const allKeep: SecretControls = d.mode === "one" ? { one: { kind: "keep" } } : { each: {} };
  const plan = planWebhooks(base, provider, formOf(d, provider), allKeep);
  if (plan.ok) return new Set();
  return new Set(d.mode === "one" ? ["one"] : plan.rows);
}

// Two lists are equal with the same events, URLs and has_secret.
export const sameList = (a: Webhook[], b: Webhook[]) =>
  a.length === b.length &&
  a.every((h) =>
    b.some((g) => g.event === h.event && g.url === h.url && g.has_secret === h.has_secret),
  );
