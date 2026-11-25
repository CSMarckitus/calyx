import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { backendAdvice, clientAdvice, coreAdvice } from "../src/errors/advice.js";
import { CalyxError, isCalyxError } from "../src/errors/index.js";

// The retry advice of part 6 of CONTRACT.md.
const table = {
  retry_now: [
    "send_outcome_unknown",
    "bulk_outcome_unknown",
    "change_outcome_unknown",
    "UNAVAILABLE",
    "DEADLINE_EXCEEDED",
    "CANCELLED",
    "timeout",
    "network_error",
    "invalid_response",
    "aborted",
    "stream_failed",
  ],
  retry_later: [
    "send_in_progress",
    "instance_queue_full",
    "rate_limited",
    "instance_not_connected",
    "pairing_in_progress",
    "core_unavailable",
    "ERROR_REASON_SEND_IN_PROGRESS",
    "ERROR_REASON_INSTANCE_QUEUE_FULL",
    "RESOURCE_EXHAUSTED",
  ],
  check_if_sent: ["idempotency_key_outcome_unknown", "ERROR_REASON_SEND_OUTCOME_UNKNOWN"],
  do_not_retry: [
    "duplicate_message",
    "idempotency_key_conflict",
    "invalid_request",
    "unauthenticated",
    "csrf_check_failed",
    "owner_only",
    "api_key_not_allowed",
    "instance_creation_not_allowed",
    "whatsapp_web_not_allowed",
    "whatsapp_web_not_acknowledged",
    "not_found",
    "quota_exceeded",
    "phone_number_id_in_use",
    "pairing_not_supported",
    "instance_in_error",
    "instance_connected",
    "instance_not_ready",
    "client_has_instances",
    "default_client",
    "email_in_use",
    "token_invalid",
    "too_many_messages",
    "core_rejected_token",
    "ERROR_REASON_DUPLICATE_MESSAGE",
    "ERROR_REASON_IDEMPOTENCY_KEY_CONFLICT",
    "INVALID_ARGUMENT",
    "NOT_FOUND",
    "ALREADY_EXISTS",
    "PERMISSION_DENIED",
    "FAILED_PRECONDITION",
    "OUT_OF_RANGE",
    "UNIMPLEMENTED",
    "UNAUTHENTICATED",
    "invalid_signature",
    "stale_timestamp",
    "invalid_event",
    "unsupported_runtime",
    "invalid_config",
  ],
  unknown: ["internal", "conflict", "UNKNOWN", "ABORTED", "INTERNAL", "DATA_LOSS"],
} as const;

const clientCodes = new Set([
  "timeout",
  "aborted",
  "network_error",
  "invalid_response",
  "stream_failed",
  "invalid_signature",
  "stale_timestamp",
  "invalid_event",
  "unsupported_runtime",
  "invalid_config",
]);

function make(code: string): CalyxError {
  const source = clientCodes.has(code)
    ? "client"
    : code === code.toUpperCase()
      ? "core"
      : "backend";
  return new CalyxError({ source, code, status: 0, detail: "x" });
}

for (const [retry, codes] of Object.entries(table)) {
  for (const code of codes) {
    test(`${code} gives ${retry}`, () => {
      assert.equal(make(code).retry, retry);
    });
  }
}

test("an unknown code stays a string and gives unknown", () => {
  const err = make("brand_new_code");
  assert.equal(err.code, "brand_new_code");
  assert.equal(err.retry, "unknown");
});

test("the advice lookup is keyed by source", () => {
  // A backend code under the source "core" is an unknown core code.
  const err = new CalyxError({ source: "core", code: "send_in_progress", status: 14, detail: "x" });
  assert.equal(err.retry, "unknown");
  assert.equal(make("send_in_progress").retry, "retry_later");
});

test("isCalyxError checks the code", () => {
  const err: unknown = make("duplicate_message");
  assert.equal(isCalyxError(err), true);
  assert.equal(isCalyxError(err, "duplicate_message"), true);
  assert.equal(isCalyxError(err, "not_found"), false);
  assert.equal(isCalyxError(new Error("x"), "duplicate_message"), false);
  assert.equal(isCalyxError(new Error("x")), false);
});

test("CalyxError keeps its optional fields", () => {
  const err = new CalyxError({
    source: "backend",
    code: "rate_limited",
    status: 429,
    detail: "slow down",
    requestId: "r1",
    retryAfter: 60,
    idempotencyKey: "k1",
  });
  assert.equal(err.name, "CalyxError");
  assert.equal(err.message, "slow down");
  assert.equal(err.retryAfter, 60);
  assert.equal(err.requestId, "r1");
  assert.equal(err.idempotencyKey, "k1");
});

test("retry-advice.json and advice.ts have the same rows", () => {
  const fileUrl = new URL("../../testdata/retry-advice.json", import.meta.url);
  const file = JSON.parse(readFileSync(fileUrl, "utf8")) as {
    rows: { source: "backend" | "core" | "client"; code: string; retry: string }[];
  };
  const maps = { backend: backendAdvice, core: coreAdvice, client: clientAdvice };
  const rows = new Map(file.rows.map((r) => [`${r.source}/${r.code}`, r.retry]));
  assert.equal(rows.size, file.rows.length, "a row is repeated");

  for (const [source, map] of Object.entries(maps)) {
    for (const [code, retry] of Object.entries(map)) {
      if (code === "unsupported_runtime") continue;
      assert.equal(rows.get(`${source}/${code}`), retry, `${source}/${code}`);
    }
  }
  for (const row of file.rows) {
    assert.notEqual(row.code, "unsupported_runtime", "the file must not list unsupported_runtime");
    const map: Record<string, string> = maps[row.source];
    assert.equal(map[row.code], row.retry, `${row.source}/${row.code} is not in advice.ts`);
  }
});

test("the field retry overrides the advice of the code", () => {
  const err = new CalyxError({
    source: "client",
    code: "timeout",
    status: 0,
    detail: "x",
    retry: "unknown",
  });
  assert.equal(err.retry, "unknown");
  assert.equal(make("duplicate_message").retry, "do_not_retry");
});
