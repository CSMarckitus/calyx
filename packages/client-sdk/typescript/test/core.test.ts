import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { test } from "node:test";

import { Metadata, ServerCredentials, type ServiceError, status } from "@grpc/grpc-js";
import { keepalive } from "../src/core/client.js";
import { coreError } from "../src/core/errors.js";
import { type CoreClient, type CoreClientOptions, createCoreClient } from "../src/core/index.js";
import { CalyxError, isCalyxError } from "../src/errors/index.js";
import {
  CreateInstanceResponse,
  InstanceStatus,
} from "../src/generated/core-grpc/calyx/core/v1/instance.js";
import { type FakeCore, type Reply, startFakeCore } from "./fake-core.js";
import { type Proxy, startProxy } from "./proxy.js";

const token = "core-secret";

async function withCore(
  reply: (method: string, request: unknown) => Reply,
  run: (core: FakeCore, client: CoreClient) => Promise<void>,
  options: Partial<CoreClientOptions> = {},
  credentials?: ServerCredentials,
) {
  const core = await startFakeCore(reply, credentials);
  // With TLS, the name is localhost: grpc-js puts the host in the TLS server name, and Node 26
  // refuses an IP address there.
  const tls = options.tls ?? false;
  const address = tls === false ? core.address : core.address.replace("127.0.0.1", "localhost");
  let client: CoreClient | undefined;
  try {
    // Inside the try: a setup error must also close the fake core, or the test process hangs.
    client = createCoreClient({ address, token, tls: false, ...options });
    await run(core, client);
  } finally {
    client?.close();
    core.close();
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

// Every unary call of the client, with a valid request.
function unaryCalls(client: CoreClient): [string, () => Promise<unknown>][] {
  return [
    ["listInstances", () => client.listInstances()],
    ["getInstanceStatus", () => client.getInstanceStatus({ instanceName: "i" })],
    ["removeInstance", () => client.removeInstance({ instanceName: "i" })],
    ["updateInstanceConfig", () => client.updateInstanceConfig({ instanceName: "i" })],
    ["getBulkJob", () => client.getBulkJob({ jobId: "j" })],
    ["getBulkJobs", () => client.getBulkJobs({ jobIds: ["j"] })],
    ["cancelBulkJob", () => client.cancelBulkJob({ jobId: "j" })],
    ["sendPlainText", () => client.sendPlainText({ instanceName: "i", to: "34600", text: "hi" })],
    [
      "sendBulkSameMessage",
      () => client.sendBulkSameMessage({ instanceName: "i", recipients: ["34600"], text: "hi" }),
    ],
    [
      "sendBulkCustomMessages",
      () =>
        client.sendBulkCustomMessages({
          instanceName: "i",
          messages: [{ to: "34600", text: "hi" }],
        }),
    ],
  ];
}

// A new RPC in a .proto file must get a method in CoreClient.
test("core: every RPC of the .proto files has a method in CoreClient", async () => {
  const dir = new URL("../../../shared-types/proto/calyx/core/v1/", import.meta.url);
  const names: string[] = [];
  for (const file of readdirSync(dir).filter((f) => f.endsWith(".proto"))) {
    const text = readFileSync(new URL(file, dir), "utf8");
    for (const m of text.matchAll(/^\s*rpc\s+(\w+)\s*\(/gm)) {
      names.push(m[1]!.charAt(0).toLowerCase() + m[1]!.slice(1));
    }
  }
  assert.equal(names.length, 12);
  await withCore(
    () => ({}),
    async (_core, client) => {
      for (const name of names) {
        assert.equal(typeof (client as unknown as Record<string, unknown>)[name], "function", name);
      }
    },
  );
});

test("core case 1: every unary call sends the token as metadata", async () => {
  await withCore(
    () => ({}),
    async (core, client) => {
      const calls = unaryCalls(client);
      for (const [, call] of calls) await call();
      assert.deepEqual(
        core.calls.map((c) => c.method),
        calls.map(([name]) => name),
      );
      for (const c of core.calls)
        assert.equal(c.metadata.authorization, `Bearer ${token}`, c.method);
    },
  );
});

test("core: a unary call returns the answer of the core", async () => {
  const value = { status: InstanceStatus.INSTANCE_STATUS_CONNECTED };
  await withCore(
    () => ({ value }),
    async (core, client) => {
      const data = await client.getInstanceStatus({ instanceName: "i" });
      assert.deepEqual(data, value);
      assert.deepEqual(core.calls[0]!.request, { instanceName: "i" });
    },
  );
});

test("core case 2: an ErrorInfo of calyx.core.v1 gives its reason, metadata and retry", async () => {
  const reply: Reply = {
    error: {
      code: status.ABORTED,
      details: "The outcome of the first send is unknown.",
      info: {
        reason: "ERROR_REASON_SEND_OUTCOME_UNKNOWN",
        domain: "calyx.core.v1",
        metadata: { at: "2026-11-20T10:00:00Z" },
      },
    },
  };
  await withCore(
    () => reply,
    async (_core, client) => {
      const err = await caught(client.getBulkJob({ jobId: "j" }));
      assert.equal(err.source, "core");
      assert.equal(err.code, "ERROR_REASON_SEND_OUTCOME_UNKNOWN");
      assert.equal(err.status, status.ABORTED);
      assert.equal(err.detail, "The outcome of the first send is unknown.");
      assert.equal(err.retry, "check_if_sent");
      assert.deepEqual(err.metadata, { at: "2026-11-20T10:00:00Z" });
    },
  );
});

test("core case 3: an ErrorInfo of another domain gives the status code name", async () => {
  const reply: Reply = {
    error: {
      code: status.FAILED_PRECONDITION,
      details: "Not ready.",
      info: {
        reason: "ERROR_REASON_DUPLICATE_MESSAGE",
        domain: "example.com",
        metadata: { at: "x" },
      },
    },
  };
  await withCore(
    () => reply,
    async (_core, client) => {
      const err = await caught(client.getBulkJob({ jobId: "j" }));
      assert.equal(err.source, "core");
      assert.equal(err.code, "FAILED_PRECONDITION");
      assert.equal(err.status, status.FAILED_PRECONDITION);
      assert.equal(err.retry, "do_not_retry");
      assert.equal(err.metadata, undefined);
    },
  );
});

test("core: a status without details gives the status code name", async () => {
  await withCore(
    () => ({ error: { code: status.NOT_FOUND, details: "No such instance." } }),
    async (_core, client) => {
      const err = await caught(client.getInstanceStatus({ instanceName: "i" }));
      assert.equal(err.code, "NOT_FOUND");
      assert.equal(err.detail, "No such instance.");
      assert.equal(err.retry, "do_not_retry");
    },
  );
});

test("core: the default deadlines are 6 minutes for sendPlainText and 2 minutes for the other calls", async () => {
  await withCore(
    () => ({}),
    async (core, client) => {
      const start = Date.now();
      for (const [, call] of unaryCalls(client)) await call();
      const end = Date.now();
      for (const c of core.calls) {
        const limit = c.method === "sendPlainText" ? 6 * 60_000 : 2 * 60_000;
        // The server sees the deadline with a small loss of precision.
        assert.ok(
          c.deadline >= start + limit - 1_000 && c.deadline <= end + limit + 1_000,
          c.method,
        );
      }
    },
  );
});

test("core: the timeoutMs of the call has priority over the timeoutMs of the client", async () => {
  await withCore(
    () => ({}),
    async (core, client) => {
      const start = Date.now();
      await client.getBulkJob({ jobId: "j" });
      await client.getBulkJob({ jobId: "j" }, { timeoutMs: 30_000 });
      const end = Date.now();
      const [fromClient, fromCall] = core.calls;
      assert.ok(
        fromClient!.deadline >= start + 10_000 - 1_000 &&
          fromClient!.deadline <= end + 10_000 + 1_000,
      );
      assert.ok(
        fromCall!.deadline >= start + 30_000 - 1_000 && fromCall!.deadline <= end + 30_000 + 1_000,
      );
    },
    { timeoutMs: 10_000 },
  );
});

test("core case 5: a call that gets no answer before timeoutMs gives DEADLINE_EXCEEDED", async () => {
  await withCore(
    () => ({ hang: true }),
    async (_core, client) => {
      const err = await caught(client.getBulkJob({ jobId: "j" }, { timeoutMs: 100 }));
      assert.equal(err.source, "core");
      assert.equal(err.code, "DEADLINE_EXCEEDED");
      assert.equal(err.status, status.DEADLINE_EXCEEDED);
      assert.equal(err.retry, "retry_now");
    },
  );
});

test("core: the AbortSignal of the program cancels the call and gives aborted", async () => {
  await withCore(
    () => ({ hang: true }),
    async (_core, client) => {
      const err = await caught(
        client.getBulkJob({ jobId: "j" }, { signal: AbortSignal.timeout(100) }),
      );
      assert.equal(err.source, "client");
      assert.equal(err.code, "aborted");
      const already = await caught(
        client.getBulkJob({ jobId: "j" }, { signal: AbortSignal.abort() }),
      );
      assert.equal(already.code, "aborted");
    },
  );
});

test("core case 6: tls true against a core without TLS gives UNAVAILABLE", async () => {
  await withCore(
    () => ({}),
    async (core, client) => {
      const err = await caught(client.listInstances());
      assert.equal(err.source, "core");
      assert.equal(err.code, "UNAVAILABLE");
      assert.equal(err.retry, "retry_now");
      assert.equal(core.calls.length, 0);
    },
    { tls: true },
  );
});

test("core: tls with given contents also uses TLS", async () => {
  await withCore(
    () => ({}),
    async (core, client) => {
      const err = await caught(client.listInstances());
      assert.equal(err.code, "UNAVAILABLE");
      assert.equal(core.calls.length, 0);
    },
    { tls: {} },
  );
});

test("core: tls with an IP address gives invalid_config at once", () => {
  const addresses = [
    "127.0.0.1:50051",
    "[::1]:50051",
    "::1",
    "dns:///10.0.0.5:50051",
    "ipv4:10.0.0.5:50051",
  ];
  for (const address of addresses) {
    for (const tls of [true, {}] as const) {
      assert.throws(
        () => createCoreClient({ address, token: "t", tls }),
        (err) =>
          isCalyxError(err, "invalid_config") &&
          err.source === "client" &&
          err.retry === "do_not_retry",
        address,
      );
    }
  }
});

test("core: tls with a host name, or no tls with an IP address, is accepted", () => {
  for (const options of [
    { address: "core:50051", tls: true },
    { address: "dns:///core.example.com:50051", tls: true },
    { address: "127.0.0.1:50051", tls: false },
  ] as const) {
    createCoreClient({ ...options, token: "t" }).close();
  }
});

test("core case 7: a call after close() rejects", async () => {
  await withCore(
    () => ({}),
    async (core, client) => {
      await client.listInstances();
      client.close();
      const err = await caught(client.listInstances());
      assert.equal(err.source, "client");
      assert.equal(err.code, "aborted");
      assert.equal(core.calls.length, 1);
      const events = await drain(client.subscribe());
      assert.equal(events.error?.code, "aborted");
    },
  );
});

// The three sends, with the idempotency key that the program gives, if any.
function sends(client: CoreClient, idempotencyKey?: string) {
  const options = idempotencyKey === undefined ? undefined : { idempotencyKey };
  return [
    [
      "sendPlainText",
      () => client.sendPlainText({ instanceName: "i", to: "34600", text: "hi" }, options),
    ],
    [
      "sendBulkSameMessage",
      () =>
        client.sendBulkSameMessage(
          { instanceName: "i", recipients: ["34600"], text: "hi" },
          options,
        ),
    ],
    [
      "sendBulkCustomMessages",
      () =>
        client.sendBulkCustomMessages(
          { instanceName: "i", messages: [{ to: "34600", text: "hi" }] },
          options,
        ),
    ],
  ] as const;
}

test("core case 4: each send makes an idempotency key, sends it, and returns it in the result", async () => {
  await withCore(
    () => ({}),
    async (core, client) => {
      const keys: string[] = [];
      for (const [name, send] of sends(client)) {
        const result = await send();
        assert.match(result.idempotencyKey, /^[0-9a-f-]{36}$/, name);
        assert.ok(result.data !== undefined, name);
        const request = core.calls.at(-1)!.request as { idempotencyKey?: string };
        assert.equal(request.idempotencyKey, result.idempotencyKey, name);
        keys.push(result.idempotencyKey);
      }
      assert.equal(new Set(keys).size, 3);
    },
  );
});

test("core case 4: each send returns its idempotency key in an error", async () => {
  const reply: Reply = {
    error: {
      code: status.ALREADY_EXISTS,
      details: "The same text went to the same recipient.",
      info: { reason: "ERROR_REASON_DUPLICATE_MESSAGE", domain: "calyx.core.v1" },
    },
  };
  await withCore(
    () => reply,
    async (core, client) => {
      for (const [name, send] of sends(client)) {
        const err = await caught(send());
        assert.equal(err.code, "ERROR_REASON_DUPLICATE_MESSAGE", name);
        const request = core.calls.at(-1)!.request as { idempotencyKey?: string };
        assert.ok(request.idempotencyKey, name);
        assert.equal(err.idempotencyKey, request.idempotencyKey, name);
      }
    },
  );
});

test("core: each send uses the idempotency key of the program, also in a client error", async () => {
  await withCore(
    () => ({}),
    async (core, client) => {
      for (const [name, send] of sends(client, "mine")) {
        const result = await send();
        assert.equal(result.idempotencyKey, "mine", name);
        assert.equal(
          (core.calls.at(-1)!.request as { idempotencyKey?: string }).idempotencyKey,
          "mine",
          name,
        );
      }
      client.close();
      for (const [name, send] of sends(client, "mine")) {
        const err = await caught(send());
        assert.equal(err.idempotencyKey, "mine", name);
      }
    },
  );
});

test("core: a send that gets no answer in time keeps its idempotency key", async () => {
  await withCore(
    () => ({ hang: true }),
    async (_core, client) => {
      const err = await caught(
        client.sendPlainText(
          { instanceName: "i", to: "34600", text: "hi" },
          { idempotencyKey: "k1", timeoutMs: 100 },
        ),
      );
      assert.equal(err.code, "DEADLINE_EXCEEDED");
      assert.equal(err.idempotencyKey, "k1");
      const aborted = await caught(
        client.sendPlainText(
          { instanceName: "i", to: "34600", text: "hi" },
          { idempotencyKey: "k2", signal: AbortSignal.abort() },
        ),
      );
      assert.equal(aborted.code, "aborted");
      assert.equal(aborted.idempotencyKey, "k2");
    },
  );
});

// Test-only TLS files (test/fixtures/tls/README.md). They protect nothing.
const tlsFile = (name: string) =>
  readFileSync(new URL(`./fixtures/tls/test-only-${name}.pem`, import.meta.url));
const ca = tlsFile("ca-cert");
const serverCert = { cert_chain: tlsFile("server-cert"), private_key: tlsFile("server-key") };

test("core: tls with a given ca reaches a core with TLS", async () => {
  await withCore(
    () => ({}),
    async (core, client) => {
      await client.listInstances();
      assert.equal(core.calls.length, 1);
      assert.equal(core.calls[0]!.metadata.authorization, `Bearer ${token}`);
    },
    { tls: { ca: new Uint8Array(ca) }, timeoutMs: 5_000 },
    ServerCredentials.createSsl(null, [serverCert]),
  );
});

test("core: tls with another ca gives UNAVAILABLE", async () => {
  await withCore(
    () => ({}),
    async (core, client) => {
      const err = await caught(client.listInstances());
      assert.equal(err.source, "core");
      assert.equal(err.code, "UNAVAILABLE");
      assert.equal(core.calls.length, 0);
    },
    { tls: { ca: tlsFile("wrong-ca-cert") }, timeoutMs: 5_000 },
    ServerCredentials.createSsl(null, [serverCert]),
  );
});

test("core: tls with a client cert and key passes a core that requires them", async () => {
  // The core checks the client certificate against the test CA.
  const mutual = () => ServerCredentials.createSsl(ca, [serverCert], true);
  const cert = tlsFile("client-cert");
  const key = tlsFile("client-key");
  await withCore(
    () => ({}),
    async (core, client) => {
      await client.listInstances();
      assert.equal(core.calls.length, 1);
    },
    { tls: { ca, cert, key }, timeoutMs: 5_000 },
    mutual(),
  );
  await withCore(
    () => ({}),
    async (core, client) => {
      const err = await caught(client.listInstances());
      assert.equal(err.code, "UNAVAILABLE");
      assert.equal(core.calls.length, 0);
    },
    { tls: { ca }, timeoutMs: 5_000 },
    mutual(),
  );
});

// Reads an iterator to its end. It gives the items, and the error that ended it, if any.
async function drain<T>(
  items: AsyncIterable<T>,
  stopAfter = Infinity,
): Promise<{ items: T[]; error?: CalyxError }> {
  const got: T[] = [];
  try {
    for await (const item of items) {
      got.push(item);
      if (got.length >= stopAfter) break;
    }
  } catch (err) {
    assert.ok(isCalyxError(err), `not a CalyxError: ${String(err)}`);
    return { items: got, error: err };
  }
  return { items: got };
}

const needsAuth = CreateInstanceResponse.fromPartial({
  status: InstanceStatus.INSTANCE_STATUS_NEEDS_AUTH,
  pairingCode: "ABCD1234",
  message: "Scan the code.",
});

function coreEvent(id: string, payloadJson = '{"status":"CONNECTED"}') {
  return {
    id,
    instanceName: "i_1",
    type: "connection.update",
    payloadJson,
    occurredAt: new Date("2026-11-20T10:00:00.000Z"),
  };
}

test("core case 8: a break in createInstance cancels the stream on the server", {
  timeout: 5_000,
}, async () => {
  let cancelled: Promise<void> | undefined;
  const reply: Reply = {
    stream: (s) => {
      cancelled = s.cancelled;
      s.write(needsAuth);
      s.write(needsAuth);
    },
  };
  await withCore(
    () => reply,
    async (core, client) => {
      const { items, error } = await drain(
        client.createInstance({ instanceName: "i_1", webhooks: [] }),
        1,
      );
      assert.equal(error, undefined);
      assert.equal(items.length, 1);
      assert.equal(items[0]!.pairingCode, "ABCD1234");
      await cancelled;
      assert.equal(core.calls.length, 1);
      assert.equal(core.calls[0]!.method, "createInstance");
      assert.equal(core.calls[0]!.metadata.authorization, `Bearer ${token}`);
      // Streams have no deadline.
      assert.equal(core.calls[0]!.deadline, Infinity);
    },
  );
});

test("core: createInstance that the server ends with status OK ends the iterator normally", async () => {
  const connected = CreateInstanceResponse.fromPartial({
    status: InstanceStatus.INSTANCE_STATUS_CONNECTED,
    message: "Paired.",
  });
  const reply: Reply = {
    stream: (s) => {
      s.write(needsAuth);
      s.write(connected);
      s.end();
    },
  };
  await withCore(
    () => reply,
    async (_core, client) => {
      const { items, error } = await drain(
        client.createInstance({ instanceName: "i_1", webhooks: [] }),
      );
      assert.equal(error, undefined);
      assert.deepEqual(
        items.map((i) => i.status),
        [InstanceStatus.INSTANCE_STATUS_NEEDS_AUTH, InstanceStatus.INSTANCE_STATUS_CONNECTED],
      );
    },
  );
});

test("core: the AbortSignal cancels createInstance on the server and gives aborted", {
  timeout: 5_000,
}, async () => {
  let cancelled: Promise<void> | undefined;
  const reply: Reply = {
    stream: (s) => {
      cancelled = s.cancelled;
      s.write(needsAuth);
    },
  };
  await withCore(
    () => reply,
    async (_core, client) => {
      const control = new AbortController();
      const reason = new Error("stop");
      const items = client.createInstance(
        { instanceName: "i_1", webhooks: [] },
        { signal: control.signal },
      );
      const got: unknown[] = [];
      const err = await caught(
        (async () => {
          for await (const item of items) {
            got.push(item);
            control.abort(reason);
          }
        })(),
      );
      assert.equal(got.length, 1);
      assert.equal(err.source, "client");
      assert.equal(err.code, "aborted");
      assert.equal(err.cause, reason);
      await cancelled;
      const already = await drain(
        client.createInstance(
          { instanceName: "i_1", webhooks: [] },
          { signal: AbortSignal.abort() },
        ),
      );
      assert.equal(already.error?.code, "aborted");
    },
  );
});

test("core: the AbortSignal also cancels subscribe", { timeout: 5_000 }, async () => {
  let cancelled: Promise<void> | undefined;
  const reply: Reply = {
    stream: (s) => {
      cancelled = s.cancelled;
    },
  };
  await withCore(
    () => reply,
    async (_core, client) => {
      const signal = AbortSignal.timeout(100);
      const { error } = await drain(
        client.subscribe({ instanceNames: [], eventTypes: [] }, { signal }),
      );
      assert.equal(error?.source, "client");
      assert.equal(error?.code, "aborted");
      assert.equal(error?.cause, signal.reason);
      await cancelled;
    },
  );
});

test("core case 9: a subscribe stream that the server ends with UNAVAILABLE gives UNAVAILABLE", async () => {
  const reply: Reply = {
    stream: (s) => {
      s.write(coreEvent("e1"));
      s.end({ code: status.UNAVAILABLE, details: "The core stops." });
    },
  };
  await withCore(
    () => reply,
    async (core, client) => {
      const { items, error } = await drain(
        client.subscribe({ instanceNames: ["i_1"], eventTypes: [] }),
      );
      assert.equal(items.length, 1);
      assert.equal(error?.source, "core");
      assert.equal(error?.code, "UNAVAILABLE");
      assert.equal(error?.status, status.UNAVAILABLE);
      assert.deepEqual(core.calls[0]!.request, { instanceNames: ["i_1"], eventTypes: [] });
      assert.equal(core.calls[0]!.metadata.authorization, `Bearer ${token}`);
      assert.equal(core.calls[0]!.deadline, Infinity);
    },
  );
});

test("core: the keepalive is every 2 minutes, with 20 seconds for the answer, only while a call runs", () => {
  assert.deepEqual(keepalive, {
    "grpc.keepalive_time_ms": 120_000,
    "grpc.keepalive_timeout_ms": 20_000,
    "grpc.keepalive_permit_without_calls": 0,
  });
  // The core closes a connection that pings more often than once a minute (keepalivePolicy in
  // apps/core/internal/grpcserver).
  const coreMinTimeMs = 60_000;
  assert.ok(keepalive["grpc.keepalive_time_ms"] > coreMinTimeMs);
});

// Runs with short keepalive times and a proxy in front of the fake core.
async function withProxy(
  reply: (method: string, proxy: Proxy) => Reply,
  run: (proxy: Proxy, client: CoreClient, core: FakeCore) => Promise<void>,
) {
  const values = { ...keepalive };
  let proxy: Proxy | undefined;
  const core = await startFakeCore((method) => reply(method, proxy!));
  let client: CoreClient | undefined;
  try {
    Object.assign(keepalive, {
      "grpc.keepalive_time_ms": 200,
      "grpc.keepalive_timeout_ms": 200,
    });
    proxy = await startProxy(core.address);
    client = createCoreClient({ address: proxy.address, token, tls: false });
    await run(proxy, client, core);
  } finally {
    Object.assign(keepalive, values);
    client?.close();
    proxy?.close();
    core.close();
  }
}

test("core: a stream of a core host that disappears gives UNAVAILABLE, and the next call works", {
  timeout: 5_000,
}, async () => {
  const reply = (method: string): Reply =>
    method === "subscribe" ? { stream: (s) => s.write(coreEvent("e1")) } : {};
  await withProxy(reply, async (proxy, client, core) => {
    let items = 0;
    const error = await caught(
      (async () => {
        // Without the keepalive, the stream would never end.
        const signal = AbortSignal.timeout(4_000);
        for await (const _ of client.subscribe({}, { signal })) {
          items += 1;
          // The pings keep a stream without events open.
          await new Promise((resolve) => setTimeout(resolve, 1_000));
          proxy.cut();
        }
      })(),
    );
    assert.equal(items, 1);
    assert.equal(error.source, "core");
    assert.equal(error.code, "UNAVAILABLE");
    assert.equal(error.status, status.UNAVAILABLE);
    assert.equal(error.retry, "retry_now");
    assert.equal(proxy.connections(), 1);

    await client.listInstances();
    assert.equal(proxy.connections(), 2);
    assert.deepEqual(
      core.calls.map((c) => c.method),
      ["subscribe", "listInstances"],
    );
  });
});

test("core: a send to a core host that disappears gives UNAVAILABLE with its key", {
  timeout: 5_000,
}, async () => {
  const reply = (_method: string, proxy: Proxy): Reply => {
    proxy.cut();
    return { hang: true };
  };
  await withProxy(reply, async (_proxy, client, core) => {
    const error = await caught(
      client.sendPlainText(
        { instanceName: "i", to: "34600", text: "hi" },
        { idempotencyKey: "order-1", timeoutMs: 4_000 },
      ),
    );
    assert.equal(error.code, "UNAVAILABLE");
    assert.equal(error.retry, "retry_now");
    assert.equal(error.idempotencyKey, "order-1");
    assert.equal(core.calls.length, 1);
  });
});

test("core case 10: a subscribe stream that the server ends with status OK gives stream_failed", async () => {
  await withCore(
    () => ({ stream: (s) => s.end() }),
    async (_core, client) => {
      const { items, error } = await drain(client.subscribe());
      assert.equal(items.length, 0);
      assert.equal(error?.source, "client");
      assert.equal(error?.code, "stream_failed");
    },
  );
});

test("core case 11: subscribe gives CalyxEvent items, and an invalid payload_json gives invalid_event", {
  timeout: 5_000,
}, async () => {
  let cancelled: Promise<void> | undefined;
  const reply: Reply = {
    stream: (s) => {
      cancelled = s.cancelled;
      s.write(coreEvent("e1"));
      s.write(coreEvent("e2", "{not json"));
      s.write(coreEvent("e3"));
    },
  };
  await withCore(
    () => reply,
    async (_core, client) => {
      const { items, error } = await drain(
        client.subscribe({ instanceNames: ["i_1"], eventTypes: ["connection.update"] }),
      );
      assert.deepEqual(items, [
        {
          id: "e1",
          event: "connection.update",
          instance: "i_1",
          data: { status: "CONNECTED" },
          timestamp: "2026-11-20T10:00:00.000Z",
        },
      ]);
      assert.equal(error?.code, "invalid_event");
      // The iterator ended, so the SDK cancels the stream.
      await cancelled;
    },
  );
});

// Aborts after the first item, when the next items already wait in the client buffer.
async function abortAfterFirst<T>(open: (signal: AbortSignal) => AsyncIterable<T>) {
  const control = new AbortController();
  const reason = new Error("stop");
  const got: T[] = [];
  const err = await caught(
    (async () => {
      for await (const item of open(control.signal)) {
        got.push(item);
        await new Promise((resolve) => setTimeout(resolve, 100));
        control.abort(reason);
      }
    })(),
  );
  return { got, err, reason };
}

test("core: no createInstance item arrives after an abort", { timeout: 5_000 }, async () => {
  const reply: Reply = {
    stream: (s) => {
      for (let n = 0; n < 5; n++) s.write(needsAuth);
    },
  };
  await withCore(
    () => reply,
    async (_core, client) => {
      const { got, err, reason } = await abortAfterFirst((signal) =>
        client.createInstance({ instanceName: "i_1", webhooks: [] }, { signal }),
      );
      assert.equal(got.length, 1);
      assert.equal(err.code, "aborted");
      assert.equal(err.cause, reason);
    },
  );
});

test("core: no subscribe event arrives after an abort, also after an end with status OK", {
  timeout: 5_000,
}, async () => {
  const reply: Reply = {
    stream: (s) => {
      for (let n = 0; n < 5; n++) s.write(coreEvent(`e${n}`));
      s.end();
    },
  };
  await withCore(
    () => reply,
    async (_core, client) => {
      const { got, err, reason } = await abortAfterFirst((signal) =>
        client.subscribe({}, { signal }),
      );
      assert.equal(got.length, 1);
      // The abort has priority over stream_failed.
      assert.equal(err.code, "aborted");
      assert.equal(err.cause, reason);
    },
  );
});

test("core: an abort after a stream that ended with status OK gives aborted", {
  timeout: 5_000,
}, async () => {
  const reply: Reply = {
    stream: (s) => {
      s.write(coreEvent("e0"));
      s.end();
    },
  };
  await withCore(
    () => reply,
    async (_core, client) => {
      const { got, err } = await abortAfterFirst((signal) => client.subscribe({}, { signal }));
      assert.equal(got.length, 1);
      assert.equal(err.code, "aborted");
    },
  );
});

test("core: subscribe with only instanceNames sends an empty eventTypes", async () => {
  await withCore(
    () => ({ stream: (s) => s.end() }),
    async (core, client) => {
      const { error } = await drain(client.subscribe({ instanceNames: ["i_1"] }));
      assert.equal(error?.code, "stream_failed");
      assert.deepEqual(core.calls[0]!.request, { instanceNames: ["i_1"], eventTypes: [] });
    },
  );
});

test("core error cases", () => {
  const file = JSON.parse(
    readFileSync(new URL("../../testdata/core-errors.json", import.meta.url), "utf8"),
  ) as {
    cases: {
      name: string;
      grpcCode: number;
      statusBase64: string;
      expected: { code: string; retry: string; retryAfterSeconds: number };
    }[];
  };
  assert.ok(file.cases.length > 0);
  for (const c of file.cases) {
    const metadata = new Metadata();
    metadata.set("grpc-status-details-bin", Buffer.from(c.statusBase64, "base64"));
    const service = Object.assign(new Error("x"), {
      code: c.grpcCode,
      details: "x",
      metadata,
    }) as ServiceError;
    const err = coreError(service);
    assert.equal(err.code, c.expected.code, c.name);
    assert.equal(err.retry, c.expected.retry, c.name);
    assert.equal(err.retryAfter ?? 0, c.expected.retryAfterSeconds, c.name);
  }
});
