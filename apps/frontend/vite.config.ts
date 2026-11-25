import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";
import { parseHeaders } from "./src/build/headers.ts";
import { makeVersion, versionJson } from "./src/build/version.ts";

const here = (p: string) => new URL(p, import.meta.url);
// The root package.json is the one source of the product version.
const pkg = JSON.parse(readFileSync(here("../../package.json"), "utf8")) as { version: string };
const headers = parseHeaders(readFileSync(here("./headers.conf"), "utf8"));
const docsHeaders = parseHeaders(readFileSync(here("./headers-docs.conf"), "utf8"));
// The paths of the docs page, as in nginx.conf. The query string and the hash do not count.
const docsPath = /^\/docs(\/|\/index\.html)?$/;
const api = process.env.CALYX_API ?? "http://127.0.0.1:8080";
const proxy = Object.fromEntries(
  ["/api", "/healthz", "/openapi.json", "/mcp"].map((p) => [
    p,
    { target: api, changeOrigin: false },
  ]),
);

// Writes dist/version.json and puts the same value in the page (meta tags, no inline script).
function versionPlugin(): Plugin {
  const v = makeVersion(pkg.version, new Date());
  return {
    name: "calyx-version",
    transformIndexHtml: () => [
      { tag: "meta", attrs: { name: "calyx-version", content: v.version }, injectTo: "head" },
      { tag: "meta", attrs: { name: "calyx-build", content: v.build }, injectTo: "head" },
    ],
    generateBundle() {
      this.emitFile({ type: "asset", fileName: "version.json", source: versionJson(v) });
    },
  };
}

// Serves /docs, which is a second HTML page and not a route of the panel.
function docsPage(): Plugin {
  return {
    name: "calyx-docs-page",
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const url = new URL(req.url ?? "/", "http://localhost");
        if (docsPath.test(url.pathname)) req.url = `/docs/index.html${url.search}`;
        next();
      });
    },
    // The middleware answers and never calls next(): Vite would then set the headers of headers.conf.
    configurePreviewServer(server) {
      const { root, build } = server.config;
      const file = resolve(root, build.outDir, "docs/index.html");
      server.middlewares.use((req, res, next) => {
        if (!docsPath.test(new URL(req.url ?? "/", "http://localhost").pathname)) return next();
        for (const [name, value] of Object.entries(docsHeaders)) res.setHeader(name, value);
        res.setHeader("Cache-Control", "no-cache");
        if (req.method !== "GET" && req.method !== "HEAD") {
          res.statusCode = 405;
          res.end();
          return;
        }
        res.setHeader("Content-Type", "text/html; charset=utf-8");
        res.end(req.method === "HEAD" ? undefined : readFileSync(file));
      });
    },
  };
}

export default defineConfig(({ mode }) => ({
  // Mode csp has no docs page.
  plugins: [react(), tailwindcss(), versionPlugin(), ...(mode === "csp" ? [] : [docsPage()])],
  resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) } },
  server: { port: 5173, strictPort: true, proxy },
  preview: { port: 4173, strictPort: true, headers, proxy },
  // Mode csp builds only the page of the CSP test, in its own folder.
  build:
    mode === "csp"
      ? { outDir: "dist-csp", rollupOptions: { input: "test/ui/csp/index.html" } }
      : { manifest: true, rollupOptions: { input: ["index.html", "docs/index.html"] } },
  test: { include: ["src/**/*.test.{ts,tsx}"], environment: "jsdom" },
}));
