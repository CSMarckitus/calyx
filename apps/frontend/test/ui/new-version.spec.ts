import type { Page } from "@playwright/test";
import { expect, healthState, login, seedBasic, test } from "./support.ts";

const newBuild = { version: "0.1.0", build: "2099-01-01T00:00:00.000Z" };

async function serveVersion(page: Page, body: object | null) {
  await page.unroute("**/version.json");
  if (body === null) return; // The real file of the build.
  await page.route("**/version.json", (route) => route.fulfill({ json: body }));
}

// A page that goes to the background and comes back asks for version.json again.
async function comeBack(page: Page) {
  await page.evaluate(() => {
    const set = (state: string) => {
      Object.defineProperty(document, "visibilityState", { value: state, configurable: true });
      Object.defineProperty(document, "hidden", { value: state === "hidden", configurable: true });
      document.dispatchEvent(new Event("visibilitychange"));
    };
    set("hidden");
    set("visible");
  });
}

test.beforeEach(async ({ page }) => {
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(healthState(page, /^Backend and core fully operational/)).toBeVisible();
});

test("the build of the page is the build of the server: no notice", async ({ page }) => {
  const asked = page.waitForResponse((r) => r.url().endsWith("/version.json"));
  await comeBack(page);
  await asked;
  await expect(page.getByText("A new version is ready.")).toHaveCount(0);
});

test("a different build shows the notice, and Reload gets the new page", async ({ page }) => {
  await serveVersion(page, newBuild);
  await comeBack(page);
  await expect(page.getByText("A new version is ready.")).toBeVisible();
  const reload = page.getByRole("button", { name: "Reload" });
  await expect(reload).toBeVisible();

  // The server of the test has the same build as the page, so the new page has no notice.
  await serveVersion(page, null);
  await reload.click();
  await expect(healthState(page, /^Backend and core fully operational/)).toBeVisible();
  await expect(page.getByText("A new version is ready.")).toHaveCount(0);
});
