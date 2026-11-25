import { randomInt } from "node:crypto";
import { readFileSync } from "node:fs";
import { test as base, expect, type Locator, type Page } from "@playwright/test";
import { parseHeaders } from "../../src/build/headers.ts";
import { myLane } from "./lanes.ts";

// The control endpoint and the preview of the lane of this worker (lanes.ts).
export const controlUrl = `http://127.0.0.1:${myLane.controlPort}`;
const laneUrl = `http://localhost:${myLane.previewPort}`;

// The control endpoint of the test program (apps/backend/internal/e2e/uiserver_test.go).
export async function control<T = unknown>(
  method: "GET" | "POST",
  path: string,
  body?: unknown,
): Promise<{ status: number; body: T }> {
  const res = await fetch(controlUrl + path, {
    method,
    headers: body === undefined ? {} : { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return { status: res.status, body: (await res.json()) as T };
}

// Replaces the progress of a bulk job in the fake core. The total and the creation time stay.
export async function setBulkJob(
  id: string,
  job: {
    status: string;
    sent: number;
    failed: number;
    progress_percent: number;
    estimated_finish_in_seconds?: number;
  },
): Promise<void> {
  const { status } = await fetch(`${controlUrl}/bulk`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ id, ...job }),
  });
  expect(status).toBe(204);
}

// The UpdateInstanceConfig calls of the fake core for one instance, in order. A field that the call does not set is null.
export async function coreUpdates(instanceId: string): Promise<
  {
    webhooks: { event: string; url: string; secret: string | null }[] | null;
    enabled: boolean | null;
    send_only: boolean | null;
  }[]
> {
  const { status, body } = await control<Awaited<ReturnType<typeof coreUpdates>>>(
    "GET",
    `/core/updates?instance=${encodeURIComponent(instanceId)}`,
  );
  expect(status).toBe(200);
  return body;
}

export type Person = { label: string; role: string; email: string; password: string };
export type Seeded = {
  owner: Person;
  client: Person;
  instance: { name: string; id: string; path: string };
};

// One organization with an owner, a client user of the client "Shop", and the connected instance "Main".
export async function seedBasic(): Promise<Seeded> {
  const { status, body } = await control<{
    users: Person[];
    instances: Seeded["instance"][];
  }>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    clients: [{ name: "Shop" }],
    users: [
      { label: "owner", role: "owner" },
      { label: "shopper", role: "client", client: "Shop" },
    ],
    instances: [{ name: "Main", client: "Shop", provider: "WHATSAPP_WEB", status: "CONNECTED" }],
  });
  expect(status).toBe(200);
  return {
    owner: body.users[0] as Person,
    client: body.users[1] as Person,
    instance: body.instances[0] as Seeded["instance"],
  };
}

// Fills the login form. The screens must give these labels and this button.
export async function login(page: Page, who: Person): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Email").fill(who.email);
  await page.getByLabel("Password").fill(who.password);
  await page.getByRole("button", { name: "Log in" }).click();
}

// A link of the sidebar. At the phone width the sidebar is in the Sheet behind the button "Menu":
// open it first.
export async function mainLink(page: Page, name: string): Promise<Locator> {
  // A test can call this right after `login`, so wait for the header before the first look.
  const nav = page.getByRole("navigation", { name: "Main" });
  const menu = page.getByRole("button", { name: "Menu", exact: true });
  // The Sheet of an earlier call can still close. While it closes, its nav and the button are both
  // visible: wait until it is gone. first() covers a Sheet that starts to close after this wait.
  await expect(page.getByRole("dialog", { name: "Menu" })).toBeHidden();
  await expect(menu.or(nav).first()).toBeVisible();
  if (await menu.isVisible()) {
    await menu.click();
    return page.getByRole("dialog", { name: "Menu" }).getByRole("link", { name, exact: true });
  }
  return nav.getByRole("link", { name, exact: true });
}

// The words of the health status in its live region. The layout always has this region, also at the
// phone width, where the button of the health status is in the closed Sheet of "Menu".
export const healthState = (page: Page, text: RegExp): Locator =>
  page.locator('[data-slot="health-state"]').filter({ hasText: text });

// The button of the health status in the sidebar. At the phone width the sidebar is in the Sheet of
// "Menu": this opens it. Press Escape (or use `closeMenu`) before a click on the screen.
export async function healthButton(page: Page, text: RegExp): Promise<Locator> {
  return (await sidebar(page)).getByRole("button", { name: text });
}

// The theme button in the sidebar, with the same rule as `healthButton`.
export async function themeButton(page: Page, name: string | RegExp): Promise<Locator> {
  return (await sidebar(page)).getByRole("button", { name });
}

async function sidebar(page: Page): Promise<Locator> {
  const nav = page.getByRole("navigation", { name: "Main" });
  const menu = page.getByRole("button", { name: "Menu", exact: true });
  const sheet = page.getByRole("dialog", { name: "Menu" });
  await expect(menu.or(nav).first()).toBeVisible();
  // The open Sheet hides "Menu" from the accessibility tree, so look at the Sheet first.
  if (await sheet.isVisible()) return sheet;
  if (!(await menu.isVisible())) {
    return page.locator('[data-slot="sidebar"]').locator("visible=true");
  }
  await menu.click();
  await expect(sheet).toBeVisible();
  // The Sheet slides in from the right side: wait for the end of the move.
  const width = page.viewportSize()?.width ?? 0;
  await expect
    .poll(async () => {
      const box = await sheet.boundingBox();
      return box ? Math.round(box.x + box.width) : -1;
    })
    .toBe(width);
  return sheet;
}

// Closes the Sheet of "Menu" when it is open. A modal Sheet hides the screen from the accessibility tree.
export async function closeMenu(page: Page): Promise<void> {
  const sheet = page.getByRole("dialog", { name: "Menu" });
  // The first Escape closes the card of the health status when it is open.
  await expect(async () => {
    if (await sheet.isVisible()) await page.keyboard.press("Escape");
    await expect(sheet).toBeHidden({ timeout: 1000 });
  }).toPass();
}

const expected = parseHeaders(readFileSync(new URL("../../headers.conf", import.meta.url), "utf8"));
const expectedDocs = parseHeaders(
  readFileSync(new URL("../../headers-docs.conf", import.meta.url), "utf8"),
);
// The paths of the docs page: the same rule as `outsidePanel` in src/app/session.tsx. Importing that
// file would load the whole app into the test program.
const isDocsPath = (path: string) => /^\/docs(\/|\/index\.html)?$/.test(path);

// Each test fails on a CSP violation, on a console error, on a request to another origin,
// or on a document that lacks a header of headers.conf (headers-docs.conf for the docs page).
// A test of an outage sets `tolerateFailedRequests`: the browser logs each failed request as a console error.
export const test = base.extend<{
  guard: undefined;
  tolerateFailedRequests: boolean;
  testProgram: undefined;
}>({
  tolerateFailedRequests: [false, { option: true }],

  // The pages open on the preview of this worker's lane. The CSP project has its own preview.
  baseURL: async ({ baseURL }, use, testInfo) => {
    await use(testInfo.project.name === "csp" ? baseURL : laneUrl);
  },

  // Each test has its own address, so the limits of the login never give a random 429.
  context: async ({ context }, use) => {
    const ip = `10.${randomInt(256)}.${randomInt(256)}.${randomInt(1, 255)}`;
    await context.setExtraHTTPHeaders({ "X-Forwarded-For": ip });
    await use(context);
  },

  // "core stop" and "backend stop" act on all later tests of the lane: each test leaves the program
  // as it found it.
  testProgram: [
    // biome-ignore lint/correctness/noEmptyPattern: Playwright reads the first argument as a fixture list.
    async ({}, use, testInfo) => {
      await use(undefined);
      if (testInfo.project.name === "csp") return;
      await control("POST", "/fault", {});
      await control("POST", "/core", { action: "start" });
      // "start" with mail on gives 409 while the backend runs, and mail would stay as the test left it.
      // So stop it first (409 when it is stopped already), and then start it with mail on.
      await control("POST", "/backend", { action: "stop" });
      await control("POST", "/backend", { action: "start", mail: true });
    },
    { auto: true },
  ],

  guard: [
    async ({ page, baseURL, tolerateFailedRequests }, use) => {
      const origin = new URL(baseURL ?? "http://localhost").origin;
      const problems: string[] = [];

      await page.addInitScript(() => {
        document.addEventListener("securitypolicyviolation", (e) => {
          console.error(`CSP violation: ${e.violatedDirective} blocked ${e.blockedURI}`);
        });
      });
      page.on("console", (m) => {
        if (tolerateFailedRequests && m.text().startsWith("Failed to load resource")) return;
        // A page with no login asks getMe, and the 401 is its answer. The browser logs it.
        if (
          m.text().includes("status of 401") &&
          new URL(m.location().url, origin).pathname === "/api/v1/me"
        )
          return;
        if (m.type() === "error") problems.push(`console: ${m.text()}`);
      });
      page.on("pageerror", (e) => problems.push(`page error: ${e.message}`));
      page.on("request", (r) => {
        const u = new URL(r.url());
        if (u.protocol.startsWith("http") && u.origin !== origin) {
          problems.push(`request to another origin: ${r.url()}`);
        }
      });
      page.on("response", (r) => {
        if (r.request().resourceType() !== "document" || r.frame() !== page.mainFrame()) return;
        const got = r.headers();
        const wanted = isDocsPath(new URL(r.url()).pathname) ? expectedDocs : expected;
        for (const [name, value] of Object.entries(wanted)) {
          if (got[name.toLowerCase()] !== value) problems.push(`header ${name} is absent or wrong`);
        }
      });

      await use(undefined);
      expect(problems).toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };

// Nothing goes out of the screen at the side: no scroll of the page to the side, no control or
// text cut, and no box that scrolls to the side.
export async function fits(page: Page, name: string) {
  const problems = await page.evaluate(() => {
    const out: string[] = [];
    const w = document.documentElement.clientWidth;
    const root = document.scrollingElement ?? document.documentElement;
    if (root.scrollWidth > w) out.push(`the page is ${root.scrollWidth} px wide in ${w} px`);
    const what = (el: Element) =>
      `${el.tagName.toLowerCase()} "${(el.textContent ?? "").trim().slice(0, 30)}"`;
    const selector =
      "button, a[href], input, select, textarea, [role=switch], [role=combobox], h1, h2, h3, p, li, dd, dt, label";
    for (const el of document.querySelectorAll(selector)) {
      const r = el.getBoundingClientRect();
      if (r.width <= 1 || r.height <= 1) continue;
      if (getComputedStyle(el).visibility === "hidden") continue;
      if (r.right > w + 1 || r.left < -1)
        out.push(`${what(el)} goes from ${Math.round(r.left)} to ${Math.round(r.right)}`);
    }
    for (const el of document.querySelectorAll("body *")) {
      const s = getComputedStyle(el);
      if (!["auto", "scroll"].includes(s.overflowX)) continue;
      if (el.scrollWidth > el.clientWidth + 1) out.push(`${what(el)} scrolls to the side`);
    }
    return out;
  });
  expect.soft(problems, name).toEqual([]);
}
