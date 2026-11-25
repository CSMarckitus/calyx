import { control, expect, test } from "./support.ts";

// A smoke test of the setup only: it needs no screen. The fixture of support.ts checks the headers.
test("the test program and the preview answer", async ({ page, request }) => {
  const ready = await control<{ ready: boolean }>("GET", "/ready");
  expect(ready).toEqual({ status: 200, body: { ready: true } });

  const response = await page.goto("/");
  expect(response?.status()).toBe(200);

  // The preview sends /healthz to the backend of the test program.
  const health = await request.get("/healthz");
  expect(health.status()).toBe(200);
  expect(await health.json()).toMatchObject({ database: "ok" });

  const version = await request.get("/version.json");
  expect(await version.json()).toHaveProperty("build");
});
