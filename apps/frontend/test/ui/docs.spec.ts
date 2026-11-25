import { readFileSync } from "node:fs";
import type { Locator, Page } from "@playwright/test";
import { parseHeaders } from "../../src/build/headers.ts";
import { control, expect, login, mainLink, type Person, seedBasic, test } from "./support.ts";

// The API docs page.

const docsHeaders = parseHeaders(
  readFileSync(new URL("../../headers-docs.conf", import.meta.url), "utf8"),
);

const scalar = (page: Page) => page.locator('[data-slot="scalar"]');

// Opens the docs page from the main menu, and waits until Scalar shows the document.
async function openDocs(page: Page): Promise<void> {
  await (await mainLink(page, "API docs")).click();
  await expect(page).toHaveURL(/\/docs$/);
  await expect(scalar(page).getByRole("heading", { name: "Start here" })).toBeVisible();
}

// The sidebar of Scalar, with a link for each group. On a narrow screen it is behind "Open Menu".
async function sidebar(page: Page): Promise<Locator> {
  const open = scalar(page).getByRole("button", { name: "Open Menu" });
  if (await open.isVisible()) await open.click();
  const bar = scalar(page).getByRole("complementary", { name: /^Sidebar for / });
  await expect(bar).toBeVisible();
  return bar;
}

const groups = {
  owner: ["Account", "Organization", "Clients", "Instances", "Messages", "Bulk jobs"],
  client: ["Account", "Instances", "Messages", "Bulk jobs"],
};

// The names of the group links of the sidebar, in order. A group link goes to "#tag/<name>".
async function groupNames(bar: Locator): Promise<string[]> {
  return bar
    .locator('a[href^="#tag/"]')
    .evaluateAll((links) =>
      links
        .filter((a) => /^#tag\/[^/]+$/.test(a.getAttribute("href") ?? ""))
        .map((a) => (a.textContent ?? "").trim()),
    );
}

// The Sheet at the phone width: "API docs" is in the group Developers, and Escape closes the Sheet.
test.describe("the Sheet on a phone", () => {
  test.use({ viewport: { width: 320, height: 640 } });
  test.beforeEach(() => test.skip(test.info().project.name !== "phone-360", "320 px once"));

  for (const role of ["owner", "client"] as const) {
    test(`the Sheet of the ${role} has API docs in Developers, and Escape closes it`, async ({
      page,
    }) => {
      const { body } = await control<{ users: (Person & { label: string })[] }>("POST", "/seed", {
        organization: { name: "Acme", acknowledged: true },
        clients: [{ name: "Shop", can_create_instances: true }],
        users: [
          { label: "owner", role: "owner" },
          { label: "shopper", role: "client", client: "Shop" },
        ],
      });
      const who = body.users.find((u) => u.label === (role === "owner" ? "owner" : "shopper"));
      await login(page, who as Person);
      await expect(page).toHaveURL(/\/$/);

      const button = page.getByRole("button", { name: "Menu", exact: true });
      const sheet = page.getByRole("dialog", { name: "Menu" });
      const nav = sheet.getByRole("navigation", { name: "Main" });
      await expect(sheet).toHaveCount(0);
      await button.click();
      await expect(nav.getByRole("link")).toHaveText(
        role === "owner"
          ? [
              "Instances",
              "API keys",
              "Connect a program",
              "API docs",
              "Organization",
              "WhatsApp Web",
              "Account",
            ]
          : ["My WhatsApp", "API keys", "Connect a program", "API docs", "Account"],
      );
      const docs = nav
        .getByRole("list", { name: "Developers" })
        .getByRole("link", { name: "API docs" });
      await expect(docs).toHaveAttribute("href", "/docs");
      await expect(docs).not.toHaveAttribute("aria-current");

      await page.keyboard.press("Escape");
      await expect(sheet).toHaveCount(0);
      await expect(button).toBeFocused();
    });
  }
});

// The fonts join "ft", "fi" and "fl" in one glyph that looks bold, so no page uses common ligatures.
test("the panel and the docs page do not use common ligatures", async ({ page }) => {
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(page).toHaveURL(/\/$/);
  const ligatures = () => page.evaluate(() => getComputedStyle(document.body).fontVariantLigatures);
  expect(await ligatures()).toBe("no-common-ligatures");
  await openDocs(page);
  expect(await ligatures()).toBe("no-common-ligatures");
});

// An owner reads how to send a message, and the page cannot send a request.
test("an owner reads sendMessage, sees no control that sends, and goes back", async ({
  page,
  baseURL,
}) => {
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(page).toHaveURL(/\/$/);
  await openDocs(page);
  await expect(page.getByRole("img", { name: "Calyx" })).toBeVisible();

  const bar = await sidebar(page);
  await expect(bar.getByRole("link", { name: "Messages", exact: true })).toBeVisible();
  await bar.getByRole("button", { name: "Open Group - Messages" }).click();
  await bar.getByRole("link", { name: /^Send one text message\./ }).click();
  await expect(
    scalar(page).getByRole("heading", { name: "Send one text message.", exact: true }),
  ).toBeVisible();

  // The example request has the address of Calyx, not only the path.
  await expect(scalar(page)).toContainText(`curl ${new URL(baseURL as string).origin}/api/v1/`);

  // No control that sends a request, opens a client or downloads the document. A local
  // address makes Scalar show a toolbar and "Ask AI" unless the settings of the page turn them off.
  for (const name of [
    /Test Request/i,
    /API Client/i,
    /Download/i,
    /Developer Tools/i,
    /Generate SDK/i,
    /Deploy API/i,
    /Ask AI/i,
  ]) {
    await expect(page.getByRole("button", { name })).toHaveCount(0);
    await expect(page.getByRole("link", { name })).toHaveCount(0);
    await expect(page.getByText(name)).toHaveCount(0);
  }

  await page.getByRole("link", { name: "Back to Calyx" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
});

// A client user sees only the operations that the role can call.
test("a client user sees no group Clients and no group Organization", async ({ page }) => {
  const { owner, client } = await seedBasic();
  await login(page, owner);
  await expect(page).toHaveURL(/\/$/);
  await openDocs(page);
  const ownerBar = await sidebar(page);
  await expect.poll(() => groupNames(ownerBar)).toEqual(groups.owner);

  await page.context().clearCookies();
  await login(page, client);
  await expect(page).toHaveURL(/\/$/);
  await openDocs(page);
  const clientBar = await sidebar(page);
  await expect.poll(() => groupNames(clientBar)).toEqual(groups.client);
});

// A person with no login opens /docs from a bookmark.
test("/docs with no login goes to the login and back, and Back skips the login", async ({
  page,
}) => {
  const { owner } = await seedBasic();
  await page.goto("/docs");
  await expect(page).toHaveURL(/\/login\?next=%2Fdocs$/);
  await page.getByLabel("Email").fill(owner.email);
  await page.getByLabel("Password").fill(owner.password);
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page).toHaveURL(/\/docs$/);
  await expect(scalar(page).getByRole("heading", { name: "Start here" })).toBeVisible();

  await page.goBack();
  await expect(page).not.toHaveURL(/\/login/);
});

// The address box gives the two addresses to copy.
test("the copy buttons copy the OpenAPI and the MCP addresses", async ({
  page,
  context,
  baseURL,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const origin = new URL(baseURL as string).origin;
  const { owner } = await seedBasic();
  await login(page, owner);
  await openDocs(page);

  await expect(
    page.getByText(
      "The file /openapi.json has every operation of the API. This page shows only the operations that your role can call.",
    ),
  ).toBeVisible();
  await expect(
    page.getByText("If the address answers 404, the server admin turned MCP off."),
  ).toBeVisible();

  await page.getByRole("button", { name: "Copy the OpenAPI address" }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(`${origin}/openapi.json`);
  await page.getByRole("button", { name: "Copy the MCP address" }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(`${origin}/mcp`);
});

// The section "Ways to connect to Calyx".
test("the section shows the three items, the contract version and the people", async ({
  page,
  baseURL,
}) => {
  const origin = new URL(baseURL as string).origin;
  const { owner } = await seedBasic();
  await login(page, owner);
  await openDocs(page);

  const section = page.getByRole("region", { name: "Ways to connect to Calyx" });
  await expect(section).toBeVisible();
  const http = section.getByRole("region", { name: "HTTP API" });
  await expect(http).toContainText(`${origin}/api/v1`);
  await expect(http.getByText("Official SDKs:", { exact: true })).toBeVisible();
  const both = "Backend and core client";
  for (const [language, name, kind] of [
    ["TypeScript", "@calyx/client-sdk", both],
    ["Go", "github.com/csmarckitus/calyx/packages/client-sdk/go", both],
    ["PHP", "calyx/client-sdk", "Backend client"],
    ["PHP", "calyx/client-sdk-core", "Core client"],
    ["Python", "calyx-client-sdk", "Backend client"],
    ["Python", "calyx-client-sdk-core", "Core client"],
    ["VB6", "packages/client-sdk/vb6", "Backend client"],
  ]) {
    const item = http.getByRole("listitem").filter({ has: page.getByText(name, { exact: true }) });
    await expect(item).toHaveCount(1);
    await expect(item).toHaveText(`${language} ${name} ${kind}`);
  }
  const openapi = section.getByRole("region", { name: "OpenAPI document" });
  await expect(openapi).toContainText(`${origin}/openapi.json`);
  const mcp = section.getByRole("region", { name: "MCP server" });
  await expect(mcp).toContainText(`${origin}/mcp`);
  await expect(mcp).toContainText("If the address answers 404, the server admin turned MCP off.");
  await expect(section.getByText("API contract: v1", { exact: true })).toBeVisible();
  await expect(section.getByText("Running backend: dev", { exact: true })).toBeVisible();

  const terms = section.getByRole("term");
  await expect(terms).toHaveText([
    "Server admin (operator)",
    "Organization admin (owner)",
    "Client user",
  ]);
  await expect(section.getByRole("definition")).toHaveCount(3);

  // From sm the three items sit in one row; at the phone width they stack.
  const boxes = await Promise.all([http, openapi, mcp].map((i) => i.boundingBox()));
  const [a, b, c] = boxes as { x: number; y: number }[];
  if ((page.viewportSize()?.width ?? 0) >= 640) {
    expect(Math.abs(a.y - b.y)).toBeLessThan(2);
    expect(Math.abs(b.y - c.y)).toBeLessThan(2);
  } else {
    expect(b.y).toBeGreaterThan(a.y);
    expect(c.y).toBeGreaterThan(b.y);
  }
});

// The example languages.
test("the language selector offers five languages, and the sentence shows", async ({ page }) => {
  const { owner } = await seedBasic();
  await login(page, owner);
  await openDocs(page);

  await expect(
    page.getByText("The language selector changes the language of every example on this page.", {
      exact: true,
    }),
  ).toBeVisible();

  const bar = await sidebar(page);
  await bar.getByRole("button", { name: "Open Group - Messages" }).click();
  await bar.getByRole("link", { name: /^Send one text message\./ }).click();

  // Scalar gives the popover panel no accessible name, so the panel is the one that its button opens.
  // The button shows only some languages as tabs, and the panel lists all of them, each with its client.
  await page
    .getByRole("button", { name: /Select from all clients/ })
    .first()
    .click();
  const panel = page.locator('[id^="headlessui-popover-panel"]').first();
  await expect(panel).toBeVisible();
  await expect(panel).toHaveText("GoNewRequestNode.jsFetchPHPGuzzlePythonRequestsShellCurl");
});

test("the header starts with Back to Calyx, and a link moves to the start of the reference", async ({
  page,
}) => {
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(page).toHaveURL(/\/$/);
  await openDocs(page);
  const back = page.getByRole("link", { name: "Back to Calyx" });
  const header = page.getByRole("banner");
  await expect(back.locator("svg")).toHaveAttribute("aria-hidden", "true");
  const backBox = await back.boundingBox();
  const logoBox = await header.locator('img[src="/calyx-logo.svg"]').boundingBox();
  expect((backBox?.x ?? 1e9) < (logoBox?.x ?? 0)).toBe(true);

  const jump = page.getByRole("button", { name: "Go to the API reference" });
  // It is on the first screen, before any scroll.
  const jumpBox = await jump.boundingBox();
  const view = page.viewportSize();
  expect(jumpBox?.y ?? 1e9).toBeGreaterThanOrEqual(0);
  expect((jumpBox?.y ?? 1e9) + (jumpBox?.height ?? 0)).toBeLessThanOrEqual(view?.height ?? 0);
  await expect(jump.locator("svg")).toHaveAttribute("aria-hidden", "true");
  await jump.click();
  const target = page.locator('[data-slot="reference"]');
  const top = await target.evaluate((el) => el.getBoundingClientRect().top);
  const height = page.viewportSize()?.height ?? 0;
  expect(top).toBeGreaterThanOrEqual(-1);
  expect(top).toBeLessThan(height / 2);
  await expect(target).toBeFocused();
  // The address has no hash, and Scalar makes its normal hashes.
  expect(new URL(page.url()).hash).toBe("");
  await (await sidebar(page)).getByRole("link", { name: "Account", exact: true }).first().click();
  await expect(page).toHaveURL(/#tag\/account/);
});

// The theme button of the page also sets the theme of Scalar.
test("the theme button sets the dark state of Scalar", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  const { owner } = await seedBasic();
  await login(page, owner);
  await openDocs(page);
  const root = page.locator("body");
  await expect(root).toHaveClass(/\blight-mode\b/);

  await page.getByRole("button", { name: "Use the dark theme" }).click();
  await expect(root).toHaveClass(/\bdark-mode\b/);
  await page.getByRole("button", { name: "Use the light theme" }).click();
  await expect(root).toHaveClass(/\blight-mode\b/);
});

// The page asks the backend for three things only. Its own files do not count.
test("the page makes only the requests GET /api/v1/me, GET /openapi.json and GET /healthz", async ({
  page,
}) => {
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(page).toHaveURL(/\/$/);
  // Leave the panel first: it polls /healthz and reads lists, and those requests are not the page's.
  await page.goto("about:blank");
  const requests: string[] = [];
  page.on("request", (r) => {
    const path = new URL(r.url()).pathname;
    if (/^\/(assets\/|docs$|theme\.js$|calyx-logo\.svg$)/.test(path)) return;
    requests.push(`${r.method()} ${path}`);
  });
  await page.goto("/docs");
  await expect(scalar(page).getByRole("heading", { name: "Start here" })).toBeVisible();
  await page.goto("/docs#tag/messages/POST/api/v1/instances/{id}/messages");
  await expect(scalar(page).getByRole("heading", { name: "Send one text message." })).toBeVisible();
  await page.waitForLoadState("networkidle");
  expect([...requests].sort()).toEqual(["GET /api/v1/me", "GET /healthz", "GET /openapi.json"]);
});

// The answer of /docs has the CSP of headers-docs.conf, with the style exception for Scalar.
test("the CSP of the /docs answer is the CSP of headers-docs.conf", async ({ page }) => {
  const { owner } = await seedBasic();
  await login(page, owner);
  const answer = await page.goto("/docs");
  expect(answer?.headers()["content-security-policy"]).toBe(docsHeaders["Content-Security-Policy"]);
});

// A failed read of the document shows the notice and "Try again".
test.describe("a failed read of the document", () => {
  test.use({ tolerateFailedRequests: true });

  test("shows the notice, and Try again reads it again", async ({ page }) => {
    const { owner } = await seedBasic();
    await login(page, owner);
    await expect(page).toHaveURL(/\/$/);
    let reads = 0;
    await page.route("**/openapi.json", (route) => {
      reads++;
      return route.fulfill({ status: 503, contentType: "text/plain", body: "Service Unavailable" });
    });
    await page.goto("/docs");
    const notice = page.getByRole("alert").filter({
      hasText: "The page cannot read the API document.",
    });
    await expect(notice).toBeVisible();
    // retryRead retries one time.
    expect(reads).toBe(2);
    // The link to the reference does nothing without the reference, so it does not show.
    await expect(page.getByRole("button", { name: "Go to the API reference" })).toHaveCount(0);

    await page.unroute("**/openapi.json");
    await page.getByRole("button", { name: "Try again" }).click();
    await expect(scalar(page).getByRole("heading", { name: "Start here" })).toBeVisible();
    await expect(notice).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Go to the API reference" })).toBeVisible();
  });
});
