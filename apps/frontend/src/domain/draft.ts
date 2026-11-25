import type { components } from "../api/generated/types.ts";

// The choices of a pairing, in sessionStorage, so a reload keeps them. A draft
// holds no pairing code and no QR: a shared screen must not keep them.

export type DeviceIdentity = NonNullable<
  components["schemas"]["CreateInstanceRequest"]["device_identity"]
>;

export type Draft = {
  userId: string;
  clientId: string;
  kind: "new" | "reconnect";
  name: string;
  sendOnly: boolean;
  // null: the automatic value.
  locale: string | null;
  device: DeviceIdentity | null;
  method: "code" | "qr" | null;
  // `country` is a code of ISO 3166-1, and `text` is what the person typed.
  phone: { country: string; text: string } | null;
  // The time of "started", in ms. null when no attempt runs.
  startedAt: number | null;
};

// The key is the address: the client for a new instance, the instance for a reconnection or a
// started pairing.
export type Where = { client: string } | { instance: string };

const prefix = "calyx.draft.";
const keyOf = (where: Where) =>
  "client" in where ? `${prefix}client.${where.client}` : `${prefix}instance.${where.instance}`;

// Started more than 10 minutes ago: the pairing ended, also if the page did not see the end.
const startedFor = 10 * 60_000;

type Base = Pick<Draft, "userId" | "clientId" | "kind">;

export const newDraft = (base: Base): Draft => ({
  ...base,
  name: "",
  sendOnly: false,
  locale: null,
  device: null,
  method: null,
  phone: null,
  startedAt: null,
});

// sessionStorage can throw (blocked storage). The page then works with no draft.
function load(where: Where): Draft | null {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(keyOf(where)) ?? "null");
    if (typeof value !== "object" || value === null) return null;
    const d = value as Partial<Draft>;
    if (typeof d.userId !== "string" || typeof d.clientId !== "string") return null;
    if (d.kind !== "new" && d.kind !== "reconnect") return null;
    return { ...newDraft(d as Base), ...d } as Draft;
  } catch {
    return null;
  }
}

export function saveDraft(where: Where, draft: Draft): void {
  try {
    sessionStorage.setItem(keyOf(where), JSON.stringify(draft));
  } catch {
    // No storage: a reload loses the draft.
  }
}

export function endDraft(where: Where): void {
  try {
    sessionStorage.removeItem(keyOf(where));
  } catch {
    // No storage: nothing to remove.
  }
}

// A draft of another user is discarded. A draft under the key of a client must be of that client.
export function readDraft(where: Where, userId: string): Draft | null {
  const draft = load(where);
  if (draft === null) {
    endDraft(where);
    return null;
  }
  if (draft.userId !== userId) {
    endDraft(where);
    return null;
  }
  if ("client" in where && draft.clientId !== where.client) return null;
  return draft;
}

export function moveDraft(from: Where, to: Where): void {
  const draft = load(from);
  endDraft(from);
  if (draft) saveDraft(to, draft);
}

// Each start and each 200 of Attach set "started". A tab with no draft (a second tab) gets one
// with no phone number, so a reload of that tab attaches again.
export function markStarted(where: Where, base: Base, now: number): void {
  const draft = load(where);
  const current = draft && draft.userId === base.userId ? draft : newDraft(base);
  saveDraft(where, { ...current, startedAt: now });
}

export function clearStarted(where: Where): void {
  const draft = load(where);
  if (draft) saveDraft(where, { ...draft, startedAt: null });
}

export function startedRecently(where: Where, userId: string, now: number): boolean {
  const draft = load(where);
  if (draft?.userId !== userId || draft.startedAt === null) return false;
  return now - draft.startedAt < startedFor;
}

// "Continue" of an instance with the name of the draft of its client ends that draft: it is the
// same attempt.
export function endDraftNamed(clientId: string, name: string, userId: string): void {
  const where = { client: clientId };
  if (readDraft(where, userId)?.name === name) endDraft(where);
}

// The end of the login session: no draft stays for the next person.
export function clearDrafts(): void {
  try {
    const keys: string[] = [];
    for (let i = 0; i < sessionStorage.length; i++) {
      const key = sessionStorage.key(i);
      if (key?.startsWith(prefix)) keys.push(key);
    }
    for (const key of keys) sessionStorage.removeItem(key);
  } catch {
    // No storage: nothing to clear.
  }
}
