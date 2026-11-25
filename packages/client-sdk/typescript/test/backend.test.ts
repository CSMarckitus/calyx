import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import {
  createCallCodes,
  createCallOperations,
  createCallRetry,
} from "../src/backend/create-calls.js";
import { setSizeLimitsForTest } from "../src/backend/http.js";
import { createBackendClient } from "../src/backend/index.js";
import { operations } from "../src/backend/operations.js";
import { CalyxError, isCalyxError } from "../src/errors/index.js";
import { newIdempotencyKey } from "../src/shared/key.js";
import { version } from "../src/version.js";
import { problem, type Reply, startFakeBackend } from "./fake-backend.js";

const sent: Reply = { body: { message_id: "m1", replayed: false } };

async function withBackend(
  reply: (req: { method: string; url: string }) => Reply,
  run: (b: Awaited<ReturnType<typeof startFakeBackend>>) => Promise<void>,
) {
  const b = await startFakeBackend(reply);
  try {
    await run(b);
  } finally {
    await b.close();
  }
}

async function caught(p: Promise<unknown>): Promise<CalyxError> {
  try {
    await p;
  } catch (err) {
    assert.ok(isCalyxError(err), `not a CalyxError: ${String(err)}`);
    return err;
  }
  throw new assert.AssertionError({ message: "the call did not fail" });
}

test("newIdempotencyKey makes a UUID each time", () => {
  const a = newIdempotencyKey();
  assert.match(a, /^[0-9a-f-]{36}$/);
  assert.notEqual(a, newIdempotencyKey());
});

test("sendMessage makes an idempotency key and returns it", async () => {
  await withBackend(
    () => sent,
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl + "/", apiKey: "k-secret" });
      const result = await client.sendMessage({ id: "inst 1", to: "34600", text: "hi" });
      assert.deepEqual(result.data, { message_id: "m1", replayed: false });
      assert.equal(b.requests.length, 1);
      const req = b.requests[0]!;
      assert.equal(req.method, "POST");
      assert.equal(req.url, "/api/v1/instances/inst%201/messages");
      assert.equal(req.headers["idempotency-key"], result.idempotencyKey);
      assert.equal(req.headers.authorization, "Bearer k-secret");
      assert.equal(req.headers["content-type"], "application/json");
      // The path parameter stays out of the body. The SDK keeps the wire names.
      assert.deepEqual(JSON.parse(req.body), { to: "34600", text: "hi" });
    },
  );
});

test("sendMessage sends the idempotency key of the program", async () => {
  await withBackend(
    () => sent,
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl });
      const result = await client.sendMessage(
        { id: "i", to: "1", text: "t" },
        { idempotencyKey: "mine" },
      );
      assert.equal(result.idempotencyKey, "mine");
      assert.equal(b.requests[0]!.headers["idempotency-key"], "mine");
    },
  );
});

test("createBulkJob has the same idempotency key rule", async () => {
  await withBackend(
    () => ({ status: 202, body: { id: "j1" } }),
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl });
      const params = { id: "i" } as unknown as Parameters<typeof client.createBulkJob>[0];
      const result = await client.createBulkJob(params);
      assert.equal(b.requests[0]!.headers["idempotency-key"], result.idempotencyKey);
      const again = await client.createBulkJob(params, { idempotencyKey: "bulk-1" });
      assert.equal(again.idempotencyKey, "bulk-1");
    },
  );
});

test("every error has the key, and a retry reuses it", async () => {
  let n = 0;
  await withBackend(
    () => (n++ === 0 ? problem(504, "send_outcome_unknown", "no answer") : sent),
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl });
      const params = { id: "i", to: "1", text: "t" };
      const err = await caught(client.sendMessage(params));
      assert.equal(err.source, "backend");
      assert.equal(err.code, "send_outcome_unknown");
      assert.equal(err.status, 504);
      assert.equal(err.retry, "retry_now");
      assert.equal(err.requestId, "req-1");
      assert.equal(err.idempotencyKey, b.requests[0]!.headers["idempotency-key"]);
      assert.equal(b.requests.length, 1, "the SDK never resends");
      const ok = await client.sendMessage(params, { idempotencyKey: err.idempotencyKey });
      assert.equal(b.requests[1]!.headers["idempotency-key"], err.idempotencyKey);
      assert.equal(ok.idempotencyKey, err.idempotencyKey);
    },
  );
});

test("a network error and a given key also carry the key", async () => {
  const client = createBackendClient({
    baseUrl: "http://x.invalid",
    fetch: () => Promise.reject(new TypeError("fetch failed")),
  });
  const err = await caught(
    client.sendMessage({ id: "i", to: "1", text: "t" }, { idempotencyKey: "k9" }),
  );
  assert.equal(err.code, "network_error");
  assert.equal(err.idempotencyKey, "k9");
});

test("a call that is not a send has no idempotency key, no body and a plain result", async () => {
  await withBackend(
    () => ({ body: { id: "i1", name: "n" } }),
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl, apiKey: "k" });
      const data = await client.getInstance({ id: "i1" });
      assert.deepEqual(data, { id: "i1", name: "n" });
      const req = b.requests[0]!;
      assert.equal(req.method, "GET");
      assert.equal(req.headers["idempotency-key"], undefined);
      assert.equal(req.headers["content-type"], undefined);
      assert.equal(req.body, "");
    },
  );
});

test("a 204 answer returns undefined", async () => {
  await withBackend(
    () => ({ status: 204 }),
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl });
      assert.equal(await client.removeInstance({ id: "i" }), undefined);
      assert.equal(b.requests[0]!.method, "DELETE");
    },
  );
});

test("without apiKey the request has no Authorization header", async () => {
  await withBackend(
    () => ({ body: {} }),
    async (b) => {
      await createBackendClient({ baseUrl: b.baseUrl }).getMe();
      assert.equal(b.requests[0]!.headers.authorization, undefined);
    },
  );
});

test("an HTML proxy page gives invalid_response", async () => {
  for (const status of [200, 502]) {
    await withBackend(
      () => ({
        status,
        headers: { "content-type": "text/html" },
        body: "<html>Bad gateway</html>",
      }),
      async (b) => {
        const err = await caught(createBackendClient({ baseUrl: b.baseUrl }).getMe());
        assert.equal(err.source, "client");
        assert.equal(err.code, "invalid_response");
        assert.equal(err.retry, "retry_now");
      },
    );
  }
});

test("the time limit gives timeout, and the program's signal gives aborted", async () => {
  await withBackend(
    () => ({ hang: true }),
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl });
      const slow = await caught(client.getMe(undefined, { timeoutMs: 40 }));
      assert.equal(slow.code, "timeout");
      assert.equal(slow.source, "client");

      const controller = new AbortController();
      setTimeout(() => controller.abort(), 40);
      const stopped = await caught(client.getMe(undefined, { signal: controller.signal }));
      assert.equal(stopped.code, "aborted");

      const early = await caught(client.getMe(undefined, { signal: AbortSignal.abort() }));
      assert.equal(early.code, "aborted");
    },
  );
});

test("a refused connection gives network_error", async () => {
  const b = await startFakeBackend(() => ({}));
  const baseUrl = b.baseUrl;
  await b.close();
  const err = await caught(createBackendClient({ baseUrl }).getMe());
  assert.equal(err.code, "network_error");
  assert.equal(err.retry, "retry_now");
});

test("Retry-After 60 gives retryAfter 60", async () => {
  await withBackend(
    () => problem(429, "rate_limited", "slow", { "retry-after": "60" }),
    async (b) => {
      const err = await caught(createBackendClient({ baseUrl: b.baseUrl }).getMe());
      assert.equal(err.code, "rate_limited");
      assert.equal(err.retryAfter, 60);
      assert.equal(err.retry, "retry_later");
    },
  );
});

test("the undici header timeout gives timeout", async () => {
  const failure = Object.assign(new TypeError("fetch failed"), {
    cause: { code: "UND_ERR_HEADERS_TIMEOUT" },
  });
  const client = createBackendClient({
    baseUrl: "http://x.invalid",
    fetch: () => Promise.reject(failure),
  });
  const err = await caught(client.sendMessage({ id: "i", to: "1", text: "t" }));
  assert.equal(err.code, "timeout");
  assert.equal(err.retry, "retry_now");
  assert.match(err.idempotencyKey ?? "", /^[0-9a-f-]{36}$/);
});

test("the call timeoutMs has priority over the client timeoutMs", async () => {
  await withBackend(
    () => ({ hang: true }),
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl, timeoutMs: 60_000 });
      const err = await caught(client.getMe(undefined, { timeoutMs: 40 }));
      assert.equal(err.code, "timeout");
    },
  );
  await withBackend(
    () => ({ delayMs: 120, body: {} }),
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl, timeoutMs: 40 });
      assert.equal((await caught(client.getMe())).code, "timeout");
      assert.deepEqual(await client.getMe(undefined, { timeoutMs: 5_000 }), {});
    },
  );
});

test("a problem body without a code gives invalid_response", async () => {
  await withBackend(
    () => ({ status: 500, body: { message: "boom" } }),
    async (b) => {
      const err = await caught(createBackendClient({ baseUrl: b.baseUrl }).getMe());
      assert.equal(err.code, "invalid_response");
    },
  );
});

test("field errors and an unknown code are kept", async () => {
  const reply: Reply = {
    status: 422,
    headers: { "content-type": "application/problem+json" },
    body: {
      status: 422,
      code: "brand_new",
      detail: "bad",
      errors: [{ location: "body.to", message: "empty" }],
    },
  };
  await withBackend(
    () => reply,
    async (b) => {
      const err = await caught(createBackendClient({ baseUrl: b.baseUrl }).getMe());
      assert.equal(err.code, "brand_new");
      assert.equal(err.retry, "unknown");
      assert.equal(err.status, 422);
      assert.deepEqual(err.errors, [{ location: "body.to", message: "empty" }]);
    },
  );
});

test("a write with only path parameters sends an empty JSON body", async () => {
  await withBackend(
    () => ({ body: {} }),
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl });
      await client.pairInstance({ id: "i" });
      const req = b.requests[0]!;
      assert.equal(req.body, "{}");
      assert.equal(req.headers["content-type"], "application/json");
    },
  );
});

test("a failed body read gives network_error with a neutral detail", async () => {
  const broken = {
    ok: true,
    status: 200,
    headers: new Headers(),
    text: () => Promise.reject(new TypeError("terminated")),
  } as unknown as Response;
  const client = createBackendClient({ baseUrl: "http://x", fetch: () => Promise.resolve(broken) });
  const err = await caught(client.getMe());
  assert.equal(err.code, "network_error");
  assert.doesNotMatch(err.detail, /did not reach/);
});

test("an empty, non-numeric or negative Retry-After gives no retryAfter", async () => {
  for (const value of ["", "soon", "-5"]) {
    await withBackend(
      () => problem(429, "rate_limited", "slow", { "retry-after": value }),
      async (b) => {
        const err = await caught(createBackendClient({ baseUrl: b.baseUrl }).getMe());
        assert.equal(err.retryAfter, undefined, JSON.stringify(value));
      },
    );
  }
});

test("a request carries the User-Agent of the SDK in Node", async () => {
  await withBackend(
    () => ({ body: {} }),
    async (b) => {
      await createBackendClient({ baseUrl: b.baseUrl }).getMe();
      assert.equal(b.requests[0]!.headers["user-agent"], `calyx-typescript-sdk/${version}`);
    },
  );
});

test("an answer over the size limit gives invalid_response", async () => {
  setSizeLimitsForTest(1024, 1024);
  try {
    await withBackend(
      () => ({ body: `{"a":"${"x".repeat(2048)}"}` }),
      async (b) => {
        const err = await caught(createBackendClient({ baseUrl: b.baseUrl }).getMe());
        assert.equal(err.code, "invalid_response");
      },
    );
    // The error body of a non-2xx answer has the same limit.
    await withBackend(
      () => ({ status: 500, body: "y".repeat(2048) }),
      async (b) => {
        const err = await caught(createBackendClient({ baseUrl: b.baseUrl }).getMe());
        assert.equal(err.code, "invalid_response");
      },
    );
    // A body of exactly the limit is accepted.
    const exact = `{"a":"${"x".repeat(1024 - 8)}"}`;
    assert.equal(exact.length, 1024);
    await withBackend(
      () => ({ body: exact }),
      async (b) => {
        await createBackendClient({ baseUrl: b.baseUrl }).getMe();
      },
    );
  } finally {
    setSizeLimitsForTest(32 * 1024 * 1024, 1024 * 1024);
  }
});

test("createBulkJob has a 150 second time limit", () => {
  assert.equal(operations.createBulkJob.timeoutMs, 150_000);
});

// The create-call rule of CONTRACT.md part 6.
const createCalls = JSON.parse(
  readFileSync(new URL("../../testdata/create-calls.json", import.meta.url), "utf8"),
) as { operations: string[]; codes: string[]; retry: string };

test("create-calls.json lists the operations and codes of the code", () => {
  assert.deepEqual([...createCallOperations].sort(), [...createCalls.operations].sort());
  assert.deepEqual([...createCallCodes].sort(), [...createCalls.codes].sort());
  assert.equal(createCallRetry, createCalls.retry);
});

const callParams: Record<string, Record<string, unknown>> = {
  createClient: { name: "n" },
  createInstance: { display_name: "n", provider: "WHATSAPP_WEB" },
  updateClient: { id: "c1" },
};

const refusingFetch = (async () => {
  throw new TypeError("fetch failed");
}) as unknown as typeof fetch;

// One way to get each of the four client codes.
async function failWith(code: string, id: string): Promise<CalyxError> {
  if (code === "invalid_response") {
    return withBackendResult(() => ({ body: "not json" }), id, {});
  }
  if (code === "network_error") {
    return withBackendResult(() => ({ body: "x" }), id, {}, { fetch: refusingFetch });
  }
  if (code === "timeout") {
    return withBackendResult(() => ({ hang: true }), id, { timeoutMs: 50 });
  }
  const controller = new AbortController();
  controller.abort();
  return withBackendResult(() => ({ hang: true }), id, { signal: controller.signal });
}

async function withBackendResult(
  reply: () => Reply,
  id: string,
  options: { timeoutMs?: number; signal?: AbortSignal },
  config: { fetch?: typeof fetch } = {},
): Promise<CalyxError> {
  let err: CalyxError | undefined;
  await withBackend(reply, async (b) => {
    const client = createBackendClient({ baseUrl: b.baseUrl, ...config });
    const call = client[id as "createClient"] as (p: unknown, o: unknown) => Promise<unknown>;
    err = await caught(call(callParams[id], options));
  });
  return err as CalyxError;
}

for (const op of createCalls.operations) {
  for (const code of createCalls.codes) {
    test(`${op} gives unknown for the client error ${code}`, async () => {
      const err = await failWith(code, op);
      assert.equal(err.source, "client");
      assert.equal(err.code, code);
      assert.equal(err.retry, "unknown");
    });
  }
}

test("updateClient keeps the advice of the table", async () => {
  const err = await failWith("network_error", "updateClient");
  assert.equal(err.code, "network_error");
  assert.equal(err.retry, "retry_now");
});

test("a create call keeps the advice of a backend error", async () => {
  await withBackend(
    () => problem(404, "not_found", "no"),
    async (b) => {
      const client = createBackendClient({ baseUrl: b.baseUrl });
      const err = await caught(client.createClient({ name: "n" }));
      assert.equal(err.source, "backend");
      assert.equal(err.retry, "do_not_retry");
    },
  );
});
