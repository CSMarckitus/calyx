import type { Page, Request } from "@playwright/test";
import {
  closeMenu,
  control,
  expect,
  healthButton,
  login,
  mainLink,
  type Person,
  test,
  themeButton,
} from "./support.ts";

// The reliability of the page: a double click, two tabs with one instance, and the
// translator of the browser. A refused change and a cancelled pairing are failed requests in the log.
test.use({ tolerateFailedRequests: true });

type Seeded = {
  clients: { name: string; id: string }[];
  users: (Person & { id: string; invite_path?: string; reset_path?: string })[];
  instances: { name: string; id: string; path: string }[];
};

async function seed(body: Record<string, unknown>): Promise<Seeded> {
  const { status, body: answer } = await control<Seeded>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    clients: [{ name: "Kiosk", can_create_instances: true }],
    users: [
      { label: "owner", role: "owner" },
      { label: "shopper", role: "client", client: "Kiosk" },
    ],
    ...body,
  });
  expect(status).toBe(200);
  return answer;
}

const person = (s: Seeded, label: string) => s.users.find((u) => u.label === label) as Person;
const userOf = (s: Seeded, label: string) => s.users.find((u) => u.label === label);
const idOf = (s: Seeded, name: string) => s.instances.find((i) => i.name === name)?.id as string;

// The drawer, and not the card of services, which is a dialog too.
const drawer = (page: Page) => page.locator('[role="dialog"][data-slot="sheet-content"]');
// A modal drawer hides the list from the accessibility tree, so the cell has a CSS locator.
const cell = (page: Page, name: string) =>
  page
    .locator("main")
    .locator('[data-slot="instance"]', { has: page.getByText(name, { exact: true }) });
const card = (page: Page) => drawer(page).locator('[data-slot="instance"]');
const question = (page: Page, name: string) =>
  page.getByRole("alertdialog", { name }).or(page.getByRole("dialog", { name }));

// Counts the requests that change data: every method but GET, except the two lookups.
function changes(page: Page) {
  const sent: string[] = [];
  page.on("request", (r: Request) => {
    const path = new URL(r.url()).pathname;
    if (r.method() === "GET" || !path.startsWith("/api/") || path.endsWith("/lookup")) return;
    sent.push(`${r.method()} ${path}`);
  });
  return {
    // Waits a moment for a late second request, then gives the count of one kind.
    count: async (method: string, path: string | RegExp) => {
      await page.waitForTimeout(700);
      return sent.filter((s) => {
        const [m, p] = s.split(" ") as [string, string];
        return m === method && (typeof path === "string" ? p === path : path.test(p));
      }).length;
    },
  };
}

test.describe("1. a double click sends one request", () => {
  test("log in, forgot password, reset and invitation", async ({ page }) => {
    const s = await seed({
      clients: [{ name: "Kiosk" }, { name: "Bakery" }],
      users: [
        { label: "owner", role: "owner", reset: true },
        { label: "shopper", role: "client", client: "Kiosk" },
        { label: "invitee", role: "client", client: "Bakery", pending: true },
      ],
    });
    const sent = changes(page);

    await page.goto("/login");
    await page.getByLabel("Email").fill(person(s, "shopper").email);
    await page.getByLabel("Password").fill(person(s, "shopper").password);
    await page.getByRole("button", { name: "Log in" }).dblclick();
    await expect(page).toHaveURL(/\/$/);
    expect(await sent.count("POST", "/api/v1/auth/login")).toBe(1);
    await page.context().clearCookies();

    // A new link replaces the seeded one, so the reset comes before "Send the link".
    await page.goto(userOf(s, "owner")?.reset_path ?? "");
    await page.getByLabel("New password").fill("a-new-long-password");
    await page.getByRole("button", { name: "Set the password" }).dblclick();
    await expect(page).toHaveURL(/\/login/);
    expect(await sent.count("POST", "/api/v1/auth/password-reset/confirm")).toBe(1);

    await page.goto("/forgot-password");
    await page.getByLabel("Email").fill(person(s, "owner").email);
    await page.getByRole("button", { name: "Send the link" }).dblclick();
    await expect(page.getByText("Check your mail.")).toBeVisible();
    expect(await sent.count("POST", "/api/v1/auth/password-reset")).toBe(1);

    await page.goto(userOf(s, "invitee")?.invite_path ?? "");
    await page.getByLabel("Choose a password").fill("a-good-long-password");
    await page.getByRole("button", { name: "Set password and continue" }).dblclick();
    await expect(page).toHaveURL(/\/$/);
    expect(await sent.count("POST", "/api/v1/invitations/accept")).toBe(1);
  });

  test("the terms and the settings", async ({ page }) => {
    const s = await seed({ organization: { name: "Acme", acknowledged: false } });
    await login(page, person(s, "owner"));
    await expect(page.locator('[data-slot="terms-notice"]')).toBeVisible();
    await expect(page.locator('[data-slot="terms-banner"]')).toHaveCount(0);
    const sent = changes(page);

    await page.goto("/organization/whatsapp-web");
    await page.getByRole("checkbox").check();
    await page.getByRole("button", { name: "Accept" }).dblclick();
    await expect(page).toHaveURL(/\/$/);
    expect(await sent.count("POST", "/api/v1/organization/whatsapp-web-acknowledgement")).toBe(1);

    await page.goto("/organization");

    await page.getByRole("textbox", { name: "Name" }).fill("Acme Ltd");
    await page.getByRole("button", { name: "Save" }).dblclick();
    await expect(page.getByRole("textbox", { name: "Name" })).toHaveValue("Acme Ltd");
    expect(await sent.count("PATCH", "/api/v1/organization")).toBe(1);

    await page.goto("/account");
    await page.getByLabel("Current password").fill(person(s, "owner").password);
    await page.getByLabel("New password").fill("another-long-password");
    await page.getByRole("button", { name: "Change password" }).dblclick();
    await expect(page.getByText("Your password is changed.")).toBeVisible();
    expect(await sent.count("PUT", "/api/v1/me/password")).toBe(1);

    await page.getByRole("button", { name: "Log out" }).dblclick();
    await expect(page).toHaveURL(/\/login/);
    expect(await sent.count("POST", "/api/v1/auth/logout")).toBe(1);
  });

  test("rename, Calyx access and remove", async ({ page }) => {
    const s = await seed({
      instances: [{ name: "Main", client: "Kiosk", provider: "WHATSAPP_WEB", status: "CONNECTED" }],
    });
    await login(page, person(s, "owner"));
    const path = `/api/v1/instances/${idOf(s, "Main")}`;
    await cell(page, "Main").getByRole("link").click();
    await expect(card(page).getByRole("status")).toHaveText("Connected");
    const sent = changes(page);

    await card(page).getByRole("button", { name: "Rename" }).click();
    await card(page).getByRole("textbox", { name: "Name" }).fill("Front desk");
    await card(page).getByRole("button", { name: "Save" }).dblclick();
    await expect(card(page).getByRole("heading", { name: "Front desk" })).toBeVisible();
    expect(await sent.count("PATCH", path)).toBe(1);

    const access = card(page).getByRole("switch", { name: "Calyx access of Front desk" });
    await access.click();
    await question(page, "Turn off Calyx access?")
      .getByRole("button", { name: "Turn off" })
      .dblclick();
    await expect(access).not.toBeChecked();
    expect(await sent.count("PATCH", path)).toBe(2);

    await card(page).getByRole("button", { name: "Turn on Calyx access" }).dblclick();
    await expect(access).toBeChecked();
    expect(await sent.count("PATCH", path)).toBe(3);

    await access.click();
    await question(page, "Turn off Calyx access?")
      .getByRole("button", { name: "Turn off" })
      .click();
    await expect(access).not.toBeChecked();
    expect(await sent.count("PATCH", path)).toBe(4);
    await access.dblclick();
    await expect(access).toBeChecked();
    expect(await sent.count("PATCH", path)).toBe(5);

    await card(page).getByRole("button", { name: "Remove this instance" }).click();
    await question(page, "Remove Front desk?").getByRole("button", { name: "Remove" }).dblclick();
    await expect(cell(page, "Front desk")).toHaveCount(0);
    expect(await sent.count("DELETE", path)).toBe(1);
  });

  test("the start of a pairing, its cancel, and a reconnection", async ({ page }) => {
    const s = await seed({
      instances: [{ name: "Old", client: "Kiosk", provider: "WHATSAPP_WEB", status: "NEEDS_AUTH" }],
    });
    await login(page, person(s, "owner"));
    const sent = changes(page);

    await page
      .locator("main")
      .getByRole("region", { name: "Kiosk", exact: true })
      .getByRole("button", { name: "+ Connect a number" })
      .click();
    await drawer(page).getByRole("textbox", { name: "Name" }).fill("Front desk");
    await drawer(page).getByRole("button", { name: "Continue", exact: true }).click();
    await drawer(page).getByRole("button", { name: "Continue", exact: true }).click();
    await drawer(page).getByRole("button", { name: "Continue with automatic" }).click();
    await drawer(page)
      .getByRole("button", { name: /^No, on another phone/ })
      .click();
    await control("POST", "/pairing", {
      steps: [{ step: "update", qr: "2@first" }, { step: "wait" }],
    });
    await drawer(page).getByRole("button", { name: "Show the QR code" }).dblclick();
    await expect(drawer(page).getByRole("img", { name: "QR code" })).toBeVisible();
    expect(await sent.count("POST", "/api/v1/instances")).toBe(1);

    await drawer(page).getByRole("button", { name: "Cancel", exact: true }).dblclick();
    await expect(page).toHaveURL(/\/$/);
    expect(await sent.count("DELETE", /\/pairing$/)).toBe(1);

    await cell(page, "Old").getByRole("button", { name: "Reconnect" }).click();
    await drawer(page)
      .getByRole("button", { name: /^No, on another phone/ })
      .click();
    await control("POST", "/pairing", {
      steps: [{ step: "update", qr: "2@again" }, { step: "wait" }],
    });
    await drawer(page).getByRole("button", { name: "Show the QR code" }).dblclick();
    await expect(drawer(page).getByRole("img", { name: "QR code" })).toBeVisible();
    expect(await sent.count("POST", /\/pair$/)).toBe(1);
  });
});

test("1. a double click sends one request: the buttons of the clients", async ({ page }) => {
  const s = await seed({ clients: [], users: [{ label: "owner", role: "owner" }] });
  await login(page, person(s, "owner"));
  const main = page.getByRole("main");
  await expect(main.getByRole("heading", { name: "Instances" })).toBeVisible();
  const sent = changes(page);

  await main.getByRole("button", { name: "Add a client" }).click();
  const ask = page.getByRole("dialog", { name: "Add a client" });
  await ask.getByLabel("Name").fill("Bakery");
  await ask.getByRole("button", { name: "Add client" }).dblclick();
  await expect(main.getByRole("heading", { name: "Bakery", level: 1 })).toBeVisible();
  expect(await sent.count("POST", "/api/v1/clients")).toBe(1);

  const access = main.getByRole("region", { name: "Access" });
  await access.getByLabel("Email").fill("ana@bakery.example");
  await access.getByRole("button", { name: "Send invitation" }).dblclick();
  await expect(access).toContainText("Invitation sent to");
  expect(await sent.count("POST", /\/invitations$/)).toBe(1);
  await access.getByRole("button", { name: "Send again" }).dblclick();
  expect(await sent.count("POST", /\/invitations$/)).toBe(2);
  await access.getByRole("button", { name: "Cancel invitation" }).dblclick();
  await expect(access).toContainText("No invitation is open.");
  expect(await sent.count("DELETE", /\/invitations$/)).toBe(1);

  const limits = main.getByRole("region", { name: "Limits" });
  await limits.getByRole("button", { name: "Save limits" }).dblclick();
  await expect(limits).toContainText("The limits are saved.");
  expect(await sent.count("PATCH", /^\/api\/v1\/clients\/[^/]+$/)).toBe(1);

  await main.getByRole("button", { name: "Delete this client" }).click();
  await page
    .getByRole("dialog", { name: "Delete Bakery?" })
    .getByRole("button", { name: "Delete" })
    .dblclick();
  await expect(page).toHaveURL(/\/$/);
  expect(await sent.count("DELETE", /^\/api\/v1\/clients\/[^/]+$/)).toBe(1);
});

test("1. a double click sends one request: create and revoke an API key", async ({ page }) => {
  const s = await seed({ clients: [], users: [{ label: "owner", role: "owner" }] });
  await login(page, person(s, "owner"));
  await expect(page.getByRole("heading", { name: "Instances" })).toBeVisible();
  await page.goto("/developers/api-keys");
  await expect(page.getByRole("heading", { name: "API keys", level: 1 })).toBeVisible();
  const sent = changes(page);

  await page.getByRole("button", { name: "Create API key" }).click();
  await page.getByLabel("Name", { exact: true }).fill("Assistant");
  await page.getByRole("button", { name: "Save", exact: true }).dblclick();
  const newKey = page.getByRole("dialog", { name: "Your new API key" });
  await expect(newKey).toBeVisible();
  expect(await sent.count("POST", "/api/v1/api-keys")).toBe(1);
  await newKey.getByRole("button", { name: "I saved it" }).click();
  await expect(newKey).toBeHidden();

  await page.getByRole("button", { name: "Revoke" }).click();
  await page
    .getByRole("dialog", { name: /^Revoke/ })
    .getByRole("button", { name: "Revoke" })
    .dblclick();
  await expect(page.getByText("You have no API keys.")).toBeVisible();
  expect(await sent.count("DELETE", /^\/api\/v1\/api-keys\/[^/]+$/)).toBe(1);
});

// Two separate presses: the second one comes after the first request started. The state
// "pending" must stop it, also on the buttons of a question that closes.
test("1. Enter pressed twice sends one request: a rename, Turn off and Remove", async ({
  page,
}) => {
  const s = await seed({
    instances: [{ name: "Main", client: "Kiosk", provider: "WHATSAPP_WEB", status: "CONNECTED" }],
  });
  await login(page, person(s, "owner"));
  const path = `/api/v1/instances/${idOf(s, "Main")}`;
  await cell(page, "Main").getByRole("link").click();
  await expect(card(page).getByRole("status")).toHaveText("Connected");
  const sent = changes(page);

  await card(page).getByRole("button", { name: "Rename" }).click();
  await card(page).getByRole("textbox", { name: "Name" }).fill("Front desk");
  await page.keyboard.press("Enter");
  await page.keyboard.press("Enter");
  await expect(card(page).getByRole("heading", { name: "Front desk" })).toBeVisible();
  expect(await sent.count("PATCH", path)).toBe(1);

  const access = card(page).getByRole("switch", { name: "Calyx access of Front desk" });
  await access.click();
  await question(page, "Turn off Calyx access?").getByRole("button", { name: "Turn off" }).focus();
  await page.keyboard.press("Enter");
  await page.keyboard.press("Enter");
  await expect(access).not.toBeChecked();
  expect(await sent.count("PATCH", path)).toBe(2);
  await expect(card(page).locator('[data-slot="failure"]')).toHaveCount(0);

  await card(page).getByRole("button", { name: "Remove this instance" }).click();
  await question(page, "Remove Front desk?").getByRole("button", { name: "Remove" }).focus();
  await page.keyboard.press("Enter");
  await page.keyboard.press("Enter");
  await expect(cell(page, "Front desk")).toHaveCount(0);
  expect(await sent.count("DELETE", path)).toBe(1);
});

// The guard of a double click is only on the buttons that send a change: a toggle takes both clicks.
test("1. a double click on the theme button changes the theme two times", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  const s = await seed({});
  await login(page, person(s, "owner"));
  await (await themeButton(page, "Use the dark theme")).dblclick();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(await themeButton(page, "Use the dark theme")).toBeVisible();
});

test("2. two tabs change one instance: the second shows the conflict, and then the new state", async ({
  page,
  context,
}) => {
  const s = await seed({
    instances: [{ name: "Main", client: "Kiosk", provider: "WHATSAPP_WEB", status: "CONNECTED" }],
  });
  await login(page, person(s, "owner"));
  const other = await context.newPage();
  await other.goto(`/instances/${idOf(s, "Main")}`);
  await page.goto(`/instances/${idOf(s, "Main")}`);
  for (const p of [page, other]) {
    await expect(card(p).getByRole("status")).toHaveText("Connected");
    await expect(card(p).getByRole("combobox", { name: "Use" })).toContainText("Send and receive");
  }

  // The first change waits 2 s in the core, so the second one comes while it runs.
  await control("POST", "/fault", { method: "UpdateInstanceConfig", delay_ms: 2000 });
  const first = page.waitForResponse(
    (r) => r.request().method() === "PATCH" && r.url().endsWith(idOf(s, "Main")),
  );
  await card(page).getByRole("combobox", { name: "Use" }).click();
  await page.getByRole("option", { name: "Only send" }).click();

  const access = card(other).getByRole("switch", { name: "Calyx access of Main" });
  await access.click();
  const conflict = other.waitForResponse(
    (r) => r.request().method() === "PATCH" && r.url().endsWith(idOf(s, "Main")),
  );
  await question(other, "Turn off Calyx access?").getByRole("button", { name: "Turn off" }).click();
  expect((await conflict).status()).toBe(409);
  const failure = card(other).locator('[data-slot="failure"]');
  await expect(failure).toContainText("The number changed, or another change of it runs.");
  await expect(failure).toContainText("Look at its state now and try again.");
  expect((await first).status()).toBe(200);

  // The second tab reads again and shows the change of the first one, with no action.
  await expect(card(other).getByRole("combobox", { name: "Use" })).toContainText("Only send", {
    timeout: 5000,
  });
  await expect(access).toBeChecked();
  await other.close();
});

// A translator replaces each text node with its own element, as Google Translate does with <font>.
// It leaves alone what has translate="no". React then writes to a text node that is no longer in
// the page, so the page shows an old text: the page records each such write as "stale".
function translator() {
  type W = Window & { stale: string[]; translated: () => string[] };
  const w = window as unknown as W;
  w.stale = [];
  const replaced = new WeakSet<Node>();
  const desc = Object.getOwnPropertyDescriptor(Node.prototype, "nodeValue") as PropertyDescriptor;
  Object.defineProperty(Node.prototype, "nodeValue", {
    configurable: true,
    get: desc.get,
    set(this: Node, v: string) {
      if (replaced.has(this)) w.stale.push(String(v));
      desc.set?.call(this, v);
    },
  });
  const skip = new Set(["SCRIPT", "STYLE", "TEXTAREA", "OPTION", "FONT"]);
  const translate = (root: Node) => {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const found: Text[] = [];
    for (let n = walker.nextNode(); n; n = walker.nextNode()) found.push(n as Text);
    for (const t of found) {
      const parent = t.parentElement;
      if (!parent || !t.data.trim() || skip.has(parent.tagName)) continue;
      if (parent.closest('[translate="no"]')) continue;
      const font = document.createElement("font");
      font.className = "translated";
      font.textContent = t.data;
      parent.replaceChild(font, t);
      replaced.add(t);
    }
  };
  // The texts that the translator changed.
  w.translated = () =>
    [...document.querySelectorAll("font.translated")].map((f) => f.textContent ?? "");
  const start = () => {
    translate(document.body);
    new MutationObserver((records) => {
      for (const r of records) for (const n of r.addedNodes) if (n.isConnected) translate(n);
      for (const r of records)
        if (r.type === "characterData" && r.target.isConnected) translate(r.target);
    }).observe(document.body, { childList: true, subtree: true, characterData: true });
  };
  if (document.body) start();
  else document.addEventListener("DOMContentLoaded", start);
}

async function stale(page: Page): Promise<string[]> {
  return page.evaluate(() => (window as unknown as { stale: string[] }).stale);
}

// The names, the emails and the word "Calyx" stay as they are.
async function expectKept(page: Page, words: string[]) {
  const translated = await page.evaluate(() =>
    (window as unknown as { translated: () => string[] }).translated(),
  );
  const found = translated.filter((t) => words.some((w) => t.includes(w)));
  expect(found).toEqual([]);
}

test.describe("3. the translator of the browser", () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(translator);
  });

  test("an owner: the list and the drawer change state with no error", async ({ page }) => {
    const s = await seed({
      instances: [
        { name: "Main", client: "Kiosk", provider: "WHATSAPP_WEB", status: "CONNECTED" },
        {
          name: "Off line",
          client: "Kiosk",
          provider: "WHATSAPP_WEB",
          status: "CONNECTED",
          enabled: false,
        },
      ],
    });
    const owner = person(s, "owner");
    await login(page, owner);
    await expect(cell(page, "Main")).toContainText("Connected");
    const words = ["Calyx", "Acme", "Kiosk", "Main", "Off line", owner.email];
    await expectKept(page, words);

    await cell(page, "Main").getByRole("link").click();
    await expect(card(page).getByRole("status")).toHaveText("Connected");
    await card(page)
      .getByRole("button", { name: "What are the states of the connection?" })
      .click();
    await page.keyboard.press("Escape");
    await expectKept(page, words);

    await control("POST", "/status", { instance: idOf(s, "Main"), status: "NEEDS_AUTH" });
    await expect(card(page).getByRole("status")).toHaveText("Disconnected");
    await expect(cell(page, "Main")).toContainText("The phone must be linked again.");
    await control("POST", "/status", { instance: idOf(s, "Main"), status: "DISCONNECTED" });
    await expect(card(page).getByRole("status")).toHaveText("Reconnecting");
    await control("POST", "/status", { instance: idOf(s, "Main"), status: "CONNECTED" });
    await expect(card(page).getByRole("status")).toHaveText("Connected");
    await expect(cell(page, "Main")).not.toContainText("Calyx is trying again.");

    // A refused change shows its text, with the name of the Manager.
    await control("POST", "/fault", { method: "UpdateInstanceConfig", code: "UNAVAILABLE" });
    await card(page).getByRole("switch").click();
    await question(page, "Turn off Calyx access?")
      .getByRole("button", { name: "Turn off" })
      .click();
    await expect(card(page).locator('[data-slot="failure"]')).toBeVisible();
    await expectKept(page, words);
    await control("POST", "/fault", {});

    // The header of the health status counts the seconds.
    await page.waitForTimeout(2500);
    await page.getByRole("button", { name: "Close" }).first().click();
    await expect(page.locator("body")).not.toContainText("This page stopped");
    expect(await stale(page)).toEqual([]);
  });

  test("a client user: its screen, the settings and the connection", async ({ page }) => {
    const s = await seed({
      instances: [{ name: "Main", client: "Kiosk", provider: "WHATSAPP_WEB", status: "CONNECTED" }],
    });
    const shopper = person(s, "shopper");
    await login(page, shopper);
    const screen = page.locator('[data-slot="instance"]', { hasText: "Main" });
    await expect(screen.getByRole("status")).toHaveText("Connected");
    const words = ["Calyx", "Acme", "Kiosk", "Main", shopper.email];
    await expectKept(page, words);

    await control("POST", "/status", { instance: idOf(s, "Main"), status: "NEEDS_AUTH" });
    await expect(screen.getByRole("status")).toHaveText("Disconnected");
    await control("POST", "/status", { instance: idOf(s, "Main"), status: "CONNECTED" });
    await expect(screen.getByRole("status")).toHaveText("Connected");

    // The health card, with its rows and the seconds.
    await (await healthButton(page, /^Calyx is fully operational/)).focus();
    await expect(page.locator('[data-slot="health-card"]')).toBeVisible();
    await page.waitForTimeout(2500);
    await expectKept(page, words);
    await page.keyboard.press("Escape");
    await closeMenu(page);

    await page.getByRole("button", { name: "Connect another number" }).click();
    await expect(drawer(page)).toBeVisible();
    await expectKept(page, words);
    await drawer(page).getByRole("textbox", { name: "Name" }).fill("Second");
    await drawer(page).getByRole("button", { name: "Continue", exact: true }).click();
    await drawer(page).getByRole("button", { name: "Continue", exact: true }).click();
    await drawer(page).getByRole("button", { name: "Continue with automatic" }).click();
    await drawer(page)
      .getByRole("button", { name: /^No, on another phone/ })
      .click();
    await control("POST", "/pairing", {
      steps: [{ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" }],
    });
    await drawer(page).getByRole("button", { name: "Show the QR code" }).click();
    await expect(drawer(page).getByRole("img", { name: "QR code" })).toBeVisible();
    await control("POST", "/pairing/next");
    await expect(drawer(page)).toContainText("is connected");
    await expectKept(page, [...words, "Second"]);
    await drawer(page).getByRole("button", { name: "Done" }).click();
    // With two numbers, Done shows the card of the new one in the drawer.
    await drawer(page).getByRole("button", { name: "Close" }).click();

    await (await mainLink(page, "Account")).click();
    await expect(page.getByRole("heading", { name: "Account", level: 1 })).toBeVisible();
    await expectKept(page, words);
    await expect(page.locator("body")).not.toContainText("This page stopped");
    expect(await stale(page)).toEqual([]);
  });

  test("an owner: the page of a client changes with no error", async ({ page }) => {
    const s = await seed({
      clients: [{ name: "Bakery" }],
      users: [
        { label: "owner", role: "owner" },
        { label: "invitee", role: "client", client: "Bakery", pending: true },
      ],
    });
    const owner = person(s, "owner");
    const invitee = person(s, "invitee");
    await login(page, owner);
    await expect(page.getByRole("heading", { name: "Instances" })).toBeVisible();
    const bakery = s.clients.find((c) => c.name === "Bakery")?.id as string;
    await page.goto(`/clients/${bakery}`);
    const access = page.getByRole("main").getByRole("region", { name: "Access" });
    await expect(access).toContainText(invitee.email);
    const words = ["Calyx", "Acme", "Bakery", owner.email, invitee.email, "ana@bakery.example"];
    await expectKept(page, words);

    await access.getByRole("button", { name: "Send again" }).click();
    await expect(access.getByText("The mail is sent.")).toBeVisible();
    await access.getByRole("button", { name: "Cancel invitation" }).click();
    await expect(access).toContainText("No invitation is open.");
    await access.getByLabel("Email").fill("ana@bakery.example");
    await access.getByRole("button", { name: "Send invitation" }).click();
    await expect(access).toContainText("Invitation sent to");
    await page.getByRole("main").getByRole("button", { name: "Save limits" }).click();
    await expect(page.getByText("The limits are saved.")).toBeVisible();
    await expectKept(page, words);
    await expect(page.locator("body")).not.toContainText("This page stopped");
    expect(await stale(page)).toEqual([]);
  });

  test("the screens with no login", async ({ page }) => {
    const s = await seed({
      clients: [{ name: "Bakery" }],
      users: [
        { label: "owner", role: "owner", reset: true },
        { label: "invitee", role: "client", client: "Bakery", pending: true },
      ],
    });
    const invitee = userOf(s, "invitee") as Person & { invite_path?: string };
    await page.goto(invitee.invite_path ?? "");
    await expect(page.getByRole("heading", { name: "Welcome" })).toBeVisible();
    await expectKept(page, ["Calyx", "Acme", invitee.email]);

    await page.goto("/login");
    await page.getByLabel("Email").fill(person(s, "owner").email);
    await page.getByLabel("Password").fill("not-the-password");
    await page.getByRole("button", { name: "Log in" }).click();
    await expect(page.locator('[data-slot="failure"]')).toBeVisible();
    await page.getByLabel("Password").fill("still-not-the-password");
    await page.getByRole("button", { name: "Log in" }).click();
    await page.waitForTimeout(500);
    await expectKept(page, ["Calyx"]);

    await page.goto("/forgot-password");
    await page.getByLabel("Email").fill(person(s, "owner").email);
    await page.getByRole("button", { name: "Send the link" }).click();
    await expect(page.getByText("Check your mail.")).toBeVisible();
    await expectKept(page, ["Calyx"]);
    await expect(page.locator("body")).not.toContainText("This page stopped");
    expect(await stale(page)).toEqual([]);
  });
});

// A change with no known result says "Checking the result" while the page reads the data
// again. Then it shows the data. The test holds that read for 3 s.
test("a change with no known result says Checking the result, then shows the data", async ({
  page,
}) => {
  const s = await seed({
    instances: [{ name: "Main", client: "Kiosk", provider: "WHATSAPP_WEB", status: "CONNECTED" }],
  });
  await login(page, person(s, "owner"));
  // Waits for the first screen, so the next `goto` does not cut the login.
  await expect(cell(page, "Main")).toBeVisible();
  await page.goto(`/instances/${idOf(s, "Main")}`);
  const use = card(page).getByRole("combobox", { name: "Use" });
  await expect(use).toContainText("Send and receive");

  await control("POST", "/fault", { method: "UpdateInstanceConfig", code: "UNAVAILABLE" });
  await page.route("**/api/v1/instances", async (route) => {
    await new Promise((r) => setTimeout(r, 3000));
    await route.continue().catch(() => undefined);
  });
  await use.click();
  await page.getByRole("option", { name: "Only send" }).click();

  const failure = card(page).locator('[data-slot="failure"]');
  await expect(failure).toContainText("Calyx did not confirm the change.");
  await expect(failure.getByRole("status")).toHaveText("Checking the result");
  await expect(failure.getByRole("status")).toHaveCount(0, { timeout: 10_000 });
  await expect(use).toContainText("Send and receive");
});

// "Save webhooks" and the button of the question "Only send" with webhooks.
test("1. a double click sends one request: save webhooks, and only send", async ({ page }) => {
  const hooks = [{ event: "message.received", url: "https://n8n.example/hook" }];
  const s = await seed({
    instances: [
      { name: "Main", client: "Kiosk", provider: "WHATSAPP_WEB", status: "CONNECTED" },
      {
        name: "Hooked",
        client: "Kiosk",
        provider: "WHATSAPP_WEB",
        status: "CONNECTED",
        webhooks: hooks,
      },
    ],
  });
  await login(page, person(s, "owner"));
  await expect(cell(page, "Main")).toBeVisible();
  const sent = changes(page);
  const patch = (name: string) => new RegExp(`^/api/v1/instances/${idOf(s, name)}$`);

  await page.goto(`/instances/${idOf(s, "Main")}/webhooks`);
  const view = page.locator('[data-slot="webhooks"]');
  await view.getByLabel("URL", { exact: true }).fill("https://n8n.example/new");
  await view.getByRole("checkbox", { name: "A message arrives", exact: true }).check();
  await view.getByRole("button", { name: "Save webhooks" }).dblclick();
  await expect(view.getByText("Saved.", { exact: true })).toBeVisible();
  expect(await sent.count("PATCH", patch("Main"))).toBe(1);

  await page.goto(`/instances/${idOf(s, "Hooked")}`);
  await card(page).getByRole("combobox", { name: "Use" }).click();
  await page.getByRole("option", { name: "Only send" }).click();
  const ask = page.getByRole("dialog").filter({ hasText: "Calyx stops calling these addresses." });
  await ask.getByRole("button", { name: "Remove the webhooks and only send" }).dblclick();
  await expect(card(page).getByRole("combobox", { name: "Use" })).toContainText("Only send");
  expect(await sent.count("PATCH", patch("Hooked"))).toBe(1);
});

// "Stop sending" and "Remove the user".
test("1. a double click sends one request: Stop sending and Remove the user", async ({ page }) => {
  const s = await seed({
    instances: [
      {
        name: "Main",
        client: "Kiosk",
        provider: "WHATSAPP_WEB",
        status: "CONNECTED",
        bulk_jobs: [
          {
            status: "RUNNING",
            total: 500,
            sent: 120,
            failed: 3,
            progress_percent: 24.6,
            created_ago_seconds: 600,
            created_by: "owner",
          },
        ],
      },
    ],
  });
  const jobId = (s.instances[0] as unknown as { bulk_job_ids: string[] }).bulk_job_ids[0];
  await login(page, person(s, "owner"));
  await expect(cell(page, "Main")).toBeVisible();
  const sent = changes(page);

  await page.goto(`/instances/${idOf(s, "Main")}/bulk-jobs`);
  await page.getByRole("button", { name: "Stop sending" }).click();
  await page
    .getByRole("dialog", { name: "Stop sending?" })
    .getByRole("button", { name: "Stop sending" })
    .dblclick();
  await expect(page.locator('[data-slot="bulk-job"]')).toContainText("Stopped");
  expect(await sent.count("POST", `/api/v1/bulk-jobs/${jobId}/cancel`)).toBe(1);

  const kiosk = s.clients.find((c) => c.name === "Kiosk") as { path?: string };
  await page.goto(kiosk.path ?? "");
  await page.getByRole("button", { name: "Remove the user" }).click();
  const ask = page.getByRole("dialog", { name: "Remove the user?" });
  const confirm = ask.getByRole("button", { name: "Remove the user" });
  await expect(confirm).toBeEnabled();
  await confirm.dblclick();
  await expect(page.getByText("No invitation is open.")).toBeVisible();
  expect(await sent.count("DELETE", /^\/api\/v1\/clients\/[^/]+\/user$/)).toBe(1);
});
