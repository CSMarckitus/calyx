import assert from "node:assert/strict";
import { test } from "node:test";

import { setSizeLimitsForTest } from "../src/backend/http.js";
import { createBackendClient } from "../src/backend/index.js";
import { operations } from "../src/backend/operations.js";
import { FrameParser, openPairingStream } from "../src/backend/sse.js";
import { CalyxError, isCalyxError } from "../src/errors/index.js";
import { problem, type Reply, startFakeBackend } from "./fake-backend.js";

const update =
  'event: update\ndata: {"message":"scan","pairing_code":null,"qr_text":"QR1","status":"NEEDS_AUTH"}\n\n';
const done = 'event: done\ndata: {"status":"CONNECTED"}\n\n';
const failed = 'event: failed\ndata: {"code":"pairing_expired","detail":"gone"}\n\n';

async function withStream(
  reply: Reply,
  run: (
    open: (idleTimeoutMs: number, signal?: AbortSignal) => AsyncIterable<unknown>,
    b: Awaited<ReturnType<typeof startFakeBackend>>,
  ) => Promise<void>,
) {
  const b = await startFakeBackend(() => reply);
  try {
    await run(
      (idleTimeoutMs, signal) =>
        openPairingStream(
          {
            config: { baseUrl: b.baseUrl, apiKey: "k" },
            row: operations.followPairing,
            params: { id: "i 1" },
            ...(signal === undefined ? {} : { signal }),
          },
          { idleTimeoutMs },
        ),
      b,
    );
  } finally {
    await b.close();
  }
}

async function collect(it: AsyncIterable<unknown>): Promise<unknown[]> {
  const out: unknown[] = [];
  for await (const x of it) out.push(x);
  return out;
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

test("pings and comments give no item", async () => {
  const frames = [": ping\n\n", ": hello\n", ": other\n\n", update, ": ping\n\n", done];
  await withStream({ stream: { frames } }, async (open) => {
    const items = (await collect(open(1000))) as { event: string }[];
    assert.deepEqual(
      items.map((i) => i.event),
      ["update", "done"],
    );
  });
});

test("update, then done, ends the iterator", async () => {
  await withStream({ stream: { frames: [update, done] } }, async (open, b) => {
    const items = await collect(open(1000));
    assert.deepEqual(items, [
      {
        event: "update",
        data: { message: "scan", pairing_code: null, qr_text: "QR1", status: "NEEDS_AUTH" },
      },
      { event: "done", data: { status: "CONNECTED" } },
    ]);
    const req = b.requests[0]!;
    assert.equal(req.method, "GET");
    assert.equal(req.url, "/api/v1/instances/i%201/pairing");
    assert.equal(req.headers.authorization, "Bearer k");
    assert.equal(req.headers.accept, "text/event-stream");
  });
});

test("a failed event ends the iterator", async () => {
  await withStream({ stream: { frames: [failed], keepOpen: true } }, async (open) => {
    const items = await collect(open(1000));
    assert.deepEqual(items, [
      { event: "failed", data: { code: "pairing_expired", detail: "gone" } },
    ]);
  });
});

test("a failed item as the first item ends the iterator", async () => {
  const first = 'event: failed\ndata: {"code":"pairing_expired","detail":"The codes ended."}\n\n';
  await withStream({ stream: { frames: [first] } }, async (open) => {
    const items = await collect(open(1000));
    assert.deepEqual(items, [
      { event: "failed", data: { code: "pairing_expired", detail: "The codes ended." } },
    ]);
  });
});

test("the parser handles frames split in bytes, CRLF and multi-byte text", async () => {
  const text =
    'event: update\r\ndata: {"message":"ñ€","pairing_code":null,\r\ndata: "qr_text":null,"status":"CONNECTING"}\r\n\r\n';
  const bytes = Buffer.from(text + done);
  // One byte at a time, so a character can split between two chunks.
  const frames = Array.from(bytes, (_, i) => bytes.subarray(i, i + 1));
  await withStream({ stream: { frames } }, async (open) => {
    const items = (await collect(open(1000))) as { event: string; data: { message?: string } }[];
    assert.equal(items.length, 2);
    assert.equal(items[0]!.data.message, "ñ€");
  });
});

test("an unknown event is ignored, and a field without a space after the colon works", async () => {
  const frames = [
    "event: later\ndata: {}\n\n",
    "event:update\ndata:" +
      '{"message":"m","pairing_code":null,"qr_text":null,"status":"CONNECTING"}' +
      "\n\n",
    done,
  ];
  await withStream({ stream: { frames } }, async (open) => {
    const items = (await collect(open(1000))) as { event: string }[];
    assert.deepEqual(
      items.map((i) => i.event),
      ["update", "done"],
    );
  });
});

test("data that is not JSON gives invalid_response", async () => {
  await withStream({ stream: { frames: ["event: update\ndata: nope\n\n"] } }, async (open) => {
    const err = await caught(collect(open(1000)));
    assert.equal(err.code, "invalid_response");
    assert.equal(err.source, "client");
  });
});

test("a stream that ends before done or failed gives stream_failed", async () => {
  await withStream({ stream: { frames: [update] } }, async (open) => {
    const seen: unknown[] = [];
    const err = await caught(
      (async () => {
        for await (const x of open(1000)) seen.push(x);
      })(),
    );
    assert.equal(seen.length, 1);
    assert.equal(err.code, "stream_failed");
    assert.equal(err.source, "client");
    assert.equal(err.status, 0);
  });
});

test("a silent stream gives stream_failed after idleTimeoutMs", async () => {
  await withStream({ stream: { frames: [update], keepOpen: true } }, async (open) => {
    const started = Date.now();
    const err = await caught(collect(open(150)));
    assert.equal(err.code, "stream_failed");
    assert.ok(Date.now() - started >= 140);
  });
});

test("a ping restarts the idle time", async () => {
  // Six pings 80 ms apart last longer than the 200 ms idle time, so each one must restart it.
  const frames = [...Array(6).fill(": ping\n\n"), done];
  await withStream({ stream: { frames, gapMs: 80 } }, async (open) => {
    const items = await collect(open(200));
    assert.equal(items.length, 1);
  });
});

test("a 404 on the first call gives not_found", async () => {
  await withStream(problem(404, "not_found"), async (open) => {
    const err = await caught(collect(open(1000)));
    assert.equal(err.source, "backend");
    assert.equal(err.code, "not_found");
    assert.equal(err.status, 404);
    assert.equal(err.requestId, "req-1");
  });
});

test("a break closes the connection", { timeout: 5000 }, async () => {
  let closed!: () => void;
  const serverClosed = new Promise<void>((r) => (closed = r));
  await withStream(
    { stream: { frames: [update], keepOpen: true, onClose: closed } },
    async (open) => {
      for await (const item of open(5000)) {
        assert.equal((item as { event: string }).event, "update");
        break;
      }
      await serverClosed;
    },
  );
});

test("an AbortSignal closes the connection and gives aborted", { timeout: 5000 }, async () => {
  let closed!: () => void;
  const serverClosed = new Promise<void>((r) => (closed = r));
  const ctl = new AbortController();
  await withStream(
    { stream: { frames: [update], keepOpen: true, onClose: closed } },
    async (open) => {
      const err = await caught(
        (async () => {
          for await (const _ of open(5000, ctl.signal)) ctl.abort();
        })(),
      );
      assert.equal(err.code, "aborted");
      await serverClosed;
    },
  );
});

test("a signal that already aborted gives aborted and opens nothing", async () => {
  await withStream({ stream: { frames: [done] } }, async (open, b) => {
    const err = await caught(collect(open(1000, AbortSignal.abort())));
    assert.equal(err.code, "aborted");
    assert.equal(b.requests.length, 0);
  });
});

test("a refused connection gives network_error", async () => {
  const b = await startFakeBackend(() => ({}));
  const baseUrl = b.baseUrl;
  await b.close();
  const err = await caught(
    collect(
      openPairingStream(
        { config: { baseUrl }, row: operations.followPairing, params: { id: "i" } },
        { idleTimeoutMs: 1000 },
      ),
    ),
  );
  assert.equal(err.code, "network_error");
});

test("followPairing on the client has no time limit and ends after done", async () => {
  const b = await startFakeBackend(() => ({ stream: { frames: [update, done] } }));
  try {
    const client = createBackendClient({ baseUrl: b.baseUrl, apiKey: "k", timeoutMs: 1 });
    const items = await collect(client.followPairing({ id: "i" }));
    assert.equal(items.length, 2);
    assert.equal(b.requests.length, 1);
  } finally {
    await b.close();
  }
});

test("a slow loop body does not count as idle time", async () => {
  const frames = [update, ": ping\n\n", ": ping\n\n", ": ping\n\n", done];
  await withStream({ stream: { frames, gapMs: 50 } }, async (open) => {
    const events: string[] = [];
    for await (const item of open(200)) {
      events.push((item as { event: string }).event);
      await new Promise((r) => setTimeout(r, 300));
    }
    assert.deepEqual(events, ["update", "done"]);
  });
});

test("a 200 answer that is not an event stream gives invalid_response", async () => {
  const reply: Reply = { headers: { "content-type": "text/html" }, body: "<html>proxy</html>" };
  await withStream(reply, async (open) => {
    const err = await caught(collect(open(1000)));
    assert.equal(err.code, "invalid_response");
    assert.equal(err.source, "client");
  });
});

test("an idle timeout while the error body is read gives stream_failed", {
  timeout: 5000,
}, async () => {
  await withStream({ status: 500, stream: { frames: ["{"], keepOpen: true } }, async (open) => {
    const err = await caught(collect(open(150)));
    assert.equal(err.code, "stream_failed");
  });
});

test("an abort while the error body is read gives aborted", { timeout: 5000 }, async () => {
  const ctl = new AbortController();
  setTimeout(() => ctl.abort(), 100);
  await withStream({ status: 500, stream: { frames: ["{"], keepOpen: true } }, async (open) => {
    const err = await caught(collect(open(5000, ctl.signal)));
    assert.equal(err.code, "aborted");
  });
});

test("an empty chunk after a CR does not make the next LF an extra line end", () => {
  const parser = new FrameParser();
  assert.deepEqual(parser.push("event: update\rdata: a\r"), []);
  assert.deepEqual(parser.push(""), []);
  // The LF completes the CRLF. It is not an empty line, so the frame must not end here.
  assert.deepEqual(parser.push("\n"), []);
  assert.deepEqual(parser.push("\r\n"), [{ event: "update", data: "a" }]);
});

test("an event over the size limit gives invalid_response and closes the stream", {
  timeout: 5000,
}, async () => {
  setSizeLimitsForTest(1024, 1024);
  try {
    let closed!: () => void;
    const serverClosed = new Promise<void>((r) => (closed = r));
    // Many short lines: no line is long, but the event passes 1 KiB.
    const event = `event: update\n${"data: 1\n".repeat(200)}\n`;
    await withStream(
      { stream: { frames: [event], keepOpen: true, onClose: closed } },
      async (open) => {
        const err = await caught(collect(open(5000)));
        assert.equal(err.code, "invalid_response");
        await serverClosed;
      },
    );
    // Each event is under the limit and the whole stream is over it: an empty line resets the count.
    const pings = ": ping\n\n".repeat(200);
    await withStream({ stream: { frames: [pings, done] } }, async (open) => {
      assert.equal((await collect(open(1000))).length, 1);
    });
    // Comment lines that never end an event count too.
    await withStream({ stream: { frames: [": ping\n".repeat(200)] } }, async (open) => {
      assert.equal((await caught(collect(open(1000)))).code, "invalid_response");
    });
  } finally {
    setSizeLimitsForTest(32 * 1024 * 1024, 1024 * 1024);
  }
});

test("the parser accepts an event of exactly the limit and rejects one more byte", () => {
  setSizeLimitsForTest(1024, 100);
  try {
    // "data: " + body + "\n" + "\n" is the event; the empty line counts too.
    const event = (n: number) => `data: ${"x".repeat(n - 8)}\n\n`;
    assert.equal(new FrameParser().push(event(100)).length, 1);
    assert.throws(() => new FrameParser().push(event(101)));
  } finally {
    setSizeLimitsForTest(32 * 1024 * 1024, 1024 * 1024);
  }
});

test("the parser counts bytes, not characters, of a multi-byte event", () => {
  setSizeLimitsForTest(1024, 100);
  try {
    // 40 characters of 3 bytes: 120 bytes of data, but only 40 UTF-16 code units.
    assert.throws(() => new FrameParser().push(`data: ${"€".repeat(40)}\n\n`));
    // 30 of them: 90 + 6 + 2 = 98 bytes, accepted.
    assert.equal(new FrameParser().push(`data: ${"€".repeat(30)}\n\n`).length, 1);
  } finally {
    setSizeLimitsForTest(32 * 1024 * 1024, 1024 * 1024);
  }
});
