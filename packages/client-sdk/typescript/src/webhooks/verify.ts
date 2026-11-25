import { CalyxError } from "../errors/index.js";
import { type CalyxEvent, invalidEvent, toCalyxEvent } from "./events.js";

export interface VerifyWebhookInput {
  // The received bytes. Never JSON that a program parsed and encoded again.
  rawBody: string | Uint8Array;
  headers: Headers | Record<string, string | string[] | undefined>;
  secret: string;
  toleranceSeconds?: number;
  now?: Date;
}

function reject(code: "invalid_signature" | "stale_timestamp", detail: string): CalyxError {
  return new CalyxError({ source: "client", code, status: 0, detail });
}

function header(headers: VerifyWebhookInput["headers"], name: string): string | undefined {
  // A duck-type check, because a Headers object of another realm or a polyfill fails instanceof.
  if (typeof (headers as Headers).get === "function")
    return (headers as Headers).get(name) ?? undefined;
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() !== name) continue;
    return Array.isArray(value) ? value[0] : value;
  }
  return undefined;
}

function fromHex(hex: string): Uint8Array<ArrayBuffer> | undefined {
  if (hex.length % 2 !== 0 || !/^[0-9a-fA-F]*$/.test(hex)) return undefined;
  const bytes = new Uint8Array(hex.length / 2);
  for (let i = 0; i < bytes.length; i++) bytes[i] = parseInt(hex.slice(2 * i, 2 * i + 2), 16);
  return bytes;
}

export async function verifyWebhook(input: VerifyWebhookInput): Promise<CalyxEvent> {
  const { rawBody, headers, secret, toleranceSeconds = 300, now = new Date() } = input;
  if (secret === "") throw reject("invalid_signature", "The webhook secret is empty.");

  const timestamp = header(headers, "x-calyx-timestamp");
  const signature = header(headers, "x-calyx-signature");
  if (timestamp === undefined || signature === undefined) {
    throw reject("invalid_signature", "The signature or the timestamp header is missing.");
  }
  if (!/^\d+$/.test(timestamp)) {
    throw reject("invalid_signature", "The timestamp header is not a number.");
  }
  // Written as a negation, so a NaN `now` or tolerance fails closed.
  if (!(Math.abs(now.getTime() / 1000 - Number(timestamp)) <= toleranceSeconds)) {
    throw reject("stale_timestamp", "The timestamp is outside the tolerance.");
  }

  const expected = signature.startsWith("sha256=") ? fromHex(signature.slice(7)) : undefined;
  if (expected === undefined)
    throw reject("invalid_signature", "The signature has a wrong format.");

  const encoder = new TextEncoder();
  const body = typeof rawBody === "string" ? encoder.encode(rawBody) : rawBody;
  const prefix = encoder.encode(`${timestamp}.`);
  const signed = new Uint8Array(prefix.length + body.length);
  signed.set(prefix);
  signed.set(body, prefix.length);

  const key = await globalThis.crypto.subtle.importKey(
    "raw",
    encoder.encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["verify"],
  );
  // `verify` compares the two values in constant time.
  if (!(await globalThis.crypto.subtle.verify("HMAC", key, expected, signed))) {
    throw reject("invalid_signature", "The signature does not match.");
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(new TextDecoder().decode(body));
  } catch (cause) {
    throw invalidEvent("The body is not valid JSON.", cause);
  }
  return toCalyxEvent(parsed);
}
