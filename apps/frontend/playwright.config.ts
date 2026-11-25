import { defineConfig } from "@playwright/test";
import { cspPort, lanes, workers } from "./test/ui/lanes.ts";

// CALYX_UI=csp runs the CSP spec alone: no test program and no real build (script test:ui:csp).
const cspOnly = process.env.CALYX_UI === "csp";

export default defineConfig({
  testDir: "test/ui",
  testMatch: "**/*.spec.ts",
  // One lane (test program and preview) for each worker: test/ui/lanes.ts.
  workers,
  // A spec file stays on one worker, so its tests keep their order.
  fullyParallel: false,
  retries: 0,
  timeout: 150_000, // An outage test waits for two health checks.
  reporter: "list",
  globalSetup: cspOnly ? undefined : "./test/ui/global-setup.ts",
  use: { browserName: "chromium" },
  projects: [
    {
      name: "csp",
      testMatch: "csp/**/*.spec.ts",
      use: { baseURL: `http://localhost:${cspPort}` },
    },
    ...[
      ["phone-360", 360, 800],
      ["desktop-1280", 1280, 800],
    ].map(([name, width, height]) => ({
      name: name as string,
      testIgnore: "csp/**",
      use: {
        // test/ui/support.ts changes it to the preview of the worker's lane.
        baseURL: `http://localhost:${lanes[0].previewPort}`,
        viewport: { width: width as number, height: height as number },
      },
    })),
  ],
  webServer: [
    {
      // Mode csp builds the page of test/ui/csp. The preview sends the headers of headers.conf.
      command: `pnpm exec vite build --mode csp && pnpm exec vite preview --mode csp --port ${cspPort}`,
      url: `http://localhost:${cspPort}/test/ui/csp/index.html`,
      reuseExistingServer: false,
      timeout: 120_000,
    },
    ...(cspOnly
      ? []
      : lanes.map((l, i) => ({
          // Each preview proxies /api and /healthz to the backend of its lane (CALYX_API).
          // Playwright starts the servers one after the other, so only the first one builds.
          command: `${i === 0 ? "pnpm exec vite build && " : ""}pnpm exec vite preview --port ${l.previewPort}`,
          url: `http://localhost:${l.previewPort}/version.json`,
          reuseExistingServer: false,
          timeout: 120_000,
          env: { CALYX_API: `http://127.0.0.1:${l.backendPort}` },
        }))),
  ],
});
