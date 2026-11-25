import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { isCalyxError } from "../src/errors/index.js";
import { parseEvent, verifyWebhook } from "../src/webhooks/index.js";

const vectorUrl = new URL("../../../shared-types/testdata/webhook-signature.json", import.meta.url);
const vector = JSON.parse(readFileSync(vectorUrl, "utf8")) as {
  body: string;
  event: {
    id: string;
    instanceName: string;
    type: string;
    payloadJson: string;
    occurredAt: string;
  };
  headers: Record<string, string>;
  now: number;
  secret: string;
};
const now = new Date(vector.now * 1000);

async function failure(promise: Promise<unknown>, code: string) {
  await assert.rejects(promise, (err) => {
    assert.ok(isCalyxError(err, code as never), `expected ${code}, got ${String(err)}`);
    assert.equal((err as { source: string }).source, "client");
    return true;
  });
}

// Signs a body as the core does, so a test can build bodies that the vector does not cover.
async function sign(body: string, secret: string, timestamp: string) {
  const key = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const mac = new Uint8Array(
    await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(`${timestamp}.${body}`)),
  );
  return "sha256=" + Array.from(mac, (b) => b.toString(16).padStart(2, "0")).join("");
}

test("the shared vector passes, as a string and as bytes", async () => {
  const input = { headers: vector.headers, secret: vector.secret, now };
  const event = await verifyWebhook({ ...input, rawBody: vector.body });
  assert.equal(event.id, "evt_0001");
  assert.equal(event.event, "message.received");
  assert.equal(event.instance, vector.event.instanceName);
  assert.equal(event.timestamp, vector.event.occurredAt);
  assert.deepEqual(event.data, JSON.parse(vector.event.payloadJson));
  const again = await verifyWebhook({ ...input, rawBody: new TextEncoder().encode(vector.body) });
  assert.deepEqual(again, event);
});

test("a wrong secret, a changed body and a missing header give invalid_signature", async () => {
  const base = { rawBody: vector.body, headers: vector.headers, secret: vector.secret, now };
  await failure(verifyWebhook({ ...base, secret: "other-secret" }), "invalid_signature");
  await failure(verifyWebhook({ ...base, rawBody: vector.body + " " }), "invalid_signature");
  const { "X-Calyx-Signature": _s, ...noSignature } = vector.headers;
  await failure(verifyWebhook({ ...base, headers: noSignature }), "invalid_signature");
  const { "X-Calyx-Timestamp": _t, ...noTimestamp } = vector.headers;
  await failure(verifyWebhook({ ...base, headers: noTimestamp }), "invalid_signature");
  const bad = { ...vector.headers, "X-Calyx-Signature": "sha256=zz" };
  await failure(verifyWebhook({ ...base, headers: bad }), "invalid_signature");
  const noPrefix = {
    ...vector.headers,
    "X-Calyx-Signature": vector.headers["X-Calyx-Signature"]!.slice(7),
  };
  await failure(verifyWebhook({ ...base, headers: noPrefix }), "invalid_signature");
});

test("an empty secret gives invalid_signature", async () => {
  await failure(
    verifyWebhook({ rawBody: vector.body, headers: vector.headers, secret: "", now }),
    "invalid_signature",
  );
});

test("a Headers object and a record with other letter case give the same result", async () => {
  const base = { rawBody: vector.body, secret: vector.secret, now };
  const fromHeaders = await verifyWebhook({ ...base, headers: new Headers(vector.headers) });
  const lower = await verifyWebhook({
    ...base,
    headers: {
      "x-calyx-signature": vector.headers["X-Calyx-Signature"]!,
      "x-calyx-timestamp": [vector.headers["X-Calyx-Timestamp"]!, "ignored"],
    },
  });
  assert.deepEqual(lower, fromHeaders);
});

test("a timestamp outside the tolerance gives stale_timestamp", async () => {
  const base = { rawBody: vector.body, headers: vector.headers, secret: vector.secret };
  const at = (seconds: number) => new Date((vector.now + seconds) * 1000);
  await failure(verifyWebhook({ ...base, now: at(301) }), "stale_timestamp");
  await failure(verifyWebhook({ ...base, now: at(-301) }), "stale_timestamp");
  await verifyWebhook({ ...base, now: at(300) });
  await verifyWebhook({ ...base, now: at(-300) });
  await failure(verifyWebhook({ ...base, now: at(11), toleranceSeconds: 10 }), "stale_timestamp");
  await verifyWebhook({ ...base, now: at(10), toleranceSeconds: 10 });
});

test("a body that is not a Calyx event gives invalid_event", async () => {
  const good = JSON.parse(vector.body) as Record<string, unknown>;
  const bodies = [
    "not json",
    "[1]",
    "null",
    JSON.stringify({ ...good, id: undefined }),
    JSON.stringify({ ...good, event: 5 }),
    JSON.stringify({ ...good, instance: undefined }),
    JSON.stringify({ ...good, timestamp: undefined }),
    JSON.stringify({ ...good, data: undefined }),
    JSON.stringify({ ...good, id: "" }),
    JSON.stringify({ ...good, event: "" }),
    JSON.stringify({ ...good, instance: "" }),
  ];
  for (const body of bodies) {
    const timestamp = vector.headers["X-Calyx-Timestamp"]!;
    const headers = {
      "X-Calyx-Timestamp": timestamp,
      "X-Calyx-Signature": await sign(body, vector.secret, timestamp),
    };
    await failure(
      verifyWebhook({ rawBody: body, headers, secret: vector.secret, now }),
      "invalid_event",
    );
  }
});

test("parseEvent maps the fields of an Event", () => {
  const event = parseEvent({
    id: vector.event.id,
    instanceName: vector.event.instanceName,
    type: vector.event.type,
    payloadJson: vector.event.payloadJson,
    occurredAt: new Date(vector.event.occurredAt),
  });
  assert.deepEqual(event, JSON.parse(vector.body));
});

test("an invalid payloadJson, or no time, gives invalid_event", () => {
  const base = { id: "e", instanceName: "i", type: "connection.update", occurredAt: new Date() };
  assert.throws(
    () => parseEvent({ ...base, payloadJson: "{oops" }),
    (err) => isCalyxError(err, "invalid_event"),
  );
  assert.throws(
    () => parseEvent({ ...base, payloadJson: "{}", occurredAt: undefined }),
    (err) => isCalyxError(err, "invalid_event"),
  );
});

test("an invalid now or tolerance fails closed with stale_timestamp", async () => {
  const base = { rawBody: vector.body, headers: vector.headers, secret: vector.secret };
  await failure(verifyWebhook({ ...base, now: new Date(NaN) }), "stale_timestamp");
  await failure(verifyWebhook({ ...base, now, toleranceSeconds: NaN }), "stale_timestamp");
});

test("a Headers object from another realm, with only get, works", async () => {
  const lookalike = {
    get: (name: string) =>
      Object.entries(vector.headers).find(
        ([key]) => key.toLowerCase() === name.toLowerCase(),
      )?.[1] ?? null,
  };
  const event = await verifyWebhook({
    rawBody: vector.body,
    headers: lookalike as unknown as Headers,
    secret: vector.secret,
    now,
  });
  assert.equal(event.id, "evt_0001");
});
