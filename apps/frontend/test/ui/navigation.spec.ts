import type { Page } from "@playwright/test";
import { control, expect, login, mainLink, type Person, seedBasic, test } from "./support.ts";

// The sidebar and its routes.

const heading = (page: Page, name: string) => page.getByRole("heading", { name, level: 1 });
const phone = () => test.info().project.name === "phone-360";

// Each entry of the panel and the heading of its screen. "API docs" is a full page load.
const entries = {
  owner: [
    ["Instances", "Instances"],
    ["API keys", "API keys"],
    ["Connect a program", "Connect a program"],
    ["Organization", "Organization"],
    ["WhatsApp Web", "WhatsApp Web"],
    ["Account", "Account"],
  ],
  // The client user of seedBasic has one instance, "Main": the list opens it.
  client: [
    ["My WhatsApp", "Main"],
    ["API keys", "API keys"],
    ["Connect a program", "Connect a program"],
    ["Account", "Account"],
  ],
};

const links = {
  owner: [
    "Instances",
    "API keys",
    "Connect a program",
    "API docs",
    "Organization",
    "WhatsApp Web",
    "Account",
  ],
  client: ["My WhatsApp", "API keys", "Connect a program", "API docs", "Account"],
};

const starts = [
  ["/", { owner: "Instances", client: "Main" }],
  ["/developers/api-keys", { owner: "API keys", client: "API keys" }],
] as const;

test.describe("each entry opens its screen", () => {
  for (const role of ["owner", "client"] as const) {
    test(`the ${role} opens each entry from / and from /developers/api-keys`, async ({ page }) => {
      const seeded = await seedBasic();
      await login(page, seeded[role]);
      await expect(page).toHaveURL(/\/$/);

      const nav = page.getByRole("navigation", { name: "Main" });
      await (await mainLink(page, "Account")).waitFor();
      await expect(nav.getByRole("link")).toHaveText(links[role]);
      await expect(page.getByText(/^Calyx \d+\.\d+\.\d+$/).locator("visible=true")).toBeVisible();
      await expect(nav.getByText("Developers", { exact: true })).toBeVisible();
      await expect(nav.getByText("Organization", { exact: true })).toHaveCount(
        role === "owner" ? 2 : 0,
      );

      for (const [start, startHeading] of starts) {
        for (const [entry, screen] of entries[role]) {
          await page.goto(start);
          await expect(heading(page, startHeading[role])).toBeVisible();
          await (await mainLink(page, entry)).click();
          await expect(heading(page, screen)).toBeVisible();
          // At the phone width the Sheet closes after the selection: check only at the wide width.
          if (!phone()) {
            await expect(await mainLink(page, entry)).toHaveAttribute("aria-current", "page");
          }
        }
        await page.goto(start);
        await expect(heading(page, startHeading[role])).toBeVisible();
        const docs = await mainLink(page, "API docs");
        await expect(docs).toHaveAttribute("href", "/docs");
        await docs.click();
        await expect(page).toHaveURL(/\/docs$/);
      }
    });
  }
});

test("the active entry on /clients/:id is Instances", async ({ page }) => {
  const { body } = await control<{ clients: { id: string; name: string }[]; users: Person[] }>(
    "POST",
    "/seed",
    {
      organization: { name: "Acme", acknowledged: true },
      clients: [{ name: "Shop" }],
      users: [{ label: "owner", role: "owner" }],
    },
  );
  await login(page, body.users[0] as Person);
  await expect(page).toHaveURL(/\/$/);
  const shop = body.clients.find((c) => c.name === "Shop");
  await page.goto(`/clients/${shop?.id}`);
  await expect(page.getByRole("heading", { name: "Shop", level: 1 })).toBeVisible();
  await expect(await mainLink(page, "Instances")).toHaveAttribute("aria-current", "page");
  await expect(
    page.getByRole("navigation", { name: "Main" }).locator('[aria-current="page"]'),
  ).toHaveCount(1);
});

test("an owner opens API keys on a phone, and Escape closes the Menu", async ({ page }) => {
  test.skip(!phone(), "the phone width");
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(heading(page, "Instances")).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Main" })).toBeHidden();

  const menu = page.getByRole("button", { name: "Menu", exact: true });
  const sheet = page.getByRole("dialog", { name: "Menu" });
  await menu.click();
  await expect(sheet).toBeVisible();
  // The Sheet slides in from the right side.
  const width = page.viewportSize()?.width ?? 0;
  await expect
    .poll(async () => {
      const box = await sheet.boundingBox();
      return box ? Math.round(box.x + box.width) : -1;
    })
    .toBe(width);
  // The Sheet moves the focus into the sidebar.
  await expect(sheet.getByRole("navigation", { name: "Main" }).locator(":focus")).toHaveCount(1);
  await sheet.getByRole("link", { name: "API keys", exact: true }).click();
  await expect(sheet).toBeHidden();
  await expect(heading(page, "API keys")).toBeVisible();

  await menu.click();
  await expect(sheet).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
  await expect(menu).toBeFocused();
});

test("the sidebar stays in view on a long screen at the wide width", async ({ page }) => {
  test.skip(phone(), "the wide width");
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(heading(page, "Instances")).toBeVisible();
  const nav = page.getByRole("navigation", { name: "Main" });
  const version = page.getByText(/^Calyx \d+\.\d+\.\d+$/).locator("visible=true");
  await expect(nav.getByRole("link", { name: "Instances", exact: true })).toBeInViewport();
  await expect(version).toBeInViewport();
  // A screen three times as tall as the viewport, scrolled to its end.
  await page.locator("main").evaluate((m) => {
    m.style.minHeight = "2400px";
  });
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(1000);
  await expect(nav.getByRole("link", { name: "Instances", exact: true })).toBeInViewport();
  await expect(version).toBeInViewport();
});

test("from sm, the sidebar holds the logo, the health status, the version and the theme", async ({
  page,
}) => {
  test.skip(phone(), "the wide width");
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(heading(page, "Instances")).toBeVisible();
  // No top bar: the screen starts at the top of the page.
  await expect(page.getByRole("banner")).toHaveCount(0);
  expect((await page.locator("main").boundingBox())?.y).toBe(0);

  const sidebar = page.locator('[data-slot="sidebar"]').locator("visible=true");
  const logo = sidebar.getByRole("link").filter({ has: page.getByRole("img", { name: "Calyx" }) });
  const nav = sidebar.getByRole("navigation", { name: "Main" });
  const health = sidebar.getByRole("button", { name: /^Backend and core fully operational/ });
  const version = sidebar.getByText(/^Calyx \d+\.\d+\.\d+$/);
  const theme = sidebar.getByRole("button", { name: /^Use the (light|dark) theme$/ });
  for (const part of [logo, nav, health, version, theme]) await expect(part).toBeVisible();
  await expect(logo).toHaveAttribute("href", "/");
  // The column is a landmark (an aside with no name) that holds all five parts.
  const aside = page.getByRole("complementary").filter({ has: sidebar });
  await expect(aside).toHaveCount(1);
  await expect(aside.getByRole("navigation", { name: "Main" })).toBeVisible();
  await expect(aside.getByText(/^Calyx \d+\.\d+\.\d+$/)).toBeVisible();

  // Top to bottom: the logo, the entries, the health status, then one line with the version and the
  // theme button at its right.
  const box = async (l: typeof logo) =>
    (await l.boundingBox()) ?? { x: 0, y: 0, width: 0, height: 0 };
  const [l, n, h, v, t] = await Promise.all([logo, nav, health, version, theme].map(box));
  expect(l.y + l.height).toBeLessThanOrEqual(n.y);
  expect(n.y + n.height).toBeLessThanOrEqual(h.y);
  expect(h.y + h.height).toBeLessThanOrEqual(t.y);
  expect(Math.abs(v.y + v.height / 2 - (t.y + t.height / 2))).toBeLessThan(2);
  expect(v.x + v.width).toBeLessThan(t.x);
  // The sidebar fills the height of the viewport.
  expect((await sidebar.boundingBox())?.height).toBe(page.viewportSize()?.height);
});

test("from sm, the health details open from the sidebar and stay inside the viewport", async ({
  page,
}) => {
  test.skip(phone(), "the wide width");
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(heading(page, "Instances")).toBeVisible();
  const sidebar = page.locator('[data-slot="sidebar"]').locator("visible=true");
  await sidebar.getByRole("button", { name: /^Backend and core fully operational/ }).click();
  const card = page.locator('[data-slot="health-card"]');
  await expect(card).toBeVisible();
  const b = (await card.boundingBox()) ?? { x: -1, y: -1, width: 0, height: 0 };
  const view = page.viewportSize() ?? { width: 0, height: 0 };
  expect(b.x).toBeGreaterThanOrEqual(0);
  expect(b.y).toBeGreaterThanOrEqual(0);
  expect(b.x + b.width).toBeLessThanOrEqual(view.width);
  expect(b.y + b.height).toBeLessThanOrEqual(view.height);
});

test("at the phone width, a bar with the logo and Menu, and the health status and the theme in the Sheet", async ({
  page,
}) => {
  test.skip(!phone(), "the phone width");
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(heading(page, "Instances")).toBeVisible();
  const bar = page.getByRole("banner");
  await expect(bar.getByRole("button", { name: "Menu", exact: true })).toBeVisible();
  await expect(
    bar.getByRole("link").filter({ has: page.getByRole("img", { name: "Calyx" }) }),
  ).toBeVisible();
  // The logo is first (left), and the button "Menu" is at the far right.
  const logo = await bar
    .getByRole("link")
    .filter({ has: page.getByRole("img", { name: "Calyx" }) })
    .boundingBox();
  const button = await bar.getByRole("button", { name: "Menu", exact: true }).boundingBox();
  const view = page.viewportSize()?.width ?? 0;
  expect((logo?.x ?? 1e9) < (button?.x ?? 0)).toBe(true);
  expect(view - ((button?.x ?? 0) + (button?.width ?? 0))).toBeLessThanOrEqual(24);
  await expect(
    page.getByRole("button", { name: /^Backend and core fully operational/ }),
  ).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^Use the (light|dark) theme$/ })).toHaveCount(0);

  const sheet = page.getByRole("dialog", { name: "Menu" });
  await bar.getByRole("button", { name: "Menu", exact: true }).click();
  await expect(
    sheet.getByRole("button", { name: /^Backend and core fully operational/ }),
  ).toBeVisible();
  await expect(sheet.getByRole("button", { name: /^Use the (light|dark) theme$/ })).toBeVisible();
  await expect(sheet.getByText(/^Calyx \d+\.\d+\.\d+$/)).toBeVisible();
});

test("a group with a heading has its entries indented, with a line on their left", async ({
  page,
}) => {
  const { owner } = await seedBasic();
  await login(page, owner);
  await expect(heading(page, "Instances")).toBeVisible();
  await (await mainLink(page, "Account")).waitFor();
  const nav = page.getByRole("navigation", { name: "Main" }).locator("visible=true");
  const line = (name: string) =>
    nav.getByRole("list", { name }).evaluate((ul) => getComputedStyle(ul).borderLeftWidth);
  for (const name of ["Developers", "Organization"]) {
    await expect(nav.getByRole("list", { name })).toBeVisible();
    expect(await line(name)).toBe("1px");
  }
  // A group with one entry has no heading and no line.
  const single = nav
    .getByRole("listitem")
    .filter({ has: page.getByRole("link", { name: "Account", exact: true }) });
  expect(await single.locator("..").evaluate((ul) => getComputedStyle(ul).borderLeftWidth)).toBe(
    "0px",
  );
});

test.describe("the routes", () => {
  test("the owner opens Account, Organization, API keys and Connect a program", async ({
    page,
  }) => {
    const { owner } = await seedBasic();
    await login(page, owner);
    await expect(page).toHaveURL(/\/$/);
    await page.goto("/account");
    await expect(heading(page, "Account")).toBeVisible();
    await page.goto("/organization");
    await expect(heading(page, "Organization")).toBeVisible();
    await page.goto("/organization/whatsapp-web");
    await expect(heading(page, "WhatsApp Web")).toBeVisible();
    await page.goto("/developers/api-keys");
    await expect(heading(page, "API keys")).toBeVisible();
    await page.goto("/developers/connect");
    await expect(heading(page, "Connect a program")).toBeVisible();
  });

  test("a client user opens Account, API keys and Connect a program, and sees NotFound on the owner paths", async ({
    page,
  }) => {
    const { client } = await seedBasic();
    await login(page, client);
    await expect(page).toHaveURL(/\/$/);
    await page.goto("/account");
    await expect(heading(page, "Account")).toBeVisible();
    await page.goto("/developers/api-keys");
    await expect(heading(page, "API keys")).toBeVisible();
    await page.goto("/developers/connect");
    await expect(heading(page, "Connect a program")).toBeVisible();
    for (const path of ["/organization", "/organization/whatsapp-web"]) {
      await page.goto(path);
      await expect(heading(page, "This page does not exist")).toBeVisible();
    }
  });

  test("the old paths of Settings do not exist", async ({ page }) => {
    const { owner } = await seedBasic();
    await login(page, owner);
    await expect(page).toHaveURL(/\/$/);
    for (const path of ["/settings", "/settings/api-keys", "/settings/whatsapp-web"]) {
      await page.goto(path);
      await expect(heading(page, "This page does not exist")).toBeVisible();
    }
  });
});
