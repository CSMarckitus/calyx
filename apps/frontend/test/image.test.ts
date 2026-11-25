import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { parseHeaders } from "../src/build/headers.ts";
import { cleanup, docker, imageOf, labelOf, runId } from "./image-docker.ts";

// This test builds the image and runs plain containers. It uses no compose project, no .env and
// no fixed port: a compose project of the author can run at the same time. Every container and
// network has a label with the id of this run, so the cleanup finds them also after a failure.
// The global setup (image-global.ts) cleans again in the main process, for an interrupted run.

const root = fileURLToPath(new URL("../../../", import.meta.url));
const frontendDir = fileURLToPath(new URL("../", import.meta.url));
const run = runId();
const label = labelOf(run);
const image = imageOf(run);
// The gateway uses the same tag as docker-compose.yml.
const nginxImage = "nginxinc/nginx-unprivileged:1.31.7-alpine";
const nodeImage = "node:26.13-alpine";

function createNetwork(name: string): string {
  const full = `imgtest-${run}-${name}`;
  docker("network", "create", "--label", label, full);
  return full;
}

interface Options {
  network: string;
  alias?: string;
  env?: string[];
  mounts?: string[];
  publish?: boolean;
}

// Returns the container name.
function start(name: string, img: string, opts: Options, command: string[] = []): string {
  const full = `imgtest-${run}-${name}`;
  const args = ["run", "-d", "--name", full, "--label", label, "--network", opts.network];
  if (opts.alias) args.push("--network-alias", opts.alias);
  for (const e of opts.env ?? []) args.push("-e", e);
  for (const m of opts.mounts ?? []) args.push("-v", m);
  // A random port on the loopback address: no fixed port is used.
  if (opts.publish) args.push("-p", "127.0.0.1::8080");
  docker(...args, img, ...command);
  return full;
}

function origin(container: string): string {
  return `http://${docker("port", container, "8080/tcp").split("\n")[0].replace("0.0.0.0", "127.0.0.1")}`;
}

function remove(container: string): void {
  docker("rm", "-f", container);
}

async function eventually<T>(
  what: string,
  fn: () => Promise<T | undefined>,
  seconds = 30,
): Promise<T> {
  const end = Date.now() + seconds * 1000;
  let last: unknown;
  while (Date.now() < end) {
    try {
      const value = await fn();
      if (value !== undefined) return value;
    } catch (error) {
      last = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 300));
  }
  throw new Error(`Timeout: ${what} ${last ? String(last) : ""}`);
}

async function waitFor(url: string): Promise<void> {
  await eventually(`${url} answers 200`, async () =>
    (await fetch(url)).status === 200 ? true : undefined,
  );
}

// A stub of the backend. It echoes what it gets, and it has a slow stream.
const stubScript = `
const http = require("http");
const id = process.env.STUB_ID;
http.createServer((req, res) => {
  if (req.url === "/api/stream") {
    // Like the real SSE: the headers go first, and each small event comes later, alone.
    res.writeHead(200, { "content-type": "text/event-stream", "x-stub": id });
    res.flushHeaders();
    setTimeout(() => res.write("first\\n"), 300);
    setTimeout(() => res.end("last\\n"), 3000);
    return;
  }
  let bytes = 0;
  req.on("data", (c) => { bytes += c.length; });
  req.on("end", () => {
    res.writeHead(200, { "content-type": "application/json", "x-stub": id });
    res.end(JSON.stringify({ id, method: req.method, url: req.url, headers: req.headers, bytes }));
  });
}).listen(8080);
`;

function startStub(name: string, network: string, id: string): string {
  return start(name, nodeImage, { network, alias: "backend", env: [`STUB_ID=${id}`] }, [
    "node",
    "-e",
    stubScript,
  ]);
}

const backendPaths = ["/api/", "/api/v1/me", "/mcp", "/mcp/x", "/healthz", "/openapi.json"];
const corePath = "/meta/webhook";
const headersConf = parseHeaders(readFileSync(`${frontendDir}headers.conf`, "utf8"));
const headersDocsConf = parseHeaders(readFileSync(`${frontendDir}headers-docs.conf`, "utf8"));

beforeAll(() => {
  // Compose passes no build arguments, so this is the same build as "docker compose build".
  docker("build", "-q", "-f", `${frontendDir}Dockerfile`, "-t", image, root);
}, 600_000);

afterAll(() => cleanup(run));

describe("image alone", () => {
  let base = "";

  beforeAll(async () => {
    const network = createNetwork("one");
    base = origin(start("frontend", image, { network, publish: true }));
    await waitFor(`${base}/`);
  });

  it("has the headers.conf file with four headers", () => {
    expect(Object.keys(headersConf)).toHaveLength(4);
  });

  it.each(
    [...backendPaths, corePath].flatMap((path) =>
      ["GET", "POST"].map((method) => [method, path] as const),
    ),
  )("answers %s %s with 503 text/plain", async (method, path) => {
    const res = await fetch(base + path, { method, body: method === "POST" ? "{}" : undefined });
    expect(res.status).toBe(503);
    expect(res.headers.get("content-type")).toMatch(/^text\/plain/);
    expect(await res.text()).toContain("not reachable");
  });

  it.each(["/api", "/%61pi/v1/me", "//api/v1/me", "/meta/webhook/x"])(
    "answers 503 for the odd path %s",
    async (path) => {
      expect((await fetch(base + path)).status).toBe(503);
    },
  );

  it("answers 503 for the other methods on a backend path", async () => {
    for (const method of ["PUT", "DELETE", "PATCH"]) {
      expect((await fetch(`${base}/api/v1/me`, { method })).status).toBe(503);
    }
  });

  it("does not take a similar name for a backend path", async () => {
    for (const path of ["/apifoo", "/healthzx", "/openapi.jsonx", "/meta/webhooks"]) {
      const res = await fetch(base + path);
      expect(res.status, path).toBe(200);
      expect(res.headers.get("content-type")).toMatch(/^text\/html/);
    }
  });

  it("serves index.html at the root", async () => {
    const res = await fetch(`${base}/`);
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toMatch(/^text\/html/);
    expect(await res.text()).toContain('<div id="root">');
  });

  it("falls back to index.html for a route of the page", async () => {
    const index = await (await fetch(`${base}/index.html`)).text();
    for (const path of ["/instances", "/instances/abc/connect", "/login"]) {
      const res = await fetch(base + path);
      expect(res.status, path).toBe(200);
      expect(await res.text()).toBe(index);
    }
  });

  it("answers 404 for a file in /assets/ that does not exist", async () => {
    for (const path of ["/assets/nope.js", "/assets/", "/assets/sub/nope.css"]) {
      expect((await fetch(base + path)).status, path).toBe(404);
    }
  });

  it("answers 405 for a method other than GET and HEAD", async () => {
    for (const path of ["/", "/instances", "/index.html", "/version.json", "/assets/nope.js"]) {
      for (const method of ["POST", "PUT", "DELETE"]) {
        expect(
          ((await fetch(base + path, { method })) as Response).status,
          `${method} ${path}`,
        ).toBe(405);
      }
    }
  });

  it("answers HEAD like GET", async () => {
    const res = await fetch(`${base}/`, { method: "HEAD" });
    expect(res.status).toBe(200);
  });

  async function assetPath(): Promise<string> {
    const html = await (await fetch(`${base}/index.html`)).text();
    const m = /\/assets\/[^"']+\.js/.exec(html);
    if (!m) throw new Error("index.html has no script in /assets/");
    return m[0];
  }

  const headerCases: [string, () => Promise<string>][] = [
    ["a page", async () => "/"],
    ["a fallback route", async () => "/instances"],
    ["an asset", assetPath],
    ["a 404", async () => "/assets/nope.js"],
    ["a 503", async () => "/api/v1/me"],
    ["the core path", async () => corePath],
    ["version.json", async () => "/version.json"],
  ];

  it.each(headerCases)("sends each header of headers.conf on %s", async (_name, path) => {
    const res = await fetch(base + (await path()));
    for (const [name, value] of Object.entries(headersConf)) {
      expect(res.headers.get(name), name).toBe(value);
    }
  });

  it("sends the headers on a 405", async () => {
    const res = await fetch(`${base}/`, { method: "POST" });
    for (const [name, value] of Object.entries(headersConf)) {
      expect(res.headers.get(name), name).toBe(value);
    }
  });

  it("does not name the server version", async () => {
    const res = await fetch(`${base}/`);
    expect(res.headers.get("server")).toBe("nginx");
  });

  it.each(["/index.html", "/theme.js", "/version.json", "/", "/instances"])(
    "sends no-cache for %s",
    async (path) => {
      const res = await fetch(base + path);
      expect(res.status).toBe(200);
      expect(res.headers.get("cache-control"), path).toBe("no-cache");
    },
  );

  it("sends immutable one year for a file in /assets/", async () => {
    const res = await fetch(base + (await assetPath()));
    expect(res.status).toBe(200);
    expect(res.headers.get("cache-control")).toBe("public, max-age=31536000, immutable");
  });

  it("serves version.json with the two fields", async () => {
    const body = (await (await fetch(`${base}/version.json`)).json()) as Record<string, string>;
    expect(Object.keys(body).sort()).toEqual(["build", "version"]);
  });

  it.each(["/docs", "/docs/", "/docs/index.html", "/docs?x=1"])(
    "serves the docs page at %s with the headers of headers-docs.conf",
    async (path) => {
      const res = await fetch(base + path);
      expect(res.status, path).toBe(200);
      expect(res.headers.get("content-type")).toMatch(/^text\/html/);
      expect(res.headers.get("cache-control")).toBe("no-cache");
      for (const [name, value] of Object.entries(headersDocsConf)) {
        expect(res.headers.get(name), name).toBe(value);
      }
      expect(await res.text()).toContain("API docs");
    },
  );

  it("keeps the CSP of headers.conf at the root", async () => {
    const res = await fetch(`${base}/`);
    expect(res.headers.get("content-security-policy")).toBe(headersConf["Content-Security-Policy"]);
    expect(headersConf["Content-Security-Policy"]).not.toBe(
      headersDocsConf["Content-Security-Policy"],
    );
  });

  it("answers 405 for a method other than GET and HEAD on the docs page", async () => {
    for (const path of ["/docs", "/docs/", "/docs/index.html"]) {
      const res = await fetch(base + path, { method: "POST" });
      expect(res.status, path).toBe(405);
    }
  });

  it.each(["/.vite/manifest.json", "/.vite/", "/.env", "/docs/.x"])(
    "answers 404 for the dot path %s",
    async (path) => {
      const res = await fetch(base + path);
      expect(res.status, path).toBe(404);
      expect(res.headers.get("content-security-policy")).toBe(
        headersConf["Content-Security-Policy"],
      );
    },
  );

  it("runs as the user nginx (uid 101)", () => {
    const name = docker("ps", "-q", "--filter", `label=${label}`, "--filter", "name=frontend");
    expect(docker("exec", name, "id", "-u")).toBe("101");
  });
});

describe("gateway", () => {
  let network = "";
  let base = "";
  let current = "";

  async function get(path: string, init?: RequestInit) {
    return fetch(base + path, init);
  }

  async function echo(path: string, init?: RequestInit) {
    const res = await get(path, init);
    expect(res.status, path).toBe(200);
    return (await res.json()) as {
      id: string;
      method: string;
      url: string;
      bytes: number;
      headers: Record<string, string>;
    };
  }

  beforeAll(async () => {
    network = createNetwork("two");
    start("front2", image, { network, alias: "frontend" });
    const gateway = start("gateway", nginxImage, {
      network,
      publish: true,
      mounts: [`${frontendDir}gateway.conf:/etc/nginx/conf.d/default.conf:ro`],
    });
    base = origin(gateway);
    await waitFor(`${base}/`);
  });

  it("starts with no backend: the page opens", async () => {
    const res = await get("/");
    expect(res.status).toBe(200);
    expect(await res.text()).toContain('<div id="root">');
    expect((await get("/instances")).status).toBe(200);
  });

  it.each(backendPaths)("answers 502 on %s while there is no backend", async (path) => {
    expect((await get(path)).status).toBe(502);
  });

  it("answers 503 on the core path with no backend", async () => {
    for (const method of ["GET", "POST"]) {
      const res = await get(corePath, { method, body: method === "POST" ? "{}" : undefined });
      expect(res.status).toBe(503);
      expect(res.headers.get("content-type")).toMatch(/^text\/plain/);
    }
  });

  it("follows a backend that starts after the gateway", async () => {
    current = startStub("stub1", network, "one");
    const body = await eventually("the first stub answers", async () => {
      const res = await get("/healthz");
      return res.status === 200 ? ((await res.json()) as { id: string }) : undefined;
    });
    expect(body.id).toBe("one");
  });

  it.each(backendPaths)("sends %s to the backend", async (path) => {
    const body = await echo(path);
    expect(body.id).toBe("one");
    expect(body.url).toBe(path);
  });

  it("sends the query and the method to the backend", async () => {
    const body = await echo("/api/v1/x?a=1&b=2", { method: "POST", body: "hello" });
    expect(body.method).toBe("POST");
    expect(body.url).toBe("/api/v1/x?a=1&b=2");
    expect(body.bytes).toBe(5);
  });

  it("keeps the Host header", async () => {
    const body = await echo("/api/");
    expect(body.headers.host).toBe(new URL(base).host);
  });

  it("sends the page and the unknown routes to the frontend", async () => {
    for (const path of ["/", "/instances", "/index.html", "/assets/nope.js"]) {
      const res = await get(path);
      expect(res.headers.get("x-stub"), path).toBeNull();
    }
    expect((await get("/instances")).status).toBe(200);
    expect((await get("/assets/nope.js")).status).toBe(404);
  });

  it("keeps the headers of the frontend", async () => {
    const res = await get("/");
    for (const [name, value] of Object.entries(headersConf)) {
      expect(res.headers.get(name), name).toBe(value);
    }
  });

  it("answers 503 on the core path, with a backend running", async () => {
    for (const method of ["GET", "POST"]) {
      const res = await get(corePath, { method, body: method === "POST" ? "{}" : undefined });
      expect(res.status).toBe(503);
      expect(res.headers.get("x-stub")).toBeNull();
    }
  });

  it("overwrites a forged X-Forwarded-For", async () => {
    const body = await echo("/api/", { headers: { "X-Forwarded-For": "6.6.6.6, 7.7.7.7" } });
    const seen = body.headers["x-forwarded-for"];
    expect(seen).not.toContain("6.6.6.6");
    expect(seen).not.toContain(",");
    expect(seen).toMatch(/^\d+\.\d+\.\d+\.\d+$/);
  });

  it("sets X-Forwarded-For when the client sends none", async () => {
    const body = await echo("/api/");
    expect(body.headers["x-forwarded-for"]).toMatch(/^\d+\.\d+\.\d+\.\d+$/);
  });

  // The behavior test below cannot tell the two modes apart: nginx sent each small event at once
  // also with "proxy_buffering on" (tried with a stub that flushes the headers and sends the first
  // event 300 ms later). So the directives are checked in the text of the file, too.
  it("has no buffer in the text of the backend location", () => {
    const conf = readFileSync(`${frontendDir}gateway.conf`, "utf8");
    const block = /location ~ \^\/\(api[^{]*\{([^}]*)\}/.exec(conf)?.[1] ?? "";
    expect(block).toContain("proxy_buffering off;");
    expect(block).toContain("proxy_request_buffering off;");
  });

  it("sends a stream that arrives in small events", async () => {
    const start = Date.now();
    const res = await get("/api/stream");
    const reader = (res.body as ReadableStream<Uint8Array>).getReader();
    const first = await reader.read();
    const firstAt = Date.now() - start;
    expect(new TextDecoder().decode(first.value)).toBe("first\n");
    // The stub waits 3 s before the last bytes.
    expect(firstAt).toBeLessThan(1500);
    let rest = "";
    for (;;) {
      const part = await reader.read();
      if (part.done) break;
      rest += new TextDecoder().decode(part.value);
    }
    expect(rest).toBe("last\n");
    expect(Date.now() - start).toBeGreaterThanOrEqual(2500);
  });

  it("accepts a body of 16 MiB and refuses one of 18 MiB", async () => {
    const ok = await echo("/api/v1/x", { method: "POST", body: new Uint8Array(16 * 1024 * 1024) });
    expect(ok.bytes).toBe(16 * 1024 * 1024);
    const big = await get("/api/v1/x", { method: "POST", body: new Uint8Array(18 * 1024 * 1024) });
    expect(big.status).toBe(413);
  });

  it("follows a backend with a new address", async () => {
    const before = docker(
      "inspect",
      "-f",
      "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
      current,
    );
    remove(current);
    // A container that holds the freed address, so the new stub gets another one.
    start("holder", nodeImage, { network }, ["node", "-e", "setTimeout(()=>{},600000)"]);
    current = startStub("stub2", network, "two");
    const after = docker(
      "inspect",
      "-f",
      "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
      current,
    );
    expect(after).not.toBe(before);
    const body = await eventually("the gateway follows the new stub", async () => {
      const res = await get("/healthz");
      if (res.status !== 200) return undefined;
      const json = (await res.json()) as { id: string };
      return json.id === "two" ? json : undefined;
    });
    expect(body.id).toBe("two");
  });

  it("answers 502 again when the backend stops, and the page still opens", async () => {
    remove(current);
    await eventually("the gateway answers 502", async () =>
      (await get("/api/")).status === 502 ? true : undefined,
    );
    expect((await get("/")).status).toBe(200);
  });
});
