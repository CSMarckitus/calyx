import { randomInt } from "node:crypto";
import type { Page } from "@playwright/test";
import { control, expect, login, type Person, test } from "./support.ts";

// The clients, their limits and the invitation of their user.

// A refused value and a 404 are answers that the browser logs as failed requests.
test.use({ tolerateFailedRequests: true });

type Seeded = {
  clients: { name: string; id: string; path: string }[];
  users: (Person & { id: string; invite_path?: string })[];
  instances: { name: string; id: string; path: string }[];
};

async function seed(body: Record<string, unknown> = {}): Promise<Seeded> {
  const { status, body: answer } = await control<Seeded>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    users: [{ label: "owner", role: "owner" }],
    ...body,
  });
  expect(status).toBe(200);
  return answer;
}

const person = (s: Seeded, label: string) => s.users.find((u) => u.label === label) as Person;
const pathOf = (s: Seeded, name: string) => s.clients.find((c) => c.name === name)?.path as string;

const main = (page: Page) => page.getByRole("main");
const group = (page: Page, name: string) => main(page).getByRole("region", { name, exact: true });
const part = (page: Page, name: string) => main(page).getByRole("region", { name, exact: true });
const link = (page: Page) => part(page, "Access").locator('[data-slot="invite-link"]');
const newEmail = () => `ana-${randomInt(1e9)}@bakery.example`;

// Logs in and waits for the first screen, so a next `goto` does not cut the login.
async function enter(page: Page, who: Person) {
  await login(page, who);
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
}

// The invitation links that the fake mail server got for one address.
async function mailed(email: string): Promise<string[]> {
  const { body } = await control<{ to: string[]; paths: string[] }[]>("GET", "/mails");
  return body
    .filter((m) => m.to.includes(email))
    .flatMap((m) => m.paths.filter((p) => p.startsWith("/invite?token=")));
}

// The status of `lookupInvitation` for the token of a link: 200 valid, 410 replaced or cancelled.
async function lookup(page: Page, href: string): Promise<number> {
  const token = new URL(href, "http://localhost").searchParams.get("token");
  const res = await page.request.post("/api/v1/invitations/lookup", {
    data: { token },
    headers: { Origin: new URL(page.url()).origin },
  });
  return res.status();
}

async function mailOff() {
  await control("POST", "/backend", { action: "stop" });
  await control("POST", "/backend", { action: "start", mail: false });
}

for (const mail of [true, false]) {
  test(`${mail ? "with mail" : "with mail off"}: add a client, invite its user, send again and cancel`, async ({
    page,
  }) => {
    if (!mail) await mailOff();
    const s = await seed();
    await enter(page, person(s, "owner"));
    await expect(main(page).getByRole("heading", { name: "Instances" })).toBeVisible();

    await main(page).getByRole("button", { name: "Add a client" }).click();
    const ask = page.getByRole("dialog", { name: "Add a client" });
    const can = ask.getByLabel("Can this client connect numbers?");
    await expect(can.locator("option:checked")).toHaveText(
      "Yes, up to 1 for now. You can change it later in Limits.",
    );
    await ask.getByLabel("Name").fill("Bakery");
    await ask.getByRole("button", { name: "Add client" }).click();

    await expect(page).toHaveURL(/\/clients\/[0-9a-f-]{36}$/);
    await expect(main(page).getByRole("heading", { name: "Bakery", level: 1 })).toBeVisible();
    const limits = part(page, "Limits");
    await expect(limits.getByLabel("Can this client connect numbers?")).toHaveValue("yes");
    await expect(limits.getByLabel("Number of instances")).toHaveValue("1");

    const access = part(page, "Access");
    await expect(access).toContainText("No invitation is open.");
    await expect(access).not.toContainText("cannot connect a number yet");
    const email = newEmail();
    await access.getByLabel("Email").fill(email);
    await access.getByRole("button", { name: "Send invitation" }).click();
    await expect(access).toContainText(`Invitation sent to ${email}.`);
    await expect(access).toContainText(/It expires on \d{1,2} [A-Z][a-z]+( \d{4})?, \d\d:\d\d\./);
    await expect(access.locator('[translate="no"]', { hasText: email })).toBeVisible();

    let current: string;
    if (mail) {
      await expect.poll(() => mailed(email)).toHaveLength(1);
      const [first] = await mailed(email);
      await expect(link(page)).toHaveCount(0);
      await expect(access).toContainText("If the mail did not arrive, ask them to check spam.");
      await access.getByRole("button", { name: "Send again" }).click();
      await expect.poll(() => mailed(email)).toHaveLength(2);
      current = (await mailed(email))[1] as string;
      // A new invitation replaces the old one.
      expect(await lookup(page, first as string)).toBe(410);
      expect(await lookup(page, current)).toBe(200);
    } else {
      // No mail: the link shows, to copy, once.
      await expect(link(page)).toContainText("/invite?token=");
      const first = (await link(page).textContent()) as string;
      expect(await lookup(page, first)).toBe(200);
      await expect(access.getByRole("button", { name: "Copy the link" })).toBeVisible();
      await expect(access).not.toContainText("check spam");
      expect(await mailed(email)).toEqual([]);

      await page.reload();
      await expect(access).toContainText(`Invitation sent to ${email}.`);
      await expect(link(page)).toHaveCount(0);
      await expect(access.getByRole("button", { name: "Send again" })).toHaveCount(0);
      await access.getByRole("button", { name: "Make a new link" }).click();
      await expect(link(page)).toContainText("/invite?token=");
      current = (await link(page).textContent()) as string;
      expect(current).not.toBe(first);
      expect(await lookup(page, first)).toBe(410);
      expect(await lookup(page, current)).toBe(200);
    }

    await access.getByRole("button", { name: "Cancel invitation" }).click();
    await expect(access).toContainText("No invitation is open.");
    await expect(link(page)).toHaveCount(0);
    expect(await lookup(page, current)).toBe(410);

    // The list has the new client, with no number, and the way back to it.
    await main(page).getByRole("link", { name: "All instances" }).click();
    // The card takes the place of "No numbers yet."
    await expect(
      group(page, "Bakery").getByRole("button", { name: "+ Connect a number" }),
    ).toBeVisible();
    await group(page, "Bakery").getByRole("link", { name: "Manage client" }).click();
    await expect(main(page).getByRole("heading", { name: "Bakery", level: 1 })).toBeVisible();
  });
}

test("Add a client: No, a name that is not valid, and Cancel", async ({ page }) => {
  const s = await seed();
  await enter(page, person(s, "owner"));
  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/clients") sent.push("x");
  });

  await main(page).getByRole("button", { name: "Add a client" }).click();
  const ask = page.getByRole("dialog", { name: "Add a client" });
  await ask.getByLabel("Name").fill("Kiosk");
  await ask.getByRole("button", { name: "Cancel" }).click();
  await expect(ask).toBeHidden();
  expect(sent).toEqual([]);

  await main(page).getByRole("button", { name: "Add a client" }).click();
  await ask.getByLabel("Name").fill("   ");
  await ask.getByRole("button", { name: "Add client" }).click();
  await expect(ask.getByLabel("Name")).toHaveAttribute("aria-invalid", "true");
  await expect(ask).toContainText("Use 1 to 100 characters.");
  await ask.getByLabel("Name").fill("Kiosk");
  await ask.getByLabel("Can this client connect numbers?").selectOption("no");
  await ask.getByRole("button", { name: "Add client" }).click();
  await expect(main(page).getByRole("heading", { name: "Kiosk", level: 1 })).toBeVisible();
  await expect(part(page, "Limits").getByLabel("Can this client connect numbers?")).toHaveValue(
    "no",
  );
});

test("the limits: saved, kept after a reload, and the list follows them", async ({ page }) => {
  const s = await seed({
    clients: [{ name: "Shop", can_create_instances: true }],
    instances: [{ name: "Main", client: "Shop", provider: "WHATSAPP_WEB", status: "CONNECTED" }],
  });
  await enter(page, person(s, "owner"));
  await expect(
    group(page, "Shop").getByRole("button", { name: "+ Connect a number" }),
  ).toBeVisible();
  await group(page, "Shop").getByRole("link", { name: "Manage client" }).click();

  const limits = part(page, "Limits");
  await expect(limits.getByLabel("Number of instances")).toHaveValue("0");
  await expect(limits).toContainText("0 means no limit of its own.");
  await limits.getByLabel("Number of instances").fill("10001");
  await limits.getByRole("button", { name: "Save limits" }).click();
  await expect(limits.getByLabel("Number of instances")).toHaveAttribute("aria-invalid", "true");
  await expect(limits).toContainText("Use a number from 0 to 10000.");

  await limits.getByLabel("Number of instances").fill("1");
  await limits.getByLabel("Can this client connect numbers?").selectOption("no");
  await limits.getByRole("button", { name: "Save limits" }).click();
  await expect(limits).toContainText("The limits are saved.");
  await page.reload();
  await expect(limits.getByLabel("Number of instances")).toHaveValue("1");
  await expect(limits.getByLabel("Can this client connect numbers?")).toHaveValue("no");

  // One number of a limit of 1: no free place.
  await main(page).getByRole("link", { name: "All instances" }).click();
  await expect(group(page, "Shop")).toContainText("There is no free place for a number.");
  await expect(group(page, "Shop").getByRole("button", { name: "+ Connect a number" })).toHaveCount(
    0,
  );
});

test("the default client shows only its limit", async ({ page }) => {
  const s = await seed();
  await enter(page, person(s, "owner"));
  await group(page, "My numbers").getByRole("link", { name: "Manage client" }).click();
  await expect(main(page).getByRole("heading", { name: "My numbers", level: 1 })).toBeVisible();
  await expect(part(page, "Access")).toHaveCount(0);
  await expect(main(page).getByLabel("Can this client connect numbers?")).toHaveCount(0);
  await expect(main(page).getByRole("button", { name: "Delete this client" })).toHaveCount(0);

  const limits = part(page, "Limits");
  await limits.getByLabel("Number of instances").fill("3");
  await limits.getByRole("button", { name: "Save limits" }).click();
  await expect(limits).toContainText("The limits are saved.");
  await page.reload();
  await expect(limits.getByLabel("Number of instances")).toHaveValue("3");
});

test("a client with a user shows the user, and no invitation", async ({ page }) => {
  const s = await seed({
    clients: [{ name: "Shop" }],
    users: [
      { label: "owner", role: "owner" },
      { label: "shopper", role: "client", client: "Shop" },
    ],
  });
  await enter(page, person(s, "owner"));
  await page.goto(pathOf(s, "Shop"));
  const access = part(page, "Access");
  await expect(access).toContainText(`${person(s, "shopper").email} is the user of this client.`);
  await expect(access.getByLabel("Email")).toHaveCount(0);
  await expect(access.getByRole("button", { name: "Send invitation" })).toHaveCount(0);
  await expect(access.getByRole("button", { name: "Cancel invitation" })).toHaveCount(0);
});

test("a pending invitation from before, and a refused email", async ({ page }) => {
  const s = await seed({
    clients: [{ name: "Bakery" }, { name: "Kiosk" }],
    users: [
      { label: "owner", role: "owner" },
      { label: "invitee", role: "client", client: "Bakery", pending: true },
    ],
  });
  await enter(page, person(s, "owner"));
  await page.goto(pathOf(s, "Bakery"));
  const access = part(page, "Access");
  await expect(access).toContainText(`Invitation sent to ${person(s, "invitee").email}.`);
  await expect(access.getByRole("button", { name: "Send again" })).toBeVisible();
  await expect(access.getByRole("button", { name: "Cancel invitation" })).toBeVisible();

  await page.goto(pathOf(s, "Kiosk"));
  await access.getByLabel("Email").fill("not-an-email");
  await access.getByRole("button", { name: "Send invitation" }).click();
  await expect(access.getByLabel("Email")).toHaveAttribute("aria-invalid", "true");
  await expect(access).toContainText("Write an email address.");
  await expect(access).toContainText("No invitation is open.");
});

test("before the acknowledgement, the invitation warns that the client cannot connect a number yet", async ({
  page,
}) => {
  const s = await seed({
    organization: { name: "Acme", acknowledged: false },
    clients: [{ name: "Shop", can_create_instances: true }],
  });
  // The Instances screen shows only the terms notice, so it has no heading to wait for.
  await login(page, person(s, "owner"));
  await expect(page.locator('[data-slot="terms-notice"]')).toBeVisible();
  await page.goto(pathOf(s, "Shop"));
  const access = part(page, "Access");
  await expect(access).toContainText("This client cannot connect a number yet.");
  await access.getByRole("link", { name: "Read the WhatsApp Web terms" }).click();
  await expect(page).toHaveURL(/\/organization\/whatsapp-web$/);
});

test("Delete this client: only with no instance", async ({ page }) => {
  const s = await seed({
    clients: [{ name: "Shop" }, { name: "Empty" }],
    instances: [{ name: "Main", client: "Shop", provider: "WHATSAPP_WEB", status: "CONNECTED" }],
  });
  await enter(page, person(s, "owner"));
  await page.goto(pathOf(s, "Shop"));
  await expect(main(page).getByRole("heading", { name: "Shop", level: 1 })).toBeVisible();
  await expect(main(page).getByRole("button", { name: "Delete this client" })).toHaveCount(0);
  await expect(main(page)).toContainText("To delete this client, remove its numbers first.");

  await page.goto(pathOf(s, "Empty"));
  await main(page).getByRole("button", { name: "Delete this client" }).click();
  const ask = page.getByRole("dialog", { name: "Delete Empty?" });
  await ask.getByRole("button", { name: "Cancel" }).click();
  await expect(main(page).getByRole("heading", { name: "Empty", level: 1 })).toBeVisible();
  await main(page).getByRole("button", { name: "Delete this client" }).click();
  await ask.getByRole("button", { name: "Delete" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(group(page, "Shop")).toBeVisible();
  await expect(group(page, "Empty")).toHaveCount(0);
});

test("a client user, and an unknown client, get 'This page does not exist'", async ({ page }) => {
  const s = await seed({
    clients: [{ name: "Shop" }],
    users: [
      { label: "owner", role: "owner" },
      { label: "shopper", role: "client", client: "Shop" },
    ],
  });
  await enter(page, person(s, "shopper"));
  await page.goto(pathOf(s, "Shop"));
  await expect(page.getByText("This page does not exist")).toBeVisible();

  await page.context().clearCookies();
  await enter(page, person(s, "owner"));
  await page.goto("/clients/00000000-0000-4000-8000-000000000000");
  await expect(page.getByText("This page does not exist")).toBeVisible();
});

// No event comes when the person accepts, so the owner asks for the read.
test("a pending invitation: Check again shows the user after the person accepts", async ({
  page,
  baseURL,
}) => {
  const s = await seed({
    clients: [{ name: "Bakery" }],
    users: [
      { label: "owner", role: "owner" },
      { label: "invitee", role: "client", client: "Bakery", pending: true },
    ],
  });
  const invitee = s.users.find((u) => u.label === "invitee");
  await enter(page, person(s, "owner"));
  await page.goto(pathOf(s, "Bakery"));
  const access = part(page, "Access");
  await expect(access).toContainText(`Invitation sent to ${invitee?.email}.`);

  // The person accepts in another browser, so the session of the owner stays.
  const origin = new URL(baseURL as string).origin;
  const token = new URL(invitee?.invite_path ?? "", origin).searchParams.get("token");
  const res = await fetch(`${origin}/api/v1/invitations/accept`, {
    method: "POST",
    headers: { "content-type": "application/json", Origin: origin },
    body: JSON.stringify({ token, password: "a long and new password 42" }),
  });
  expect(res.status).toBeLessThan(300);
  await expect(access).toContainText(`Invitation sent to ${invitee?.email}.`);

  await access.getByRole("button", { name: "Check again" }).click();
  await expect(access).toContainText(`${invitee?.email} is the user of this client.`);
});

// One change at a time: "Send again" and "Cancel invitation" disable each other while one runs.
test("a pending invitation: Send again and Cancel invitation do not run at once", async ({
  page,
}) => {
  const s = await seed({
    clients: [{ name: "Bakery" }],
    users: [
      { label: "owner", role: "owner" },
      { label: "invitee", role: "client", client: "Bakery", pending: true },
    ],
  });
  await enter(page, person(s, "owner"));
  await page.goto(pathOf(s, "Bakery"));
  const access = part(page, "Access");
  const again = access.getByRole("button", { name: "Send again" });
  const cancel = access.getByRole("button", { name: "Cancel invitation" });
  await expect(again).toBeEnabled();

  // Each change waits 1.5 s, so the test sees the other button while it runs.
  await page.route("**/api/v1/clients/*/invitations", async (route) => {
    await new Promise((r) => setTimeout(r, 1500));
    await route.continue().catch(() => undefined);
  });
  await again.click();
  await expect(cancel).toBeDisabled();
  await expect(again).toBeEnabled({ timeout: 10_000 });
  await expect(cancel).toBeEnabled();

  await cancel.click();
  await expect(again).toBeDisabled();
  await expect(access).toContainText("No invitation is open.", { timeout: 10_000 });
});
