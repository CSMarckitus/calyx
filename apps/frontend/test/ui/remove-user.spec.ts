import { randomInt } from "node:crypto";
import type { Browser, Page } from "@playwright/test";
import { control, expect, login, type Person, test } from "./support.ts";

// "Remove the user" of a client. A 404 and a failed read are failed requests in
// the browser log.
test.use({ tolerateFailedRequests: true });

type Hook = { event: string; url: string };
type Seeded = {
  clients: { name: string; id: string; path: string }[];
  users: Person[];
  instances: { name: string; id: string; path: string }[];
};

const mainHook = "https://n8n.shop.example/webhook/main";
const secondHook = "https://crm.shop.example/calyx";
const goneHook = "https://old.shop.example/removing";
const otherHook = "https://other.example/hook";

// The client "Shop" with its user, two instances with one webhook each, and a running bulk job on
// "Main". "Old" is removing, and "Kiosk" belongs to another client: the dialog shows neither.
async function seed(): Promise<Seeded> {
  const { status, body } = await control<Seeded>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    clients: [{ name: "Shop" }, { name: "Other" }],
    users: [
      { label: "owner", role: "owner" },
      { label: "shopper", role: "client", client: "Shop" },
    ],
    instances: [
      {
        name: "Main",
        client: "Shop",
        status: "CONNECTED",
        webhooks: [{ event: "message.received", url: mainHook }],
        bulk_jobs: [
          {
            status: "RUNNING",
            total: 500,
            sent: 120,
            failed: 3,
            progress_percent: 24.6,
            created_ago_seconds: 600,
            created_by: "shopper",
          },
        ],
      },
      {
        name: "Second",
        client: "Shop",
        status: "CONNECTED",
        webhooks: [{ event: "message.status", url: secondHook }],
      },
      {
        name: "Old",
        client: "Shop",
        state: "removing",
        webhooks: [{ event: "message.received", url: goneHook }],
      },
      {
        name: "Kiosk",
        client: "Other",
        status: "CONNECTED",
        webhooks: [{ event: "message.received", url: otherHook }],
      },
    ],
  });
  expect(status).toBe(200);
  return body;
}

const person = (s: Seeded, label: string) => s.users.find((u) => u.label === label) as Person;
const clientOf = (s: Seeded, name: string) =>
  s.clients.find((c) => c.name === name) as Seeded["clients"][number];
const instanceOf = (s: Seeded, name: string) =>
  s.instances.find((i) => i.name === name) as Seeded["instances"][number];

const sentences = (email: string) => [
  `${email} can no longer log in, and Calyx revokes the API keys of ${email}. Programs with these keys get an error.`,
  "Calyx does not change the instances: it keeps calling the webhook URLs below, also the ones that a program set with a key of",
  "Bulk jobs keep sending. Open each instance below to see its bulk jobs.",
  "You can invite this email again. The old API keys stay revoked.",
];

const access = (page: Page) => page.getByRole("main").getByRole("region", { name: "Access" });
const ask = (page: Page) => page.getByRole("dialog", { name: "Remove the user?" });
const item = (page: Page, name: string) =>
  ask(page).locator('[data-slot="removal-instance"]', {
    has: page.getByText(name, { exact: true }),
  });
const confirm = (page: Page) => ask(page).getByRole("button", { name: "Remove the user" });

// Logs in as the owner and opens the client.
async function openClient(page: Page, s: Seeded): Promise<void> {
  await login(page, person(s, "owner"));
  await expect(page).toHaveURL(/\/$/);
  await page.goto(clientOf(s, "Shop").path);
  await expect(access(page)).toContainText(
    `${person(s, "shopper").email} is the user of this client.`,
  );
}

async function openDialog(page: Page): Promise<void> {
  await access(page).getByRole("button", { name: "Remove the user" }).click();
  await expect(ask(page)).toBeVisible();
}

// The webhooks of each instance, as the owner reads them.
async function hooks(page: Page): Promise<Record<string, Hook[]>> {
  const res = await page.request.get("/api/v1/instances");
  expect(res.ok()).toBe(true);
  const list = (await res.json()) as {
    items: { display_name: string; webhooks: (Hook & { has_secret: boolean })[] }[];
  };
  return Object.fromEntries(
    list.items.map((i) => [i.display_name, i.webhooks.map(({ event, url }) => ({ event, url }))]),
  );
}

// A separate browser, with its own login session and its own address for the login limits.
async function otherBrowser(browser: Browser, baseURL: string): Promise<Page> {
  const context = await browser.newContext({
    baseURL,
    extraHTTPHeaders: {
      "X-Forwarded-For": `10.${randomInt(256)}.${randomInt(256)}.${randomInt(1, 255)}`,
    },
  });
  return context.newPage();
}

test("the dialog lists the URLs as text, the four sentences and the links, then removes the user", async ({
  page,
}) => {
  const s = await seed();
  const email = person(s, "shopper").email;
  await openClient(page, s);
  const before = await hooks(page);

  // The dialog reads the instances, and makes no bulk read.
  const reads: string[] = [];
  page.on("request", (r) => {
    const path = new URL(r.url()).pathname;
    if (path === "/api/v1/instances" || path.includes("bulk-jobs")) reads.push(path);
  });
  await openDialog(page);
  for (const text of sentences(email)) await expect(ask(page)).toContainText(text);
  await expect(ask(page)).toContainText(`a key of ${email}.`);

  for (const [name, url] of [
    ["Main", mainHook],
    ["Second", secondHook],
  ] as const) {
    const id = instanceOf(s, name).id;
    const shown = item(page, name).getByText(url, { exact: true });
    await expect(shown).toBeVisible();
    await expect(shown).toHaveAttribute("translate", "no");
    // A URL is text, never a link.
    await expect(ask(page).getByRole("link", { name: url })).toHaveCount(0);
    await expect(item(page, name).getByRole("link", { name: "Webhooks" })).toHaveAttribute(
      "href",
      `/instances/${id}/webhooks`,
    );
    await expect(item(page, name).getByRole("link", { name: "Bulk jobs" })).toHaveAttribute(
      "href",
      `/instances/${id}/bulk-jobs`,
    );
  }
  await expect(ask(page).getByText("Old", { exact: true })).toHaveCount(0);
  await expect(ask(page)).not.toContainText(goneHook);
  await expect(ask(page).getByText("Kiosk", { exact: true })).toHaveCount(0);
  await expect(ask(page)).not.toContainText(otherHook);
  await expect(confirm(page)).toBeEnabled();
  expect(reads.filter((p) => p.includes("bulk-jobs"))).toEqual([]);
  expect(reads.length).toBeGreaterThan(0);

  // A link opens the view of the instance.
  await item(page, "Main").getByRole("link", { name: "Bulk jobs" }).click();
  await expect(page).toHaveURL(new RegExp(`/instances/${instanceOf(s, "Main").id}/bulk-jobs$`));
  await expect(page.locator('[data-slot="bulk-job"]')).toHaveCount(1);
  await page.goBack();
  await expect(access(page)).toContainText(`${email} is the user of this client.`);

  await openDialog(page);
  await confirm(page).click();
  await expect(ask(page)).toBeHidden();
  await expect(access(page)).toContainText("No invitation is open.");
  await expect(access(page).getByLabel("Email")).toBeVisible();
  await expect(access(page).getByRole("button", { name: "Remove the user" })).toHaveCount(0);
  // The instances and the webhooks do not change.
  expect(await hooks(page)).toEqual(before);
});

test("a failed read of the instances does not allow the removal", async ({ page }) => {
  const s = await seed();
  await openClient(page, s);

  await page.route(
    "**/api/v1/instances",
    (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/problem+json",
        body: JSON.stringify({ status: 503, code: "internal", detail: "down" }),
      }),
    { times: 1 },
  );
  const removes: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "DELETE" && new URL(r.url()).pathname.endsWith("/user")) removes.push("x");
  });
  await openDialog(page);
  await expect(ask(page).locator('[data-slot="failure"]')).toBeVisible();
  await expect(confirm(page)).toBeDisabled();
  await expect(ask(page)).not.toContainText(mainHook);
  await confirm(page).click({ force: true });
  expect(removes).toEqual([]);

  // The next open reads again.
  await ask(page).getByRole("button", { name: "Cancel" }).click();
  await expect(ask(page)).toBeHidden();
  await openDialog(page);
  await expect(item(page, "Main")).toContainText(mainHook);
  await expect(confirm(page)).toBeEnabled();
});

test("a 404: the client has no user now", async ({ page }) => {
  const s = await seed();
  await openClient(page, s);
  await openDialog(page);
  await expect(confirm(page)).toBeEnabled();

  // The user is removed in another tab. No event comes for it.
  const res = await page.request.delete(`/api/v1/clients/${clientOf(s, "Shop").id}/user`, {
    headers: { Origin: new URL(page.url()).origin },
  });
  expect(res.status()).toBe(204);

  await confirm(page).click();
  await expect(ask(page)).toBeHidden();
  await expect(access(page).getByRole("alert")).toContainText("This client has no user now.");
  await expect(access(page)).toContainText("No invitation is open.");
  // The button is gone, so the focus moves to the email field.
  await expect(access(page).getByLabel("Email")).toBeFocused();
});

test("the user cannot log in after the removal, and its session ends", async ({
  page,
  browser,
  baseURL,
}) => {
  const s = await seed();
  const shopper = person(s, "shopper");
  const other = await otherBrowser(browser, baseURL as string);
  await login(other, shopper);
  await expect(other).toHaveURL(/\/$/);
  await expect(other.getByRole("heading", { level: 1 })).toBeVisible();

  await openClient(page, s);
  await openDialog(page);
  await confirm(page).click();
  await expect(access(page)).toContainText("No invitation is open.");

  await other.reload();
  await expect(other).toHaveURL(/\/login(\?.*)?$/);
  await login(other, shopper);
  await expect(other.getByRole("alert")).toContainText("The email or the password is not correct.");
  await expect(other).toHaveURL(/\/login$/);
  await other.context().close();
});

test("an invitation of the same email after the removal gives the user back", async ({
  page,
  baseURL,
}) => {
  const s = await seed();
  const email = person(s, "shopper").email;
  await openClient(page, s);
  await openDialog(page);
  await confirm(page).click();
  await expect(access(page)).toContainText("No invitation is open.");

  await access(page).getByLabel("Email").fill(email);
  await access(page).getByRole("button", { name: "Send invitation" }).click();
  await expect(access(page)).toContainText(`Invitation sent to ${email}.`);

  const { body } = await control<{ to: string[]; paths: string[] }[]>("GET", "/mails");
  const path = body
    .filter((m) => m.to.includes(email))
    .flatMap((m) => m.paths.filter((p) => p.startsWith("/invite?token=")))
    .at(-1);
  const origin = new URL(baseURL as string).origin;
  const token = new URL(path ?? "", origin).searchParams.get("token");
  const res = await fetch(`${origin}/api/v1/invitations/accept`, {
    method: "POST",
    headers: { "content-type": "application/json", Origin: origin },
    body: JSON.stringify({ token, password: "a long and new password 42" }),
  });
  expect(res.status).toBeLessThan(300);

  await access(page).getByRole("button", { name: "Check again" }).click();
  await expect(access(page)).toContainText(`${email} is the user of this client.`);
  await expect(access(page).getByRole("button", { name: "Remove the user" })).toBeVisible();
});
