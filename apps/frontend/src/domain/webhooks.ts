import type { components } from "../api/generated/types.ts";

// The rules of a save of webhooks. The URL rules and the limit
// of 20 webhooks are checks of the screen.

type Webhook = components["schemas"]["Webhook"];
type WebhookInput = components["schemas"]["WebhookInput"];
export type Provider = components["schemas"]["Instance"]["provider"];

// Copied from apps/core/internal/events/types.go, as apps/backend/internal/instances/webhooks.go
// does. Keep the three lists the same.
export const calyxEvents = [
  "connection.update",
  "message.received",
  "message.status",
  "bulk.finished",
];

const whatsmeowEvents = [
  "Message",
  "Receipt",
  "UndecryptableMessage",
  "Presence",
  "ChatPresence",
  "Contact",
  "PushName",
  "JoinedGroup",
  "GroupInfo",
  "Picture",
];

export const rawEvents = (provider: Provider): string[] =>
  provider === "META_CLOUD" ? ["meta.webhook"] : whatsmeowEvents.map((e) => `whatsmeow.${e}`);

// The events that the form shows. A stored webhook with another event is unknown.
export const pageEvents = (provider: Provider): string[] => [
  ...calyxEvents,
  ...rawEvents(provider),
];

// In "one" mode, `url` applies to every event. In "each" mode, every event has its `url`.
export type WebhookForm = {
  urlMode: "one" | "each";
  url?: string;
  events: { event: string; url?: string }[];
};

export type Action = { kind: "keep" } | { kind: "set"; value: string } | { kind: "none" };

// "one" for urlMode "one", "each" for urlMode "each". An absent action is "keep".
export type SecretControls = { one?: Action } | { each?: Record<string, Action> };

// `newSecret` is true when the save sends a secret value: has_secret cannot confirm it on a
// webhook that was signed.
export type Effect = {
  event: string;
  url: string;
  wasSigned: boolean;
  signed: boolean;
  newSecret: boolean;
  change: "same" | "changed-url" | "new" | "removed" | "kept-unknown";
};

export type Plan =
  | { ok: true; body: WebhookInput[]; effects: Effect[] }
  | { ok: false; reason: "secret_needed"; rows: string[] };

const keep: Action = { kind: "keep" };

function actionOf(form: WebhookForm, secrets: SecretControls, event: string): Action {
  if (form.urlMode === "one") return ("one" in secrets && secrets.one) || keep;
  return ("each" in secrets && secrets.each?.[event]) || keep;
}

const urlOf = (form: WebhookForm, row: { url?: string }) =>
  (form.urlMode === "one" ? form.url : row.url) ?? "";

// A row of the form, with its stored webhook and its action.
type Row = { event: string; url: string; stored?: Webhook; action: Action };

function effectOf(row: Row): Effect {
  const wasSigned = row.stored?.has_secret ?? false;
  const { kind } = row.action;
  let change: Effect["change"] = "new";
  if (row.stored) change = row.stored.url === row.url ? "same" : "changed-url";
  // With "keep", the backend keeps the secret only on the same event and URL.
  const signed = kind === "set" || (kind === "keep" && change === "same" && wasSigned);
  return { event: row.event, url: row.url, wasSigned, signed, newSecret: kind === "set", change };
}

// The rule of a save. In "each" mode a row is its own scope, so a new row never joins a
// signed webhook.
function refusedRows(form: WebhookForm, rows: Row[], effects: Effect[]): string[] {
  const refused: string[] = [];
  const signedStays = effects.some((e) => e.change === "same" && e.wasSigned);
  effects.forEach((e, i) => {
    if (rows[i].action.kind !== "keep") return;
    if (e.change === "changed-url" && e.wasSigned) refused.push(e.event);
    if (e.change === "new" && form.urlMode === "one" && signedStays) refused.push(e.event);
  });
  return refused;
}

export function planWebhooks(
  stored: Webhook[],
  provider: Provider,
  form: WebhookForm,
  secrets: SecretControls,
): Plan {
  const known = pageEvents(provider);
  const rows: Row[] = form.events.map((row) => ({
    event: row.event,
    url: urlOf(form, row),
    stored: stored.find((h) => h.event === row.event),
    action: actionOf(form, secrets, row.event),
  }));
  const effects = rows.map(effectOf);

  const refused = refusedRows(form, rows, effects);
  if (refused.length > 0) return { ok: false, reason: "secret_needed", rows: refused };

  const body: WebhookInput[] = rows.map(({ event, url, action }) => {
    if (action.kind === "set") return { event, url, secret: action.value };
    if (action.kind === "none") return { event, url, secret: "" };
    return { event, url };
  });
  const inForm = new Set(form.events.map((row) => row.event));
  for (const h of stored) {
    const base = { event: h.event, url: h.url, wasSigned: h.has_secret, newSecret: false };
    if (!known.includes(h.event)) {
      // An unknown webhook goes back unchanged; absent keeps its secret.
      body.push({ event: h.event, url: h.url });
      effects.push({ ...base, signed: h.has_secret, change: "kept-unknown" });
    } else if (!inForm.has(h.event)) {
      effects.push({ ...base, signed: false, change: "removed" });
    }
  }
  return { ok: true, body, effects };
}

export function initialForm(
  stored: Webhook[],
  provider: Provider,
): { form: WebhookForm; secrets: SecretControls } {
  const known = pageEvents(provider);
  const hooks = stored.filter((h) => known.includes(h.event));
  const urls = new Set(hooks.map((h) => h.url));
  if (urls.size <= 1) {
    const form: WebhookForm = {
      urlMode: "one",
      url: hooks[0]?.url ?? "",
      events: hooks.map((h) => ({ event: h.event })),
    };
    return { form, secrets: { one: keep } };
  }
  const form: WebhookForm = {
    urlMode: "each",
    events: hooks.map((h) => ({ event: h.event, url: h.url })),
  };
  return { form, secrets: { each: Object.fromEntries(hooks.map((h) => [h.event, keep])) } };
}

// True when the fresh list has exactly the events, URLs and has_secret of the plan.
export function matchesPlan(fresh: Webhook[], effects: Effect[]): boolean {
  if (effects.some((e) => e.newSecret && e.wasSigned)) return false;
  const expected = effects.filter((e) => e.change !== "removed");
  if (fresh.length !== expected.length) return false;
  return expected.every((e) =>
    fresh.some((h) => h.event === e.event && h.url === e.url && h.has_secret === e.signed),
  );
}

// 32 random bytes as 64 hex characters.
export function generateSecret(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(32));
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

// The backend counts the bytes of a secret in UTF-8.
export const secretBytes = (secret: string): number => new TextEncoder().encode(secret).length;
