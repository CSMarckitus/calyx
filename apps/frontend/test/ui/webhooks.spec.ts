import { randomInt } from "node:crypto";
import { type Locator, type Page, type Request, type Route, request } from "@playwright/test";
import { control, coreUpdates, expect, login, type Person, test } from "./support.ts";

// The webhooks view of an instance, the rows of its card, and "Only send" with webhooks.
// A 504, a lost answer and an aborted request are failed requests in
// the browser log.
test.use({ tolerateFailedRequests: true });

type Hook = { event: string; url: string; secret?: string };
type Seeded = {
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

// One connected WhatsApp Web instance of the client "Shop".
const main = (webhooks: Hook[] = [], extra: Record<string, unknown> = {}) => ({
  name: "Main",
  client: "Shop",
  provider: "WHATSAPP_WEB",
  status: "CONNECTED",
  webhooks,
  ...extra,
});

const person = (s: Seeded, label: string) => s.users.find((u) => u.label === label) as Person;
const idOf = (s: Seeded, name: string) => s.instances.find((i) => i.name === name)?.id as string;

const n8n = "https://n8n.example/webhook/calyx";
const other = "https://hooks.example/in";
const label: Record<string, string> = {
  "connection.update": "The connection changes",
  "message.received": "A message arrives",
  "message.status": "A sent message changes",
  "bulk.finished": "A bulk send ends",
};

const question = "Did you save it? You cannot see it again.";
const refusal =
  "Calyx cannot show the stored secret. Type it again, generate a new one, or select No signature.";
const unconfirmed =
  "Calyx did not confirm the save, and saved nothing. The core can already use the new webhooks. Select Save webhooks again, so that Calyx and the core have the same list.";
const changedMeanwhile = "Somebody changed the webhooks of this instance while you edited.";

// The drawer, and not the card of services, which is a dialog too.
const drawer = (page: Page) => page.locator('[role="dialog"][data-slot="sheet-content"]');
const card = (page: Page) => drawer(page).locator('[data-slot="instance"]');
// The view, in the drawer or in place of the card of a client user with one instance.
const view = (page: Page) => page.locator('[data-slot="webhooks"]');
// A modal drawer hides the list from the accessibility tree, so the cell has a CSS locator.
const cell = (page: Page, name: string) =>
  page
    .locator("main")
    .locator('[data-slot="instance"]', { has: page.getByText(name, { exact: true }) });
// The value of a row of the card.
const fact = (where: Locator, name: string) =>
  where
    .locator("dt", { hasText: new RegExp(`^${name}$`) })
    .locator("xpath=following-sibling::dd[1]");

const box = (page: Page, name: string) => view(page).getByRole("checkbox", { name, exact: true });
// One event of the form, with its stored state ("Signed" or "Not signed").
const hook = (page: Page, name: string) =>
  view(page).locator('[data-slot="hook"]', {
    has: page.getByRole("checkbox", { name, exact: true }),
  });
const secret = (page: Page) => view(page).getByRole("group", { name: "Secret", exact: true });
const saveButton = (page: Page) => view(page).getByRole("button", { name: "Save webhooks" });
const generated = (page: Page) => view(page).getByLabel("Generated secret");
const saved = (page: Page) => view(page).getByText("Saved.", { exact: true });
const rawSection = (page: Page) => view(page).getByRole("button", { name: /^Raw WhatsApp events/ });

// The link "Edit" of the row "Webhooks". The check first fails fast when the row is absent.
async function edit(where: Locator): Promise<void> {
  const link = fact(where, "Webhooks").getByRole("link", { name: "Edit" });
  await expect(link).toBeVisible();
  await link.click();
}

async function openView(page: Page, who: Person, id: string): Promise<void> {
  await login(page, who);
  // Waits for the first screen, so the next `goto` does not cut the login.
  await expect(page.locator("main").locator('[data-slot="instance"]').first()).toBeVisible();
  await page.goto(`/instances/${id}/webhooks`);
  await expect(view(page).getByRole("heading", { name: "Webhooks" })).toBeVisible();
}

const isPatch = (id: string) => (r: Request) =>
  r.method() === "PATCH" && new URL(r.url()).pathname === `/api/v1/instances/${id}`;

// The changes that the page sent for the instance.
function watchPatches(page: Page, id: string): Request[] {
  const sent: Request[] = [];
  page.on("request", (r) => {
    if (isPatch(id)(r)) sent.push(r);
  });
  return sent;
}

async function save(page: Page, id: string) {
  const answer = page.waitForResponse((r) => isPatch(id)(r.request()));
  await saveButton(page).click();
  return answer;
}

// The next change of the instance goes to `handle`. Each later request goes to the backend.
async function onNextPatch(page: Page, id: string, handle: (route: Route) => Promise<void>) {
  let done = false;
  await page.route(`**/api/v1/instances/${id}`, (route) => {
    if (route.request().method() !== "PATCH" || done) return route.fallback();
    done = true;
    return handle(route);
  });
}

// The backend makes the change, and the browser loses its answer.
async function loseAnswer(route: Route): Promise<void> {
  await route.fetch();
  await route.abort("connectionreset");
}

// A second login, outside the browser: a second person, or a program.
async function api(baseURL: string, who: Person) {
  const context = await request.newContext({
    baseURL,
    extraHTTPHeaders: {
      Origin: new URL(baseURL).origin,
      "X-Forwarded-For": `10.${randomInt(256)}.${randomInt(256)}.${randomInt(1, 255)}`,
    },
  });
  const res = await context.post("/api/v1/auth/login", {
    data: { email: who.email, password: who.password },
  });
  expect(res.ok()).toBe(true);
  return context;
}

type StoredHook = { event: string; url: string; has_secret: boolean };
async function storedHooks(page: Page, id: string): Promise<StoredHook[]> {
  const res = await page.request.get(`/api/v1/instances/${id}`);
  expect(res.status()).toBe(200);
  const hooks = ((await res.json()) as { webhooks: StoredHook[] }).webhooks;
  return hooks.map(({ event, url, has_secret }) => ({ event, url, has_secret }));
}

async function lastCoreUpdate(id: string) {
  const updates = await coreUpdates(id);
  expect(updates.length).toBeGreaterThan(0);
  return updates[updates.length - 1];
}

const byEvent = (a: { event: string }, b: { event: string }) => a.event.localeCompare(b.event);

// After "Saved.", every secret control is back at "Keep the secret", and no generated secret shows.
async function expectFreshForm(page: Page): Promise<void> {
  const keep = view(page).getByRole("radio", { name: "Keep the secret" });
  await expect(keep.first()).toBeVisible();
  for (const radio of await keep.all()) await expect(radio).toBeChecked();
  await expect(generated(page)).toHaveCount(0);
}

test("the rows of the card: Instance ID with Copy, and Webhooks with its count and Edit", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const s = await seed([
    main([
      { event: "message.received", url: n8n },
      { event: "message.status", url: n8n },
    ]),
  ]);
  const id = idOf(s, "Main");
  await login(page, person(s, "owner"));
  await cell(page, "Main").getByRole("link").click();
  await expect(page).toHaveURL(`/instances/${id}`);

  await expect(fact(card(page), "Instance ID")).toContainText(id);
  await fact(card(page), "Instance ID").getByRole("button", { name: "Copy", exact: true }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(id);

  await expect(fact(card(page), "Webhooks")).toContainText("2 events");
  await edit(card(page));
  await expect(page).toHaveURL(`/instances/${id}/webhooks`);
  await expect(page.getByRole("dialog", { name: "Webhooks of Main" })).toBeVisible();
  await expect(drawer(page).getByRole("heading", { name: "Webhooks", level: 2 })).toBeFocused();
});

test("an instance that only sends, and an instance that is not active: the row and the view", async ({
  page,
}) => {
  const s = await seed([
    main([], { name: "Sender", send_only: true }),
    main([{ event: "message.received", url: n8n }], { name: "Lost", state: "missing" }),
  ]);
  await login(page, person(s, "owner"));

  await cell(page, "Sender").getByRole("link").click();
  await expect(fact(card(page), "Webhooks")).toHaveText("None: this number only sends");
  await expect(fact(card(page), "Webhooks").getByRole("link")).toHaveCount(0);
  await page.goto(`/instances/${idOf(s, "Sender")}/webhooks`);
  await expect(view(page)).toContainText(
    "This instance only sends, so it has no webhooks. To use webhooks, change Use to Send and receive.",
  );
  await expect(saveButton(page)).toHaveCount(0);

  await page.goto(`/instances/${idOf(s, "Lost")}`);
  await expect(fact(card(page), "Webhooks")).toContainText("1 event");
  await expect(fact(card(page), "Webhooks").getByRole("link")).toHaveCount(0);
  await page.goto(`/instances/${idOf(s, "Lost")}/webhooks`);
  await expect(view(page)).toContainText(
    "This instance is not ready, so its webhooks cannot change now.",
  );
  // Read-only: the stored webhook shows, and nothing can save.
  await expect(view(page).getByText(label["message.received"] as string)).toBeVisible();
  await expect(saveButton(page)).toHaveCount(0);
});

test("webhooks with one secret", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const s = await seed([main()]);
  const id = idOf(s, "Main");
  await login(page, person(s, "owner"));
  await cell(page, "Main").getByRole("link").click();
  await edit(card(page));
  await expect(page).toHaveURL(`/instances/${id}/webhooks`);

  await expect(view(page).getByRole("button", { name: /^One URL for all/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await view(page).getByLabel("URL", { exact: true }).fill(n8n);
  await box(page, "A message arrives").check();
  await box(page, "A sent message changes").check();

  await secret(page).getByRole("radio", { name: "Generate a secret" }).click();
  await expect(view(page).getByText("Put it in your program first")).toBeVisible();
  await expect(generated(page)).toHaveValue(/^[0-9a-f]{64}$/);
  const value = await generated(page).inputValue();
  await view(page).getByRole("button", { name: "Copy", exact: true }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(value);

  await expect(saveButton(page)).toBeDisabled();
  await view(page).getByRole("button", { name: "I saved it" }).click();
  expect((await save(page, id)).status()).toBe(200);
  await expect(saved(page)).toBeVisible();
  await expect(hook(page, "A message arrives")).toContainText("Signed");
  await expect(hook(page, "A sent message changes")).toContainText("Signed");
  await expectFreshForm(page);

  const last = await lastCoreUpdate(id);
  expect(last.webhooks?.sort(byEvent)).toEqual([
    { event: "message.received", url: n8n, secret: value },
    { event: "message.status", url: n8n, secret: value },
  ]);
  // The WhatsApp session does not reconnect.
  expect(last.enabled).toBeNull();
});

test("a new event beside signed events needs a choice for the secret", async ({ page }) => {
  const s = await seed([
    main([
      { event: "message.received", url: n8n, secret: "old-secret" },
      { event: "message.status", url: n8n, secret: "old-secret" },
    ]),
  ]);
  const id = idOf(s, "Main");
  const sent = watchPatches(page, id);
  await openView(page, person(s, "owner"), id);
  await expect(hook(page, "A message arrives")).toContainText("Signed");
  await expect(hook(page, "A sent message changes")).toContainText("Signed");

  await box(page, "The connection changes").check();
  await saveButton(page).click();
  await expect(secret(page)).toContainText(refusal);
  await expect(secret(page).getByRole("radio", { name: "Keep the secret" })).toBeDisabled();
  expect(sent).toHaveLength(0);

  await secret(page).getByRole("radio", { name: "Type a secret" }).click();
  await secret(page).getByLabel("Your secret").fill("new-secret");
  expect((await save(page, id)).status()).toBe(200);
  await expect(saved(page)).toBeVisible();
  for (const name of ["The connection changes", "A message arrives", "A sent message changes"]) {
    await expect(hook(page, name)).toContainText("Signed");
    await expect(hook(page, name)).not.toContainText("Not signed");
  }
  const last = await lastCoreUpdate(id);
  expect(last.webhooks?.map((h) => h.secret)).toEqual(["new-secret", "new-secret", "new-secret"]);
});

test("Only send on an instance with webhooks, and the list follows the stream", async ({
  page,
  baseURL,
}) => {
  const s = await seed([
    main([
      { event: "message.received", url: n8n },
      { event: "message.status", url: other },
    ]),
  ]);
  const id = idOf(s, "Main");
  const sent = watchPatches(page, id);
  await login(page, person(s, "owner"));
  await cell(page, "Main").getByRole("link").click();
  const use = card(page).getByRole("combobox", { name: "Use" });
  await expect(use).toContainText("Send and receive");

  await use.click();
  await page.getByRole("option", { name: "Only send" }).click();
  const ask = page.getByRole("dialog").filter({ hasText: "Calyx stops calling these addresses." });
  await expect(ask).toContainText(n8n);
  await expect(ask).toContainText(other);
  expect(sent).toHaveLength(0);

  // A program adds a webhook while the dialog is open.
  const program = await api(baseURL as string, person(s, "shopper"));
  const third = "https://third.example/hook";
  const res = await program.patch(`/api/v1/instances/${id}`, {
    data: {
      webhooks: [
        { event: "message.received", url: n8n },
        { event: "message.status", url: other },
        { event: "connection.update", url: third },
      ],
    },
  });
  expect(res.status()).toBe(200);
  await program.dispose();
  await expect(ask).toContainText(third);

  const change = page.waitForRequest(isPatch(id));
  await ask.getByRole("button", { name: "Remove the webhooks and only send" }).click();
  expect((await change).postDataJSON()).toEqual({ send_only: true, webhooks: [] });
  await expect(ask).toBeHidden();
  await expect(use).toContainText("Only send");
  await expect(fact(card(page), "Webhooks")).toHaveText("None: this number only sends");
  expect(sent).toHaveLength(1);
  const last = await lastCoreUpdate(id);
  expect(last).toEqual({ webhooks: [], enabled: null, send_only: true });
});

test("a 504 keeps the form and the generated secret, and nothing is sent again", async ({
  page,
}) => {
  const s = await seed([main([{ event: "message.received", url: n8n }])]);
  const id = idOf(s, "Main");
  const sent = watchPatches(page, id);
  await openView(page, person(s, "owner"), id);
  await box(page, "A sent message changes").check();
  await secret(page).getByRole("radio", { name: "Generate a secret" }).click();
  const value = await generated(page).inputValue();
  await view(page).getByRole("button", { name: "I saved it" }).click();

  await control("POST", "/fault", { method: "UpdateInstanceConfig", code: "UNAVAILABLE" });
  const first = await save(page, id);
  expect(first.status()).toBe(504);
  expect(((await first.json()) as { code: string }).code).toBe("change_outcome_unknown");
  await expect(view(page)).toContainText(unconfirmed);
  await expect(saved(page)).toHaveCount(0);
  await expect(box(page, "A sent message changes")).toBeChecked();
  expect(sent).toHaveLength(1);

  await control("POST", "/fault", { method: "UpdateInstanceConfig" });
  expect((await save(page, id)).status()).toBe(200);
  await expect(saved(page)).toBeVisible();
  await expect(view(page).getByText(changedMeanwhile)).toHaveCount(0);
  expect(sent).toHaveLength(2);
  // The second save sends the same generated secret.
  expect(sent[1]?.postDataJSON()).toEqual(sent[0]?.postDataJSON());
  const body = sent[1]?.postDataJSON() as { webhooks: { secret?: string }[] };
  expect(body.webhooks.map((h) => h.secret)).toEqual([value, value]);
});

test("a raw event that a program set keeps its secret after a save of another event", async ({
  page,
}) => {
  const s = await seed([
    main([
      { event: "message.received", url: n8n },
      { event: "whatsmeow.Receipt", url: other, secret: "sdk-secret" },
    ]),
  ]);
  const id = idOf(s, "Main");
  await openView(page, person(s, "owner"), id);
  // Two URLs: the view opens in "One URL for each event".
  await expect(view(page).getByRole("button", { name: /^One URL for each event/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  await rawSection(page).click();
  await expect(box(page, "Receipt")).toBeChecked();
  await expect(hook(page, "Receipt")).toContainText("Signed");
  await expect(hook(page, "A message arrives")).toContainText("Not signed");

  await box(page, "A sent message changes").check();
  // The row is found by the words of its event. Its URL field has the event id in its name.
  await hook(page, "A sent message changes").getByLabel("URL for message.status").fill(n8n);
  expect((await save(page, id)).status()).toBe(200);
  await expect(saved(page)).toBeVisible();
  await expectFreshForm(page);

  expect((await storedHooks(page, id)).sort(byEvent)).toEqual([
    { event: "message.received", url: n8n, has_secret: false },
    { event: "message.status", url: n8n, has_secret: false },
    { event: "whatsmeow.Receipt", url: other, has_secret: true },
  ]);
  const last = await lastCoreUpdate(id);
  expect(last.webhooks?.sort(byEvent)).toEqual([
    { event: "message.received", url: n8n, secret: null },
    { event: "message.status", url: n8n, secret: null },
    { event: "whatsmeow.Receipt", url: other, secret: "sdk-secret" },
  ]);
});

test("a save of webhooks does not reconnect the instance", async ({ page }) => {
  const s = await seed([main([{ event: "message.received", url: n8n }])]);
  const id = idOf(s, "Main");
  await openView(page, person(s, "owner"), id);
  await box(page, "The connection changes").check();
  expect((await save(page, id)).status()).toBe(200);
  await expect(saved(page)).toBeVisible();

  const updates = await coreUpdates(id);
  expect(updates).toHaveLength(1);
  expect(updates[0]?.enabled).toBeNull();
  expect(updates[0]?.send_only).toBeNull();
  expect(updates[0]?.webhooks?.map((h) => h.event).sort()).toEqual([
    "connection.update",
    "message.received",
  ]);
});

test("a change by another person stops the save, and Load the new list replaces the form", async ({
  page,
  baseURL,
}) => {
  const s = await seed([main([{ event: "message.received", url: n8n }])]);
  const id = idOf(s, "Main");
  const sent = watchPatches(page, id);
  await openView(page, person(s, "owner"), id);
  await box(page, "A sent message changes").check();

  const second = await api(baseURL as string, person(s, "shopper"));
  const res = await second.patch(`/api/v1/instances/${id}`, {
    data: {
      webhooks: [
        { event: "message.received", url: n8n },
        { event: "connection.update", url: n8n },
      ],
    },
  });
  expect(res.status()).toBe(200);
  await second.dispose();

  await saveButton(page).click();
  await expect(view(page)).toContainText(changedMeanwhile);
  expect(sent).toHaveLength(0);

  await view(page).getByRole("button", { name: "Load the new list" }).click();
  const ask = page.getByRole("dialog", { name: "Load the new list?" });
  await ask.getByRole("button", { name: "Load the new list" }).click();
  await expect(ask).toBeHidden();
  await expect(box(page, "The connection changes")).toBeChecked();
  await expect(box(page, "A message arrives")).toBeChecked();
  await expect(box(page, "A sent message changes")).not.toBeChecked();
  await expect(view(page).getByText(changedMeanwhile)).toHaveCount(0);
  expect(sent).toHaveLength(0);
});

test("a second save after a confirmed save goes through", async ({ page }) => {
  const s = await seed([main([{ event: "message.received", url: n8n }])]);
  const id = idOf(s, "Main");
  await openView(page, person(s, "owner"), id);
  await box(page, "A sent message changes").check();
  expect((await save(page, id)).status()).toBe(200);
  await expect(saved(page)).toBeVisible();

  await box(page, "The connection changes").check();
  expect((await save(page, id)).status()).toBe(200);
  await expect(saved(page)).toBeVisible();
  await expect(view(page).getByText(changedMeanwhile)).toHaveCount(0);
  expect((await lastCoreUpdate(id)).webhooks).toHaveLength(3);
});

test("no answer, and the change applied: Saved. after the read", async ({ page }) => {
  const s = await seed([main([{ event: "message.received", url: n8n }])]);
  const id = idOf(s, "Main");
  const sent = watchPatches(page, id);
  await openView(page, person(s, "owner"), id);
  await box(page, "A sent message changes").check();
  await onNextPatch(page, id, loseAnswer);
  await saveButton(page).click();
  await expect(saved(page)).toBeVisible();
  await expect(view(page).getByText(unconfirmed)).toHaveCount(0);
  // The page read the instance, and did not send the change again.
  expect(sent).toHaveLength(1);
  await expect(hook(page, "A sent message changes")).toContainText("Not signed");
});

test("no answer, and the change applies late: the next save goes on with the new edit", async ({
  page,
  baseURL,
}) => {
  const s = await seed([main([{ event: "message.received", url: n8n }])]);
  const id = idOf(s, "Main");
  const sent = watchPatches(page, id);
  await openView(page, person(s, "owner"), id);
  await box(page, "A sent message changes").check();

  let kept: Request | undefined;
  await onNextPatch(page, id, async (route) => {
    kept = route.request();
    await route.abort("connectionreset");
  });
  await saveButton(page).click();
  await expect(view(page)).toContainText(unconfirmed);
  expect(sent).toHaveLength(1);

  // The kept request reaches the backend late, outside the page.
  const late = await page.request.fetch(kept?.url() as string, {
    method: "PATCH",
    headers: { Origin: new URL(baseURL as string).origin, "content-type": "application/json" },
    data: kept?.postDataJSON(),
  });
  expect(late.status()).toBe(200);

  await box(page, "The connection changes").check();
  expect((await save(page, id)).status()).toBe(200);
  await expect(saved(page)).toBeVisible();
  await expect(view(page).getByText(changedMeanwhile)).toHaveCount(0);
  expect(sent).toHaveLength(2);
  expect((await lastCoreUpdate(id)).webhooks?.map((h) => h.event).sort()).toEqual([
    "connection.update",
    "message.received",
    "message.status",
  ]);
});

test("a new secret on a signed webhook with no answer is not confirmed", async ({ page }) => {
  const s = await seed([main([{ event: "message.received", url: n8n, secret: "old-secret" }])]);
  const id = idOf(s, "Main");
  const sent = watchPatches(page, id);
  await openView(page, person(s, "owner"), id);
  await secret(page).getByRole("radio", { name: "Type a secret" }).click();
  await secret(page).getByLabel("Your secret").fill("new-secret");

  await onNextPatch(page, id, loseAnswer);
  await saveButton(page).click();
  await expect(view(page)).toContainText(unconfirmed);
  await expect(saved(page)).toHaveCount(0);
  expect(sent).toHaveLength(1);

  // A second save with the same secret is safe, and its 200 confirms it.
  expect((await save(page, id)).status()).toBe(200);
  await expect(saved(page)).toBeVisible();
  expect(sent).toHaveLength(2);
  expect((await lastCoreUpdate(id)).webhooks).toEqual([
    { event: "message.received", url: n8n, secret: "new-secret" },
  ]);
});

test("Save webhooks waits for I saved it, and the drawer asks before it closes", async ({
  page,
}) => {
  const s = await seed([main()]);
  const id = idOf(s, "Main");
  await openView(page, person(s, "owner"), id);
  await view(page).getByLabel("URL", { exact: true }).fill(n8n);
  await box(page, "A message arrives").check();
  await secret(page).getByRole("radio", { name: "Generate a secret" }).click();
  await expect(generated(page)).toBeVisible();
  await expect(saveButton(page)).toBeDisabled();

  await page.keyboard.press("Escape");
  await expect(drawer(page).getByText(question)).toBeVisible();
  await expect(page).toHaveURL(`/instances/${id}/webhooks`);
  await drawer(page).getByRole("button", { name: "Go back" }).click();
  await expect(drawer(page).getByText(question)).toHaveCount(0);

  await drawer(page).getByRole("button", { name: "Close", exact: true }).click();
  await expect(drawer(page).getByText(question)).toBeVisible();
  await drawer(page).getByRole("button", { name: "Go back" }).click();
  await expect(generated(page)).toBeVisible();

  await view(page).getByRole("button", { name: "I saved it" }).click();
  await expect(saveButton(page)).toBeEnabled();
  await page.keyboard.press("Escape");
  await expect(page).toHaveURL("/");
  await expect(drawer(page)).toHaveCount(0);
});

test("a save that stops moves the focus to the first problem", async ({ page }) => {
  const s = await seed([main([{ event: "message.received", url: n8n, secret: "old-secret" }])]);
  const id = idOf(s, "Main");
  const sent = watchPatches(page, id);
  await openView(page, person(s, "owner"), id);

  // A field error: the URL field takes the focus.
  const url = view(page).getByLabel("URL", { exact: true });
  await url.fill("");
  await saveButton(page).click();
  await expect(url).toBeFocused();

  // A refusal: the focus goes into the secret control, and a screen reader hears it.
  await url.fill(n8n);
  await box(page, "The connection changes").check();
  await saveButton(page).click();
  await expect(secret(page).getByRole("alert")).toHaveText(refusal);
  await expect(secret(page).getByRole("radio", { name: "Type a secret" })).toBeFocused();
  expect(sent).toHaveLength(0);
});

test("a problem on a raw row opens the raw section", async ({ page }) => {
  const s = await seed([
    main([
      { event: "message.received", url: n8n },
      { event: "message.status", url: other },
    ]),
  ]);
  const id = idOf(s, "Main");
  await openView(page, person(s, "owner"), id);
  await rawSection(page).click();
  await box(page, "Receipt").check();
  await rawSection(page).click();
  await expect(box(page, "Receipt")).toBeHidden();

  // The new row has no URL. Save opens the section and focuses the field.
  await saveButton(page).click();
  const field = hook(page, "Receipt").getByLabel("URL for whatsmeow.Receipt");
  await expect(field).toBeVisible();
  await expect(field).toBeFocused();
});

test("during a save, Save webhooks and the secret controls are disabled", async ({ page }) => {
  const s = await seed([main([{ event: "message.received", url: n8n }])]);
  const id = idOf(s, "Main");
  await openView(page, person(s, "owner"), id);
  await box(page, "A sent message changes").check();

  // The backend gets the change only when the test lets it go.
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await onNextPatch(page, id, async (route) => {
    await held;
    await route.fallback();
  });
  const answer = page.waitForResponse((r) => isPatch(id)(r.request()));
  await saveButton(page).click();
  await page.waitForRequest(isPatch(id));
  await expect(saveButton(page)).toBeDisabled();
  for (const radio of await secret(page).getByRole("radio").all())
    await expect(radio).toBeDisabled();
  await expect(box(page, "The connection changes")).toBeDisabled();

  release();
  expect((await answer).status()).toBe(200);
  await expect(saved(page)).toBeVisible();
  await expect(saveButton(page)).toBeEnabled();
  await expect(secret(page).getByRole("radio", { name: "Generate a secret" })).toBeEnabled();
});

test("the form: the events, the http:// warning and the attributes of the fields", async ({
  page,
}) => {
  const s = await seed([main()]);
  await openView(page, person(s, "owner"), idOf(s, "Main"));
  await expect(view(page)).toContainText(
    "Calyx calls this address from its own server. Use only addresses that you trust.",
  );
  await expect(view(page)).toContainText(
    "With a secret, your program can check that a call comes from Calyx. Your program must do this check. Without a secret, anyone who knows the URL can send false events to your program.",
  );

  await view(page).getByRole("button", { name: "Select all 4" }).click();
  for (const name of Object.values(label)) await expect(box(page, name)).toBeChecked();
  await view(page).getByRole("button", { name: "Clear all" }).click();
  for (const name of Object.values(label)) await expect(box(page, name)).not.toBeChecked();

  const url = view(page).getByLabel("URL", { exact: true });
  await expect(url).toHaveAttribute("translate", "no");
  const warning = view(page).getByText("The calls go without encryption.");
  await url.fill("http://n8n.example/webhook/calyx");
  await expect(warning).toBeVisible();
  await url.fill(n8n);
  await expect(warning).toHaveCount(0);

  await secret(page).getByRole("radio", { name: "Type a secret" }).click();
  const typed = secret(page).getByLabel("Your secret");
  await expect(typed).toHaveAttribute("translate", "no");
  await expect(typed).toHaveAttribute("spellcheck", "false");
  await expect(typed).toHaveAttribute("autocomplete", "off");

  // The raw events of WhatsApp Web are under their section, closed at the start.
  await expect(box(page, "Message")).toBeHidden();
  await rawSection(page).click();
  await expect(box(page, "Message")).toBeVisible();
  await expect(box(page, "meta.webhook")).toHaveCount(0);
});

test("a Meta Cloud instance shows meta.webhook as its raw event", async ({ page }) => {
  const s = await seed([main([], { provider: "META_CLOUD" })]);
  await openView(page, person(s, "owner"), idOf(s, "Main"));
  await rawSection(page).click();
  await expect(box(page, "meta.webhook")).toBeVisible();
  await expect(box(page, "Message")).toHaveCount(0);
});

// A reload, the Back of the browser and the "Back" of the view, on the path of the view.
// `before` is the path that "Edit" left: the browser goes back there.
async function walkPaths(page: Page, id: string, before: string): Promise<void> {
  await page.reload();
  await expect(view(page).getByRole("heading", { name: "Webhooks" })).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(before);
  await expect(view(page)).toHaveCount(0);
  await page.goForward();
  await expect(page).toHaveURL(`/instances/${id}/webhooks`);
  await expect(view(page).getByRole("heading", { name: "Webhooks" })).toBeVisible();
  await view(page).getByRole("link", { name: "Back" }).click();
  await expect(page).toHaveURL(`/instances/${id}`);
  await expect(view(page)).toHaveCount(0);
}

test("a reload and Back on the path of the view, for the owner and for a client user", async ({
  page,
}) => {
  const s = await seed([main(), main([], { name: "Second" })]);
  const id = idOf(s, "Main");
  for (const who of ["owner", "shopper"]) {
    await page.context().clearCookies();
    await login(page, person(s, who));
    await cell(page, "Main").getByRole("link").click();
    await edit(card(page));
    await expect(page.getByRole("dialog", { name: "Webhooks of Main" })).toBeVisible();
    await walkPaths(page, id, `/instances/${id}`);
    await expect(card(page)).toBeVisible();
  }
});

test("a client user with one instance sees the view in place of its card", async ({ page }) => {
  const s = await seed([main()]);
  const id = idOf(s, "Main");
  await login(page, person(s, "shopper"));
  const own = page.getByRole("main").locator('[data-slot="instance"]');
  await expect(
    page.getByRole("main").getByRole("heading", { name: "Main", level: 1 }),
  ).toBeVisible();
  await edit(own);
  await expect(page).toHaveURL(`/instances/${id}/webhooks`);
  await expect(page.getByRole("main").getByRole("heading", { name: "Webhooks" })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  // The card of a client user with one instance is at `/`, so "Edit" leaves `/`.
  await walkPaths(page, id, "/");
  await expect(
    page.getByRole("main").getByRole("heading", { name: "Main", level: 1 }),
  ).toBeVisible();
});

test("another view, and the id new, show This page does not exist", async ({ page }) => {
  const s = await seed([main()]);
  const id = idOf(s, "Main");
  await login(page, person(s, "owner"));
  await expect(cell(page, "Main")).toBeVisible();
  for (const path of [
    `/instances/${id}/other`,
    "/instances/new/webhooks",
    "/instances/new/bulk-jobs",
  ]) {
    await page.goto(path);
    await expect(page.getByRole("heading", { name: "This page does not exist" })).toBeVisible();
    await expect(view(page)).toHaveCount(0);
  }
});
