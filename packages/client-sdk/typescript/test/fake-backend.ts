import { createServer, type IncomingHttpHeaders } from "node:http";
import type { AddressInfo } from "node:net";

export interface Recorded {
  method: string;
  url: string;
  headers: IncomingHttpHeaders;
  body: string;
}

export interface Reply {
  status?: number;
  headers?: Record<string, string>;
  // An object becomes JSON. A string is sent as it is.
  body?: unknown;
  // Never answer. The test ends the call with a time limit or a signal.
  hang?: boolean;
  delayMs?: number;
  // An event stream. The server writes each frame, then ends the response unless keepOpen is set.
  stream?: {
    frames: (string | Uint8Array)[];
    keepOpen?: boolean;
    onClose?: () => void;
    gapMs?: number;
  };
}

export interface FakeBackend {
  baseUrl: string;
  requests: Recorded[];
  close(): Promise<void>;
}

// A recorded problem+json body of the backend.
export function problem(
  status: number,
  code: string,
  detail = "A problem.",
  headers: Record<string, string> = {},
): Reply {
  return {
    status,
    headers: { "content-type": "application/problem+json", "x-request-id": "req-1", ...headers },
    body: { type: "about:blank", title: "Problem", status, code, detail },
  };
}

// Listens on 127.0.0.1 in the test process. No other network.
export async function startFakeBackend(reply: (req: Recorded) => Reply): Promise<FakeBackend> {
  const requests: Recorded[] = [];
  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const recorded: Recorded = {
        method: req.method ?? "",
        url: req.url ?? "",
        headers: req.headers,
        body: Buffer.concat(chunks).toString("utf8"),
      };
      requests.push(recorded);
      const r = reply(recorded);
      if (r.hang) return;
      if (r.stream) {
        const { frames, keepOpen, onClose, gapMs = 0 } = r.stream;
        res.writeHead(r.status ?? 200, { "content-type": "text/event-stream", ...r.headers });
        if (onClose) res.on("close", onClose);
        void (async () => {
          for (const f of frames) {
            if (gapMs > 0) await new Promise((ok) => setTimeout(ok, gapMs));
            if (res.destroyed) return;
            res.write(f);
          }
          if (!keepOpen) res.end();
        })();
        return;
      }
      setTimeout(() => {
        const isText = typeof r.body === "string";
        const headers = {
          ...(r.body !== undefined && !isText ? { "content-type": "application/json" } : {}),
          ...r.headers,
        };
        res.writeHead(r.status ?? 200, headers);
        if (r.body === undefined) res.end();
        else res.end(isText ? (r.body as string) : JSON.stringify(r.body));
      }, r.delayMs ?? 0);
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  return {
    baseUrl: `http://127.0.0.1:${port}`,
    requests,
    close: () =>
      new Promise<void>((resolve) => {
        server.close(() => resolve());
        server.closeAllConnections();
      }),
  };
}
