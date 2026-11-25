import AxeBuilder from "@axe-core/playwright";
import type { Locator, Page } from "@playwright/test";
import {
  closeMenu,
  control,
  expect,
  fits,
  healthButton,
  login,
  type Person,
  test,
} from "./support.ts";

// WCAG 2.2 AA. axe checks each screen, a person uses the keyboard alone, and each screen
// fits at a width of 320 px and at 200 % zoom.

// A wrong password is an answer that the browser logs as a failed request.
test.use({ tolerateFailedRequests: true });

type Seeded = {
  clients: { name: string; id: string }[];
  users: (Person & { id: string; invite_path?: string; reset_path?: string })[];
  instances: { name: string; id: string; path: string }[];
};

async function seed(): Promise<Seeded> {
  const { status, body } = await control<Seeded>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    clients: [
      { name: "Shop", can_create_instances: true },
      { name: "Bakery", can_create_instances: true },
      { name: "Empty", can_create_instances: true },
    ],
    users: [
      { label: "owner", role: "owner", reset: true },
      { label: "shopper", role: "client", client: "Shop" },
      { label: "baker", role: "client", client: "Bakery" },
      { label: "invitee", role: "client", client: "Empty", pending: true },
    ],
    instances: [
      { name: "Main", client: "Shop", provider: "WHATSAPP_WEB", status: "CONNECTED" },
      { name: "Lost", provider: "WHATSAPP_WEB", status: "NEEDS_AUTH" },
      { name: "Quiet", provider: "WHATSAPP_WEB", status: "CONNECTED", enabled: false },
      { name: "Meta line", provider: "META_CLOUD", status: "CONNECTED" },
    ],
  });
  expect(status).toBe(200);
  return body;
}

const user = (s: Seeded, label: string) =>
  s.users.find((u) => u.label === label) as Seeded["users"][0];

const drawer = (page: Page) => page.locator('[role="dialog"][data-slot="sheet-content"]');
const cell = (page: Page, name: string) =>
  page
    .locator("main")
    .locator('[data-slot="instance"]', { has: page.getByText(name, { exact: true }) });

type Check = (name: string) => Promise<void>;

// The screens with no login. `check` runs on each one.
async function publicScreens(page: Page, s: Seeded, check: Check) {
  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Log in" })).toBeVisible();
  await check("login");
  await page.getByLabel("Email").fill(user(s, "owner").email);
  await page.getByLabel("Password").fill("not-the-password");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.locator('[data-slot="failure"]')).toBeVisible();
  await check("login with an error");

  await page.goto("/forgot-password");
  await expect(page.getByRole("heading", { name: "Reset your password" })).toBeVisible();
  await check("forgot password");

  await page.goto(user(s, "owner").reset_path ?? "");
  await expect(page.getByLabel("New password")).toBeVisible();
  await check("reset password");

  await page.goto(user(s, "invitee").invite_path ?? "");
  await expect(page.getByLabel("Choose a password")).toBeVisible();
  await check("invitation");
}

// The screens of an owner, the drawers and the questions.
async function ownerScreens(page: Page, s: Seeded, check: Check) {
  await login(page, user(s, "owner"));
  await expect(cell(page, "Main")).toContainText("Connected");
  await expect(cell(page, "Lost")).toContainText("Disconnected");
  await check("the list");

  await (await healthButton(page, /^Backend and core fully operational/)).focus();
  await expect(page.locator('[data-slot="health-card"]')).toBeVisible();
  await check("the card of services");
  await page.keyboard.press("Escape");
  await closeMenu(page);

  await cell(page, "Main").getByRole("link").click();
  await expect(drawer(page).getByRole("status")).toHaveText("Connected");
  await check("the drawer of an instance");
  await drawer(page)
    .getByRole("button", { name: "What are the states of the connection?" })
    .click();
  await check("the states of the connection");
  await page.keyboard.press("Escape");
  await drawer(page).getByRole("switch").click();
  await expect(page.getByRole("dialog", { name: "Turn off Calyx access?" })).toBeVisible();
  await check("the question of Calyx access");
  await page.getByRole("button", { name: "Cancel" }).click();
  await drawer(page).getByRole("button", { name: "Remove this instance" }).click();
  await expect(page.getByRole("dialog", { name: "Remove Main?" })).toBeVisible();
  await check("the question of a removal");
  await page.getByRole("button", { name: "Cancel" }).click();
  await drawer(page).getByRole("button", { name: "Close" }).first().click();

  await cell(page, "Meta line").getByRole("link").click();
  await expect(drawer(page)).toContainText("Meta Cloud API");
  await check("the drawer of a Meta instance");
  await drawer(page).getByRole("button", { name: "Close" }).first().click();

  await page
    .locator("main")
    .getByRole("region", { name: "Shop", exact: true })
    .getByRole("button", { name: "+ Connect a number" })
    .click();
  await expect(drawer(page).getByRole("textbox", { name: "Name" })).toBeVisible();
  await check("connect: the name");
  await drawer(page).getByRole("textbox", { name: "Name" }).fill("Front desk");
  await drawer(page).getByRole("button", { name: "Continue", exact: true }).click();
  await check("connect: the use");
  await drawer(page).getByRole("button", { name: "Continue", exact: true }).click();
  await check("connect: the options");
  await drawer(page).getByRole("button", { name: "Continue with automatic" }).click();
  await drawer(page)
    .getByRole("button", { name: /^Yes, on this device/ })
    .click();
  await check("connect: a code");
  await drawer(page)
    .getByRole("button", { name: /^No, on another phone/ })
    .click();
  await control("POST", "/pairing", {
    steps: [{ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" }],
  });
  await drawer(page).getByRole("button", { name: "Show the QR code" }).click();
  await expect(drawer(page).getByRole("img", { name: "QR code" })).toBeVisible();
  await check("connect: the QR code");
  await control("POST", "/pairing/next");
  await expect(drawer(page).getByRole("button", { name: "Done" })).toBeVisible();
  await check("connect: done");
  await drawer(page).getByRole("button", { name: "Done" }).click();

  // The clients. Done can leave the card of the new instance open.
  await page.goto("/");
  const manage = (name: string) =>
    page
      .locator("main")
      .getByRole("region", { name, exact: true })
      .getByRole("link", { name: "Manage client" });
  await manage("Empty").click();
  await expect(page.getByRole("heading", { name: "Empty", level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel invitation" })).toBeVisible();
  await check("a client with an invitation");
  await page.getByRole("button", { name: "Delete this client" }).click();
  await expect(page.getByRole("dialog", { name: "Delete Empty?" })).toBeVisible();
  await check("the question of a deletion");
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("link", { name: "All instances" }).click();
  await manage("Shop").click();
  await expect(page.getByText("is the user of this client.")).toBeVisible();
  await check("a client with a user");
  await page.getByRole("link", { name: "All instances" }).click();
  await manage("My numbers").click();
  await expect(page.getByRole("heading", { name: "My numbers", level: 1 })).toBeVisible();
  await check("the default client");
  await page.getByRole("link", { name: "All instances" }).click();
  await page.locator("main").getByRole("button", { name: "Add a client" }).click();
  await expect(page.getByRole("dialog", { name: "Add a client" })).toBeVisible();
  await check("add a client");
  await page.getByRole("button", { name: "Cancel" }).click();

  await page.goto("/account");
  await expect(page.getByRole("heading", { name: "Account", level: 1 })).toBeVisible();
  await check("account");
  await page.goto("/organization");
  await expect(page.getByRole("heading", { name: "Organization", level: 1 })).toBeVisible();
  await check("organization");
  await page.getByRole("button", { name: "How do I change the limit?" }).click();
  await expect(page.getByRole("tablist", { name: "How do you run Calyx?" })).toBeVisible();
  await check("organization with the limit help open");
  await page.goto("/organization/whatsapp-web");
  await expect(page.getByRole("heading", { name: "WhatsApp Web" })).toBeVisible();
  await check("the terms");
  await page.goto("/developers/api-keys");
  await expect(page.getByRole("heading", { name: "API keys", level: 1 })).toBeVisible();
  await check("API keys");
  await page.getByRole("button", { name: "Create API key" }).click();
  await page.getByLabel("Name", { exact: true }).fill("Assistant");
  await check("the form of a new key");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  const newKey = page.getByRole("dialog", { name: "Your new API key" });
  await expect(newKey.getByLabel("Key", { exact: true })).toBeVisible();
  await check("the dialog of a new key");
  await newKey.getByRole("button", { name: "I saved it" }).click();
  await expect(newKey).toBeHidden();
  await page.getByRole("button", { name: "Revoke" }).click();
  await expect(page.getByRole("dialog", { name: /^Revoke/ })).toBeVisible();
  await check("the question of a revoke");
  await page.getByRole("button", { name: "Cancel" }).click();

  await page.goto("/no-such-page");
  await expect(page.getByText("This page does not exist")).toBeVisible();
  await check("not found");
}

// The screens of a client user: one number, and no number.
async function clientScreens(page: Page, s: Seeded, check: Check) {
  await login(page, user(s, "shopper"));
  await expect(page.locator('[data-slot="instance"]').getByRole("status")).toHaveText("Connected");
  await check("the screen of one number");
  await page.context().clearCookies();
  await login(page, user(s, "baker"));
  await expect(page.getByRole("heading", { name: "Connect your WhatsApp" })).toBeVisible();
  await check("no number");
}

// The webhooks view, its read-only form, and the question "Only send".
async function seedHooks(): Promise<Seeded> {
  const { status, body } = await control<Seeded>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    clients: [{ name: "Shop", can_create_instances: true }],
    users: [{ label: "owner", role: "owner" }],
    instances: [
      {
        name: "Hooked",
        client: "Shop",
        provider: "WHATSAPP_WEB",
        status: "CONNECTED",
        webhooks: [
          { event: "message.received", url: "https://n8n.example/webhook/calyx" },
          { event: "message.status", url: "https://n8n.example/webhook/calyx" },
        ],
      },
      {
        name: "Lost",
        client: "Shop",
        provider: "WHATSAPP_WEB",
        status: "CONNECTED",
        state: "missing",
        webhooks: [{ event: "message.received", url: "https://n8n.example/webhook/calyx" }],
      },
    ],
  });
  expect(status).toBe(200);
  return body;
}

const hookId = (s: Seeded, name: string) => s.instances.find((i) => i.name === name)?.id as string;
const hooksView = (page: Page) => page.locator('[data-slot="webhooks"]');
const onlySend = (page: Page) =>
  page.getByRole("dialog").filter({ hasText: "Calyx stops calling these addresses." });

async function webhookScreens(page: Page, s: Seeded, check: Check) {
  await login(page, user(s, "owner"));
  await expect(cell(page, "Hooked")).toContainText("Connected");
  await page.goto(`/instances/${hookId(s, "Hooked")}/webhooks`);
  await expect(hooksView(page).getByRole("heading", { name: "Webhooks" })).toBeVisible();
  await check("the webhooks view");
  await hooksView(page)
    .getByRole("button", { name: /^One URL for each event/ })
    .click();
  await hooksView(page)
    .getByRole("button", { name: /^Raw WhatsApp events/ })
    .click();
  await check("the webhooks view: one URL for each event, and the raw events");
  await hooksView(page).getByRole("button", { name: "Technical details" }).click();
  await hooksView(page)
    .getByRole("button", { name: /^One URL for all/ })
    .click();
  await hooksView(page).getByLabel("URL", { exact: true }).fill("not a url");
  await hooksView(page).getByRole("button", { name: "Save webhooks" }).click();
  await expect(hooksView(page).locator("[aria-invalid=true]").first()).toBeVisible();
  await check("the webhooks view: an error");
  await hooksView(page).getByLabel("URL", { exact: true }).fill("https://n8n.example/new");
  await hooksView(page).getByRole("radio", { name: "Generate a secret" }).click();
  await expect(hooksView(page).getByLabel("Generated secret")).toBeVisible();
  await check("the webhooks view: a generated secret");

  await page.goto(`/instances/${hookId(s, "Lost")}/webhooks`);
  await expect(hooksView(page)).toContainText("This instance is not ready");
  await check("the webhooks view: read only");

  await page.goto(`/instances/${hookId(s, "Hooked")}`);
  await drawer(page).getByRole("combobox", { name: "Use" }).click();
  await page.getByRole("option", { name: "Only send" }).click();
  await expect(onlySend(page)).toBeVisible();
  await check("the question of Only send");
}

// The bulk jobs view, the question of "Stop sending", and the question of
// "Remove the user". The client "Shop" has a user, and "Main" has a webhook and three jobs.
async function seedBulk(): Promise<Seeded> {
  const job = (status: string, sent: number, failed: number, percent: number) => ({
    status,
    total: 500,
    sent,
    failed,
    progress_percent: percent,
    created_ago_seconds: 600,
    created_by: "owner",
  });
  const { status, body } = await control<Seeded>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    clients: [{ name: "Shop", can_create_instances: true }],
    users: [
      { label: "owner", role: "owner" },
      { label: "shopper", role: "client", client: "Shop" },
    ],
    instances: [
      {
        name: "Main",
        client: "Shop",
        provider: "WHATSAPP_WEB",
        status: "CONNECTED",
        webhooks: [{ event: "message.received", url: "https://n8n.example/webhook/calyx" }],
        bulk_jobs: [
          { ...job("RUNNING", 120, 3, 24.6), estimated_finish_in_seconds: 3600 },
          job("QUEUED", 0, 0, 0),
          job("COMPLETED", 497, 3, 100),
        ],
      },
    ],
  });
  expect(status).toBe(200);
  return body;
}

const bulkView = (page: Page) => page.locator('[data-slot="bulk-jobs"]');
const stopAsk = (page: Page) => page.getByRole("dialog", { name: "Stop sending?" });
const removeAsk = (page: Page) => page.getByRole("dialog", { name: "Remove the user?" });

async function bulkScreens(page: Page, s: Seeded, check: Check) {
  await login(page, user(s, "owner"));
  await expect(cell(page, "Main")).toContainText("Connected");
  await page.goto(`/instances/${hookId(s, "Main")}/bulk-jobs`);
  await expect(bulkView(page).getByText(/^Read at /)).toBeVisible();
  await expect(bulkView(page).locator('[data-slot="bulk-job"]')).toHaveCount(3);
  await check("the bulk jobs view");
  await bulkView(page).getByRole("button", { name: "Stop sending" }).first().click();
  await expect(stopAsk(page)).toBeVisible();
  await check("the question of Stop sending");
  await page.getByRole("button", { name: "Keep sending" }).click();

  const shop = (s.clients.find((c) => c.name === "Shop") as { path?: string }).path ?? "";
  await page.goto(shop);
  await page.getByRole("button", { name: "Remove the user" }).click();
  await expect(removeAsk(page).getByText("https://n8n.example/webhook/calyx")).toBeVisible();
  await check("the question of Remove the user");
}

// axe with the rules of WCAG 2.2 A and AA. `include` and `exclude` are selectors. `disabled` lists rule IDs.
type AxeScope = { include?: string; exclude?: string; disabled?: string[] };
async function axe(page: Page, name: string, scope: AxeScope = {}) {
  // A drawer or a question fades in. Its colors are final only at the end.
  await page.waitForFunction(() =>
    document
      .getAnimations()
      .every((a) => a.playState !== "running" || a.effect?.getTiming().iterations === Infinity),
  );
  let builder = new AxeBuilder({ page }).withTags([
    "wcag2a",
    "wcag2aa",
    "wcag21a",
    "wcag21aa",
    "wcag22aa",
  ]);
  if (scope.include) builder = builder.include(scope.include);
  if (scope.exclude) builder = builder.exclude(scope.exclude);
  if (scope.disabled) builder = builder.disableRules(scope.disabled);
  const result = await builder.analyze();
  const found = result.violations.map(
    (v) => `${name}: ${v.id} (${v.impact}): ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`,
  );
  expect.soft(found).toEqual([]);
}

test.describe("axe: the sidebar", () => {
  test("the open Sheet at the phone width", async ({ page }) => {
    test.skip(test.info().project.name !== "phone-360", "the phone width");
    const s = await seed();
    await login(page, user(s, "owner"));
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await page.getByRole("button", { name: "Menu", exact: true }).click();
    await expect(
      page.getByRole("dialog", { name: "Menu" }).getByRole("link", { name: "API docs" }),
    ).toBeVisible();
    await axe(page, "the Sheet, open, phone width");
  });
  test("the sidebar at the wide width", async ({ page }) => {
    test.skip(test.info().project.name !== "desktop-1280", "the wide width");
    const s = await seed();
    await login(page, user(s, "owner"));
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await expect(page.getByRole("button", { name: "Menu", exact: true })).toBeHidden();
    await expect(
      page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "API docs" }),
    ).toBeVisible();
    await axe(page, "the sidebar, wide width");
  });
});

// The terms notice in place of the Instances screen, and the banner on the other screens.
test("axe: the terms notice and its banner", async ({ page }) => {
  const { status, body } = await control<{ users: Person[] }>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: false, whatsapp_web_allowed: false },
    users: [{ label: "owner", role: "owner" }],
  });
  expect(status).toBe(200);
  await login(page, body.users[0] as Person);
  await expect(page.locator('[data-slot="terms-notice"]')).toBeVisible();
  await axe(page, "the terms notice");
  await page.goto("/organization");
  await expect(page.locator('[data-slot="terms-banner"]')).toBeVisible();
  await axe(page, "the terms banner");
});

// The parts of Calyx with every rule, then Scalar alone without the rule IDs that Scalar itself fails.
const scalarRules = [
  "color-contrast",
  "heading-order",
  "landmark-unique",
  "scrollable-region-focusable",
];

test.describe("axe: the docs page", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`the ${theme} theme`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: theme });
      const s = await seed();
      await login(page, user(s, "owner"));
      await expect(page).toHaveURL(/\/$/);
      await page.goto("/docs");
      await expect(
        page.locator('[data-slot="scalar"]').getByRole("heading", { name: "Start here" }),
      ).toBeVisible();
      await expect(page.getByRole("region", { name: "Ways to connect to Calyx" })).toBeVisible();
      await axe(page, `docs, ${theme}, the parts of Calyx`, { exclude: '[data-slot="scalar"]' });
      await axe(page, `docs, ${theme}, Scalar`, {
        include: '[data-slot="scalar"]',
        disabled: scalarRules,
      });
    });
  }
});

test.describe("axe", () => {
  test("the screens with no login", async ({ page }) => {
    const s = await seed();
    await publicScreens(page, s, (name) => axe(page, name));
  });
  test("the screens of an owner", async ({ page }) => {
    const s = await seed();
    await ownerScreens(page, s, (name) => axe(page, name));
  });
  test("the screens of a client user", async ({ page }) => {
    const s = await seed();
    await clientScreens(page, s, (name) => axe(page, name));
  });
  test("the webhooks view and the question of Only send", async ({ page }) => {
    const s = await seedHooks();
    await webhookScreens(page, s, (name) => axe(page, name));
  });
  test("the bulk jobs view and its two questions", async ({ page }) => {
    const s = await seedBulk();
    await bulkScreens(page, s, (name) => axe(page, name));
  });
  test("the dark theme", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    const s = await seed();
    await publicScreens(page, s, (name) => axe(page, `dark, ${name}`));
    await ownerScreens(page, s, (name) => axe(page, `dark, ${name}`));
  });
});

// The element with the focus has a visible mark: an outline or a ring. The body has the focus
// only when Tab leaves the end of the page for the browser.
async function focusMark(page: Page): Promise<string | null> {
  return page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    if (!el || el === document.body) return null;
    const s = getComputedStyle(el);
    const outline = s.outlineStyle !== "none" && Number.parseFloat(s.outlineWidth) > 0;
    const ring = s.boxShadow !== "none";
    if (outline || ring) return null;
    return `${el.tagName.toLowerCase()} "${(el.textContent ?? el.getAttribute("aria-label") ?? "").trim().slice(0, 40)}" has no focus mark`;
  });
}

// Presses Tab until `target` or an element in it has the focus. Each stop on the way must show
// the focus.
async function tabTo(page: Page, target: Locator, afterLoad = false, max = 60) {
  // A closed drawer or question gives the focus back to its origin, so a walk never starts at the body.
  // A new page starts at the body. A closed drawer gives the focus back in the next frame.
  const lost =
    !afterLoad &&
    !(await page
      .waitForFunction(() => document.activeElement !== document.body, undefined, { timeout: 1000 })
      .then(() => true)
      .catch(() => false));
  const missing: string[] = lost ? ["the focus was lost before this walk"] : [];
  for (let i = 0; i < max; i++) {
    await page.keyboard.press("Tab");
    // At the end of a dialog, Tab lands on a focus guard of Base UI, which moves the focus on in
    // the next frame. A second Tab before that move leaves the dialog.
    await page.waitForFunction(
      () => !document.activeElement?.hasAttribute("data-base-ui-focus-guard"),
      undefined,
      { timeout: 1000 },
    );
    const mark = await focusMark(page);
    if (mark) missing.push(mark);
    if (
      await target
        .evaluate((el) => el.contains(document.activeElement), undefined, { timeout: 1000 })
        .catch(() => false)
    ) {
      expect.soft(missing).toEqual([]);
      // The walk can pass the health button, whose card opens on focus and closes a moment after.
      // A dialog that opens while the card closes does not get the focus. So the next key waits
      // for the card to close.
      const inCard = await target.evaluate(
        (el) => el.closest('[data-slot="health-card"]') !== null,
      );
      if (!inCard) await expect(page.locator('[data-slot="health-card"]')).toHaveCount(0);
      return;
    }
  }
  throw new Error(`Tab did not reach the target in ${max} steps`);
}

test("the keyboard alone: log in, an instance, Calyx access, and Connect", async ({ page }) => {
  const s = await seed();
  await page.goto("/login");
  await tabTo(page, page.getByLabel("Email"), true);
  await page.keyboard.type(user(s, "owner").email);
  await tabTo(page, page.getByLabel("Password"));
  await page.keyboard.type(user(s, "owner").password);
  await page.keyboard.press("Enter");
  await expect(cell(page, "Main")).toContainText("Connected");

  // The card of services opens with the focus, and closes with Escape. At the phone width the health
  // status is in the Sheet behind the button "Menu": open it first, and close it after the card.
  const menu = page.getByRole("button", { name: "Menu", exact: true });
  const phone = await menu.isVisible();
  if (phone) {
    await tabTo(page, menu, true);
    await page.keyboard.press("Enter");
  }
  await tabTo(page, page.locator('[data-slot="health-card"]'), !phone);
  await page.keyboard.press("Escape");
  await expect(page.locator('[data-slot="health-card"]')).toBeHidden();
  if (phone) {
    // The first Escape closes only the card: the Sheet stays open and the focus returns to the button.
    await expect(page.getByRole("dialog", { name: "Menu" })).toBeVisible();
    await expect(
      page.getByRole("button", { name: /^Backend and core fully operational/ }),
    ).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog", { name: "Menu" })).toBeHidden();
  }

  const link = cell(page, "Main").getByRole("link");
  await tabTo(page, link);
  await page.keyboard.press("Enter");
  await expect(drawer(page).getByRole("heading", { name: "Main", level: 2 })).toBeFocused();
  const access = drawer(page).getByRole("switch");
  await tabTo(page, access);
  await page.keyboard.press("Space");
  const ask = page.getByRole("dialog", { name: "Turn off Calyx access?" });
  await expect(ask).toBeVisible();
  // The dialog moves the focus to its first button a moment after it opens. A Tab before that
  // move would race with it.
  await expect(ask.getByRole("button", { name: "Turn off" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(access).not.toBeChecked();
  await page.keyboard.press("Escape");
  await expect(page).toHaveURL(/\/$/);
  await expect(link).toBeFocused();

  const connect = page
    .locator("main")
    .getByRole("region", { name: "Shop", exact: true })
    .getByRole("button", { name: "+ Connect a number" });
  await tabTo(page, connect);
  await page.keyboard.press("Enter");
  await expect(
    drawer(page).getByRole("heading", { name: "Connect a WhatsApp number", level: 2 }),
  ).toBeFocused();
  await tabTo(page, drawer(page).getByRole("textbox", { name: "Name" }));
  await page.keyboard.type("Front desk");
  await page.keyboard.press("Enter");
  await expect(drawer(page).getByRole("button", { name: /^Only send/ })).toBeVisible();
  await tabTo(page, drawer(page).getByRole("button", { name: /^Only send/ }));
  await page.keyboard.press("Space");
  await expect(drawer(page).getByRole("button", { name: /^Only send/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await tabTo(page, drawer(page).getByRole("button", { name: "Continue", exact: true }));
  await page.keyboard.press("Enter");
  await tabTo(page, drawer(page).getByRole("button", { name: "Continue with automatic" }));
  await page.keyboard.press("Enter");
  await tabTo(page, drawer(page).getByRole("button", { name: /^No, on another phone/ }));
  await page.keyboard.press("Space");
  await control("POST", "/pairing", {
    steps: [{ step: "update", qr: "2@first" }, { step: "wait" }],
  });
  await tabTo(page, drawer(page).getByRole("button", { name: "Show the QR code" }));
  await page.keyboard.press("Enter");
  await expect(drawer(page).getByRole("img", { name: "QR code" })).toBeVisible();
  await tabTo(page, drawer(page).getByRole("button", { name: "Cancel", exact: true }));
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/$/);

  // The sidebar, and the password form of Account.
  // At the phone width the sidebar is in the Sheet behind the button "Menu": walk to it and open it first.
  const menuButton = page.getByRole("button", { name: "Menu", exact: true });
  if (await menuButton.isVisible()) {
    await tabTo(page, menuButton);
    await page.keyboard.press("Enter");
  }
  await tabTo(
    page,
    page.getByRole("navigation", { name: "Main" }).getByRole("link", { name: "Account" }),
  );
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "Account", level: 1 })).toBeVisible();
  await tabTo(page, page.getByLabel("Current password"));
  await page.keyboard.type(user(s, "owner").password);
  await tabTo(page, page.getByLabel("New password"));
  await page.keyboard.type("another-long-password");
  await page.keyboard.press("Enter");
  await expect(page.getByText("Your password is changed.")).toBeVisible();
});

test("the keyboard alone: create an API key, save it, and revoke it", async ({ page }) => {
  const s = await seed();
  await login(page, user(s, "owner"));
  await expect(cell(page, "Main")).toContainText("Connected");
  await page.goto("/developers/api-keys");
  await expect(page.getByRole("heading", { name: "API keys", level: 1 })).toBeVisible();
  // "Save" waits for the first read of the list, so Enter before that read does nothing.
  await expect(page.getByText("You have no API keys.")).toBeVisible();

  await tabTo(page, page.getByRole("button", { name: "Create API key" }), true, 120);
  await page.keyboard.press("Enter");
  // The form opens with the focus in the name field.
  await expect(page.getByLabel("Name", { exact: true })).toBeFocused();
  await page.keyboard.type("Assistant");
  await page.keyboard.press("Enter");
  const newKey = page.getByRole("dialog", { name: "Your new API key" });
  await expect(newKey.getByLabel("Key", { exact: true })).toBeFocused();

  // Escape asks first. "Go back" must leave the focus in the dialog.
  await page.keyboard.press("Escape");
  await expect(newKey.getByRole("alert")).toBeVisible();
  // The question moves the focus to "Go back" itself. A Tab here would race with that move.
  await expect(newKey.getByRole("button", { name: "Go back" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(newKey.getByRole("alert")).toHaveCount(0);
  await expect(newKey.getByRole("button", { name: "I saved it" })).toBeFocused();

  await tabTo(page, newKey.getByRole("button", { name: "Copy", exact: true }));
  await tabTo(page, newKey.getByRole("button", { name: "I saved it" }));
  await page.keyboard.press("Enter");
  await expect(newKey).toBeHidden();
  // The form closed with the dialog, so the focus goes to "Create API key", not to the body.
  await expect(page.getByRole("button", { name: "Create API key" })).toBeFocused();

  await tabTo(page, page.getByRole("button", { name: "Revoke" }));
  await page.keyboard.press("Enter");
  const ask = page.getByRole("dialog", { name: /^Revoke/ });
  await expect(ask).toBeVisible();
  // The dialog moves the focus to its first button a moment after it opens. A Tab before that
  // would leave the dialog.
  await expect(ask.getByRole("button", { name: "Revoke" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(ask).toBeHidden();
  await expect(page.getByText("You have no API keys.")).toBeVisible();
  // The row of the key is gone, so the focus is on "Create API key", not on the body.
  await expect(page.getByRole("button", { name: "Create API key" })).toBeFocused();
});

test("the keyboard alone: save webhooks, and Only send with webhooks", async ({ page }) => {
  const s = await seedHooks();
  await login(page, user(s, "owner"));
  await expect(cell(page, "Hooked")).toContainText("Connected");
  await page.goto(`/instances/${hookId(s, "Hooked")}/webhooks`);
  await expect(hooksView(page).getByRole("heading", { name: "Webhooks" })).toBeFocused();

  const url = hooksView(page).getByLabel("URL", { exact: true });
  await tabTo(page, url, true);
  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.type("https://n8n.example/changed");
  await tabTo(page, hooksView(page).getByRole("checkbox", { name: "The connection changes" }));
  await page.keyboard.press("Space");
  await expect(
    hooksView(page).getByRole("checkbox", { name: "The connection changes" }),
  ).toBeChecked();
  const save = hooksView(page).getByRole("button", { name: "Save webhooks" });
  await tabTo(page, save);
  await page.keyboard.press("Enter");
  await expect(hooksView(page).getByText("Saved.", { exact: true })).toBeVisible();
  // The form is off during the save. The focus must not fall to the body.
  await expect(save).toBeFocused();

  await page.goto(`/instances/${hookId(s, "Hooked")}`);
  const use = drawer(page).getByRole("combobox", { name: "Use" });
  await tabTo(page, use, true);
  await page.keyboard.press("Enter");
  await page.getByRole("option", { name: "Only send" }).focus();
  await page.keyboard.press("Enter");
  const ask = onlySend(page);
  await expect(ask).toBeVisible();
  const remove = ask.getByRole("button", { name: "Remove the webhooks and only send" });
  // The first button of the question has the focus. A Tab before that move would race with it.
  await expect(remove).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(use).toContainText("Only send");
  await expect(ask).toBeHidden();
});

// With mail off, the link of the invitation shows: axe and the width check it too.
test("the keyboard alone: add a client, invite its user with mail off, and the limits", async ({
  page,
}) => {
  await control("POST", "/backend", { action: "stop" });
  await control("POST", "/backend", { action: "start", mail: false });
  const s = await seed();
  await login(page, user(s, "owner"));
  await expect(cell(page, "Main")).toContainText("Connected");

  await tabTo(page, page.locator("main").getByRole("button", { name: "Add a client" }), true, 120);
  await page.keyboard.press("Enter");
  const ask = page.getByRole("dialog", { name: "Add a client" });
  await expect(ask.getByLabel("Name")).toBeFocused();
  await page.keyboard.type("Kiosk");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "Kiosk", level: 1 })).toBeVisible();

  await tabTo(page, page.getByLabel("Email"), true);
  await page.keyboard.type("ana@kiosk.example");
  await page.keyboard.press("Enter");
  const link = page.locator('[data-slot="invite-link"]');
  await expect(link).toContainText("/invite?token=");
  await axe(page, "the link of an invitation");
  await fits(page, "the link of an invitation");
  await tabTo(page, page.getByRole("button", { name: "Copy the link" }), true);
  await tabTo(page, page.getByRole("button", { name: "Cancel invitation" }));
  await page.keyboard.press("Enter");
  await expect(page.getByText("No invitation is open.")).toBeVisible();

  await tabTo(page, page.getByLabel("Number of instances"), true);
  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.type("2");
  await page.keyboard.press("Enter");
  await expect(page.getByText("The limits are saved.")).toBeVisible();
});

// The keyboard alone on the bulk jobs view and on the two questions.
test("the keyboard alone: stop sending, and remove the user", async ({ page }) => {
  const s = await seedBulk();
  await login(page, user(s, "owner"));
  await expect(cell(page, "Main")).toContainText("Connected");
  await page.goto(`/instances/${hookId(s, "Main")}/bulk-jobs`);
  await expect(bulkView(page).getByRole("heading", { name: "Bulk jobs" })).toBeFocused();
  await expect(bulkView(page).getByText(/^Read at /)).toBeVisible();

  const stop = bulkView(page).getByRole("button", { name: "Stop sending" }).first();
  await tabTo(page, stop, true);
  await page.keyboard.press("Enter");
  const ask = stopAsk(page);
  await expect(ask).toBeVisible();
  // The first button of the question has the focus. A Tab before that move would race with it.
  await expect(ask.getByRole("button", { name: "Stop sending" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(ask.getByRole("button", { name: "Keep sending" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(ask).toBeHidden();
  await expect(stop).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(ask.getByRole("button", { name: "Stop sending" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(ask).toBeHidden();
  await expect(bulkView(page)).toContainText("Stopped");
  // The "Stop sending" button is gone, so the focus goes to the heading and not to the body.
  await expect(bulkView(page).getByRole("heading", { name: "Bulk jobs" })).toBeFocused();

  const shop = (s.clients.find((c) => c.name === "Shop") as { path?: string }).path ?? "";
  await page.goto(shop);
  const remove = page.getByRole("button", { name: "Remove the user" });
  await tabTo(page, remove, true);
  await page.keyboard.press("Enter");
  const removal = removeAsk(page);
  await expect(removal).toBeVisible();
  const confirm = removal.getByRole("button", { name: "Remove the user" });
  await expect(removal.getByText("https://n8n.example/webhook/calyx")).toBeVisible();
  await expect(confirm).toBeEnabled();
  // The button "Remove the user" is off while the dialog reads the instances, so the focus starts
  // on "Cancel", the safe choice. A Tab before that move would race with it.
  const cancel = removal.getByRole("button", { name: "Cancel" });
  await expect(cancel).toBeFocused();
  await tabTo(page, removal.getByRole("link", { name: "Webhooks" }));
  await tabTo(page, removal.getByRole("link", { name: "Bulk jobs" }));
  await tabTo(page, confirm);
  await tabTo(page, cancel);
  await page.keyboard.press("Enter");
  await expect(removal).toBeHidden();
  await expect(remove).toBeFocused();

  await page.keyboard.press("Enter");
  await expect(cancel).toBeFocused();
  await expect(confirm).toBeEnabled();
  await tabTo(page, confirm);
  await page.keyboard.press("Enter");
  await expect(removal).toBeHidden();
  await expect(page.getByText("No invitation is open.")).toBeVisible();
  // The button is gone, so the focus is on the email field, not on the body.
  await expect(page.getByLabel("Email")).toBeFocused();
});

test.describe("a width of 320 px", () => {
  test.use({ viewport: { width: 320, height: 640 } });
  // One width of 320 px is enough: the project of the phone runs it.
  test.beforeEach(() => test.skip(test.info().project.name !== "phone-360", "320 px once"));

  test("the screens with no login", async ({ page }) => {
    const s = await seed();
    await publicScreens(page, s, (name) => fits(page, name));
  });
  test("the logo and the menu button share a row", async ({ page }) => {
    const s = await seed();
    await login(page, user(s, "owner"));
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    const bar = page.getByRole("banner");
    const menu = await bar.getByRole("button", { name: "Menu", exact: true }).boundingBox();
    const logo = await bar.getByRole("link").boundingBox();
    expect(menu?.y).toBe(logo?.y);
    await fits(page, "the menu button");
  });
  test("the screens of an owner", async ({ page }) => {
    const s = await seed();
    await ownerScreens(page, s, (name) => fits(page, name));
  });
  test("the screens of a client user", async ({ page }) => {
    const s = await seed();
    await clientScreens(page, s, (name) => fits(page, name));
  });
  test("the webhooks view and the question of Only send", async ({ page }) => {
    const s = await seedHooks();
    await webhookScreens(page, s, (name) => fits(page, name));
  });
  test("the bulk jobs view and its two questions", async ({ page }) => {
    const s = await seedBulk();
    await bulkScreens(page, s, (name) => fits(page, name));
  });
});

// 200 % zoom of a window of 1280 x 800 px: 640 x 400 CSS pixels, two device pixels each.
test.describe("200 % zoom", () => {
  test.use({ viewport: { width: 640, height: 400 }, deviceScaleFactor: 2 });
  test.beforeEach(() =>
    test.skip(test.info().project.name !== "desktop-1280", "the zoom of 1280 px"),
  );

  test("the screens with no login", async ({ page }) => {
    const s = await seed();
    await publicScreens(page, s, (name) => fits(page, name));
  });
  test("the screens of an owner", async ({ page }) => {
    const s = await seed();
    await ownerScreens(page, s, (name) => fits(page, name));
  });
  test("the screens of a client user", async ({ page }) => {
    const s = await seed();
    await clientScreens(page, s, (name) => fits(page, name));
  });
  test("the webhooks view and the question of Only send", async ({ page }) => {
    const s = await seedHooks();
    await webhookScreens(page, s, (name) => fits(page, name));
  });
  test("the bulk jobs view and its two questions", async ({ page }) => {
    const s = await seedBulk();
    await bulkScreens(page, s, (name) => fits(page, name));
  });
});
