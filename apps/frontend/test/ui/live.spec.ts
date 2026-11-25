import type { Locator, Page, TestInfo } from "@playwright/test";
import { control, expect, login, type Person, test } from "./support.ts";

// A change of the core is on the screen within 3 s of the event. The stream
// is open only while the page is visible.

type Seeded = {
  clients: { name: string; id: string }[];
  users: (Person & { id: string })[];
  instances: { name: string; id: string; path: string }[];
};

async function seed(instances: Record<string, unknown>[]): Promise<Seeded> {
  const { status, body } = await control<Seeded>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    clients: [{ name: "Shop" }],
    users: [
      { label: "owner", role: "owner" },
      { label: "shopper", role: "client", client: "Shop" },
    ],
    instances,
  });
  expect(status).toBe(200);
  return body;
}

const person = (s: Seeded, label: string) => s.users.find((u) => u.label === label) as Person;
const idOf = (s: Seeded, name: string) => s.instances.find((i) => i.name === name)?.id as string;

const main = (page: Page) => page.getByRole("main");
// A modal drawer hides the list from the accessibility tree, so the cell has a CSS locator.
const cell = (page: Page, name: string) =>
  page
    .locator("main")
    .locator('[data-slot="instance"]', { has: page.getByText(name, { exact: true }) });
const drawer = (page: Page) => page.getByRole("dialog").first();
const card = (page: Page) => drawer(page).locator('[data-slot="instance"]');

// The stream sends one `changed` at its start, and the page reads again after it. This waits for
// that second good `listInstances`, so the page has an open stream before the test acts.
function streamReady(page: Page) {
  let reads = 0;
  page.on("response", (r) => {
    if (new URL(r.url()).pathname === "/api/v1/instances" && r.status() === 200) reads++;
  });
  return async () => {
    await expect.poll(() => reads).toBeGreaterThanOrEqual(2);
  };
}

// The streams of `followEvents` that are open now, and the reads of the list.
function watchStream(page: Page) {
  let open = 0;
  let opened = 0;
  let reads = 0;
  const isEvents = (url: string) => new URL(url).pathname === "/api/v1/events";
  page.on("request", (r) => {
    if (isEvents(r.url())) {
      open++;
      opened++;
    }
    if (new URL(r.url()).pathname === "/api/v1/instances") reads++;
  });
  const end = (url: string) => {
    if (isEvents(url)) open--;
  };
  page.on("requestfinished", (r) => end(r.url()));
  page.on("requestfailed", (r) => end(r.url()));
  return { open: () => open, opened: () => opened, reads: () => reads };
}

// Sends the status to the fake core, and gives the time until `target` shows `text`.
async function timeToText(
  instance: string,
  status: string,
  targets: { where: string; locator: Locator }[],
  text: string,
  info: TestInfo,
): Promise<void> {
  const start = Date.now();
  const { status: code } = await control("POST", "/status", { instance, status });
  expect(code).toBe(200);
  for (const t of targets) {
    await expect(t.locator).toContainText(text, { timeout: 3000 });
    const ms = Date.now() - start;
    info.annotations.push({
      type: "time",
      description: `${t.where}: ${status} -> ${text}, ${ms} ms`,
    });
    expect(ms).toBeLessThanOrEqual(3000);
  }
}

test("from the control status to the new text in 3 s, in the drawer and the cell", async ({
  page,
}, info) => {
  const s = await seed([{ name: "Main", client: "Shop", status: "CONNECTED" }]);
  const ready = streamReady(page);
  await login(page, person(s, "owner"));
  await expect(cell(page, "Main")).toContainText("Connected");
  await ready();

  await cell(page, "Main").getByRole("link").click();
  await expect(page).toHaveURL(`/instances/${idOf(s, "Main")}`);
  await expect(card(page).getByRole("status")).toHaveText("Connected");

  await timeToText(
    idOf(s, "Main"),
    "NEEDS_AUTH",
    [
      { where: "drawer", locator: card(page).getByRole("status") },
      { where: "cell", locator: cell(page, "Main") },
    ],
    "Disconnected",
    info,
  );
  await timeToText(
    idOf(s, "Main"),
    "CONNECTED",
    [
      { where: "drawer", locator: card(page).getByRole("status") },
      { where: "cell", locator: cell(page, "Main") },
    ],
    "Connected",
    info,
  );
});

test("a client user with one number sees the new status on its screen in 3 s", async ({
  page,
}, info) => {
  const s = await seed([{ name: "Main", client: "Shop", status: "CONNECTED" }]);
  const ready = streamReady(page);
  await login(page, person(s, "shopper"));
  const screen = main(page).locator('[data-slot="instance"]');
  await expect(screen.getByRole("status")).toHaveText("Connected");
  await ready();

  await timeToText(
    idOf(s, "Main"),
    "DISCONNECTED",
    [{ where: "screen", locator: screen.getByRole("status") }],
    "Reconnecting",
    info,
  );
  await timeToText(
    idOf(s, "Main"),
    "NEEDS_AUTH",
    [{ where: "screen", locator: screen.getByRole("status") }],
    "Disconnected",
    info,
  );
  await expect(screen.getByRole("button", { name: "Reconnect" })).toBeVisible();
});

test("the cell gets its color, the cause and Reconnect, and Reconnect opens the instance", async ({
  page,
}, info) => {
  const s = await seed([
    { name: "Main", client: "Shop", status: "CONNECTED", send_only: true },
    { name: "Other", client: "Shop", status: "CONNECTED" },
  ]);
  const ready = streamReady(page);
  await login(page, person(s, "owner"));
  await expect(cell(page, "Main")).toContainText("Ready");
  await ready();
  await expect(cell(page, "Main").getByRole("button", { name: "Reconnect" })).toHaveCount(0);

  await timeToText(
    idOf(s, "Main"),
    "NEEDS_AUTH",
    [{ where: "cell", locator: cell(page, "Main") }],
    "Disconnected",
    info,
  );
  const main1 = cell(page, "Main");
  await expect(main1).toContainText("The phone must be linked again.");
  await expect(main1.locator(".bg-bad")).toHaveCount(1);
  await expect(main1).toHaveClass(/bg-bad-soft/);
  // The other number does not change.
  await expect(cell(page, "Other")).toContainText("Connected");
  await expect(page.getByText("1 number needs a person.")).toBeVisible();

  await main1.getByRole("button", { name: "Reconnect" }).click();
  await expect(page).toHaveURL(`/instances/${idOf(s, "Main")}`);
  await expect(drawer(page).getByRole("heading", { name: "Reconnect Main" })).toBeVisible();

  // The new pairing, with a QR code of the fake core.
  expect(
    (
      await control("POST", "/pairing", {
        steps: [{ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" }],
      })
    ).status,
  ).toBe(200);
  await drawer(page)
    .getByRole("button", { name: /^No, on another phone/ })
    .click();
  await drawer(page).getByRole("button", { name: "Show the QR code" }).click();
  await expect(drawer(page).getByRole("img", { name: "QR code" })).toBeVisible();
  const start = Date.now();
  expect((await control("POST", "/pairing/next")).status).toBe(200);
  await expect(drawer(page).getByRole("heading", { name: "Main is connected" })).toBeVisible();
  // The number keeps its settings: it only sends, so the cell says "Ready".
  await expect(cell(page, "Main")).toContainText("Ready", { timeout: 3000 });
  info.annotations.push({
    type: "time",
    description: `cell: pairing -> Ready, ${Date.now() - start} ms`,
  });
  await drawer(page).getByRole("button", { name: "Done" }).click();
  await expect(card(page).getByRole("status")).toHaveText("Ready");
  await expect(card(page).getByRole("combobox", { name: "Use" })).toContainText("Only send");
  await expect(cell(page, "Main")).not.toHaveClass(/bg-bad-soft/);
  await expect(page.getByText("1 number needs a person.")).toHaveCount(0);
});

test("the drawer: at the side or at the bottom, the focus on its title, and Close gives it back", async ({
  page,
}, info) => {
  const s = await seed([{ name: "Main", client: "Shop", status: "CONNECTED" }]);
  await login(page, person(s, "owner"));
  const link = cell(page, "Main").getByRole("link");
  await link.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(`/instances/${idOf(s, "Main")}`);
  const title = drawer(page).getByRole("heading", { name: "Main", level: 2 });
  await expect(title).toBeFocused();
  await expect(drawer(page)).toHaveAccessibleName("Main");

  const box = await drawer(page).boundingBox();
  const view = page.viewportSize();
  if (!box || !view) throw new Error("no box");
  await expect
    .poll(async () => (await drawer(page).boundingBox())?.x)
    .toBeCloseTo(view.width - box.width, 0);
  if (info.project.name === "phone-360") {
    // A phone: the drawer is at the bottom, with the whole width.
    expect(box.width).toBeCloseTo(view.width, 0);
    await expect
      .poll(async () => {
        const b = await drawer(page).boundingBox();
        return b ? b.y + b.height : 0;
      })
      .toBeCloseTo(view.height, 0);
  } else {
    // A wide screen: the drawer is at the right side, with the full height, and the list shows beside it.
    expect(box.width).toBeLessThanOrEqual(520);
    expect(box.height).toBeCloseTo(view.height, 0);
  }

  await drawer(page).getByRole("button", { name: "Close" }).click();
  await expect(page).toHaveURL("/");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(link).toBeFocused();

  // Escape closes it too.
  await page.keyboard.press("Enter");
  await expect(title).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page).toHaveURL("/");
  await expect(link).toBeFocused();
});

test("a direct link (an alert mail) opens the list with the drawer, and an unknown id says so", async ({
  page,
}) => {
  const s = await seed([
    { name: "Main", client: "Shop", status: "NEEDS_AUTH" },
    { name: "Other", client: "Shop", status: "CONNECTED" },
  ]);
  await login(page, person(s, "owner"));
  await expect(cell(page, "Other")).toBeVisible();
  await page.goto(`/instances/${idOf(s, "Main")}`);
  await expect(drawer(page).getByRole("heading", { name: "Main", level: 2 })).toBeFocused();
  await expect(card(page).getByRole("status")).toHaveText("Disconnected");
  await expect(cell(page, "Other")).toBeAttached();
  await drawer(page).getByRole("button", { name: "Close" }).click();
  await expect(page).toHaveURL("/");
  await expect(cell(page, "Main")).toBeVisible();

  await page.goto("/instances/00000000-0000-4000-8000-000000000000");
  await expect(
    drawer(page).getByRole("heading", { name: "This page does not exist" }),
  ).toBeFocused();
});

test("a client user with one number: its path shows its screen, and not a drawer", async ({
  page,
}) => {
  const s = await seed([{ name: "Main", client: "Shop", status: "CONNECTED" }]);
  await login(page, person(s, "shopper"));
  await expect(main(page).getByRole("heading", { name: "Main", level: 1 })).toBeVisible();
  await page.goto(`/instances/${idOf(s, "Main")}`);
  await expect(main(page).getByRole("heading", { name: "Main", level: 1 })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

// Headless Chromium keeps every tab "visible", also behind another tab. So the test sets the
// visibility of the page itself, as a person who goes to WhatsApp makes it.
async function setVisibility(page: Page, state: "hidden" | "visible"): Promise<void> {
  await page.evaluate((value) => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => value });
    Object.defineProperty(document, "hidden", {
      configurable: true,
      get: () => value === "hidden",
    });
    document.dispatchEvent(new Event("visibilitychange"));
  }, state);
}

test("the stream of a hidden page is closed, and the return reads again", async ({ page }) => {
  const s = await seed([{ name: "Main", client: "Shop", status: "CONNECTED" }]);
  const ready = streamReady(page);
  const live = watchStream(page);
  await login(page, person(s, "owner"));
  await expect(cell(page, "Main")).toContainText("Connected");
  await ready();
  expect(live.open()).toBe(1);

  await setVisibility(page, "hidden");
  await expect.poll(() => live.open()).toBe(0);
  const opened = live.opened();
  const reads = live.reads();

  // A change while the page is hidden: no stream, so no read.
  expect(
    (await control("POST", "/status", { instance: idOf(s, "Main"), status: "NEEDS_AUTH" })).status,
  ).toBe(200);
  await page.waitForTimeout(1500);
  expect(live.reads()).toBe(reads);
  expect(live.opened()).toBe(opened);
  await expect(cell(page, "Main")).toContainText("Connected");

  // The return opens the stream, and its first `changed` reads the list again.
  await setVisibility(page, "visible");
  await expect.poll(() => live.open()).toBe(1);
  await expect(cell(page, "Main")).toContainText("Disconnected", { timeout: 3000 });
  expect(live.reads()).toBeGreaterThan(reads);
});
