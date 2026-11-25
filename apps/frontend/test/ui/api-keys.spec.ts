import type { APIRequestContext, Locator, Page, Route } from "@playwright/test";
import { control, expect, login, type Person, test } from "./support.ts";

// The screen "API keys", the dialog of a new key and the help for programs. A 401, a 404 and
// a lost answer are failed requests in the browser log.
test.use({ tolerateFailedRequests: true });

type Seeded = { users: Person[] };

async function seed(): Promise<{ owner: Person; shopper: Person }> {
  const { status, body } = await control<Seeded>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    clients: [{ name: "Shop" }],
    users: [
      { label: "owner", role: "owner" },
      { label: "shopper", role: "client", client: "Shop" },
    ],
    instances: [{ name: "Main", client: "Shop", provider: "WHATSAPP_WEB", status: "CONNECTED" }],
  });
  expect(status).toBe(200);
  const [owner, shopper] = body.users as [Person, Person];
  return { owner, shopper };
}

const question = "Did you save it? You cannot see it again.";
const copyFailed = "Copy did not work. Select the text and copy it.";

const newKey = (page: Page) => page.getByRole("dialog", { name: "Your new API key" });
const keyField = (page: Page) => newKey(page).getByLabel("Key", { exact: true });
const row = (page: Page, name: string) =>
  page.locator('[data-slot="api-key"]', { has: page.getByText(name, { exact: true }) });

// Logs in and opens the screen by its path.
async function openKeys(page: Page, who: Person): Promise<void> {
  await login(page, who);
  await expect(page).toHaveURL(/\/$/);
  await page.goto("/developers/api-keys");
  await expect(page.getByRole("heading", { name: "API keys", level: 1 })).toBeVisible();
}

// Opens the form in place when it is closed, fills the name and selects "Save".
async function create(page: Page, name: string): Promise<void> {
  const field = page.getByLabel("Name", { exact: true });
  if (!(await field.isVisible()))
    await page.getByRole("button", { name: "Create API key" }).click();
  await field.fill(name);
  await page.getByRole("button", { name: "Save", exact: true }).click();
}

// Creates a key on the page and gives the whole key from the dialog.
async function createKey(page: Page, name: string): Promise<string> {
  await create(page, name);
  await expect(keyField(page)).toBeFocused();
  const key = await keyField(page).inputValue();
  expect(key).toMatch(/^cx_/);
  return key;
}

async function saved(page: Page): Promise<void> {
  await newKey(page).getByRole("button", { name: "I saved it" }).click();
  await expect(newKey(page)).toBeHidden();
}

async function revoke(page: Page, place: Locator): Promise<void> {
  await place.getByRole("button", { name: "Revoke" }).click();
  const ask = page.getByRole("dialog", { name: /^Revoke/ });
  await expect(ask).toContainText(
    "Programs that use this key stop working. Webhooks that a program set with this key keep working: check the webhooks of each instance. You cannot undo this.",
  );
  await ask.getByRole("button", { name: "Revoke" }).click();
  await expect(ask).toBeHidden();
}

async function loseAnswer(route: Route): Promise<void> {
  await route.fetch();
  await route.abort("connectionreset");
}

const emptyText =
  "You have no API keys. A key lets your software use Calyx. To connect your program, read Connect a program.";
const listText =
  "A key lets your software use Calyx. To connect your program, read Connect a program.";

test("the Connect a program link is in the text, with and without keys", async ({ page }) => {
  const { owner } = await seed();
  await openKeys(page, owner);
  await expect(page.getByText(emptyText, { exact: true })).toBeVisible();
  await expect(page.locator("main").getByRole("link", { name: "Connect a program" })).toHaveCount(
    1,
  );
  await create(page, "First");
  await saved(page);
  await expect(row(page, "First")).toBeVisible();
  await expect(page.getByText(listText, { exact: true })).toBeVisible();
  await expect(page.getByText("You have no API keys.")).toHaveCount(0);
  await expect(page.locator("main").getByRole("link", { name: "Connect a program" })).toHaveCount(
    1,
  );
  await page.locator("main").getByRole("link", { name: "Connect a program", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Connect a program", level: 1 })).toBeVisible();
});

test("the form opens in place, Cancel returns the focus, Save closes it", async ({ page }) => {
  const { owner } = await seed();
  await openKeys(page, owner);
  const open = page.getByRole("button", { name: "Create API key" });
  const section = page.getByRole("region", { name: "Your API keys" });
  await expect(section.getByRole("heading", { name: "Your API keys" })).toBeVisible();
  // The button is at the end of the section, below the text, not in the heading row.
  const buttonBox = await open.boundingBox();
  const textBox = await section
    .getByRole("link", { name: "Connect a program", exact: true })
    .boundingBox();
  expect(buttonBox?.y ?? 0).toBeGreaterThan(textBox?.y ?? Number.POSITIVE_INFINITY);
  await expect(section.getByRole("button", { name: "Create API key" })).toBeVisible();
  await expect(page.getByRole("region", { name: "Create a key" })).toHaveCount(0);
  await expect(page.getByLabel("Name", { exact: true })).toHaveCount(0);

  await open.click();
  await expect(section.getByLabel("Name", { exact: true })).toBeVisible();
  await expect(section.getByRole("button", { name: "Save", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Create API key" })).toHaveCount(0);
  await section.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(page.getByLabel("Name", { exact: true })).toHaveCount(0);
  await expect(open).toBeFocused();

  await create(page, "Assistant");
  await expect(newKey(page)).toBeVisible();
  await expect(page.getByLabel("Name", { exact: true })).toHaveCount(0);
  await saved(page);
  await expect(row(page, "Assistant")).toBeVisible();
});

test("create, copy, I saved it, and the question of Escape and Close", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const { owner } = await seed();
  await openKeys(page, owner);
  await expect(page.getByText(emptyText, { exact: true })).toBeVisible();

  const key = await createKey(page, "Assistant");
  await expect(keyField(page)).toHaveAttribute("readonly", "");
  await expect(keyField(page)).toHaveAttribute("translate", "no");
  await expect(keyField(page)).toHaveAttribute("spellcheck", "false");
  await expect(keyField(page)).toHaveAttribute("autocomplete", "off");

  await newKey(page).getByRole("button", { name: "Copy", exact: true }).click();
  await expect(newKey(page).getByRole("status").filter({ hasText: "Copied" })).toHaveCount(1);
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(key);

  // Both panels show the real key and the address of Calyx.
  await expect(newKey(page)).toContainText(`Authorization: Bearer ${key}`);
  await expect(newKey(page)).toContainText(new URL(page.url()).origin);

  await page.keyboard.press("Escape");
  await expect(newKey(page)).toBeVisible();
  await expect(newKey(page).getByRole("alert")).toContainText(question);
  await newKey(page).getByRole("button", { name: "Go back" }).click();
  await expect(newKey(page).getByText(question)).toHaveCount(0);
  await expect(keyField(page)).toHaveValue(key);

  await newKey(page).getByRole("button", { name: "Close", exact: true }).click();
  await expect(newKey(page).getByText(question)).toBeVisible();
  await newKey(page).getByRole("button", { name: "Go back" }).click();

  await saved(page);
  const prefix = key.slice(3, 11);
  await expect(row(page, "Assistant")).toContainText(`cx_${prefix}`);
  await expect(row(page, "Assistant")).toContainText("Never used");
  await expect(row(page, "Assistant")).toContainText("Created");
  await expect(page.getByText('"Last used" changes at most once a minute.')).toBeVisible();
  // The page never shows the whole key again.
  expect(await page.content()).not.toContain(key);
  await page.reload();
  await expect(row(page, "Assistant")).toBeVisible();
  expect(await page.content()).not.toContain(key);

  // "Yes, close" after the question closes the dialog.
  await createKey(page, "Second");
  await page.keyboard.press("Escape");
  await newKey(page).getByRole("button", { name: "Yes, close" }).click();
  await expect(newKey(page)).toBeHidden();
  await expect(row(page, "Second")).toBeVisible();
});

test("a failed copy says so, and the key stays selectable", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator.clipboard, "writeText", {
      value: () => Promise.reject(new DOMException("Denied", "NotAllowedError")),
    });
  });
  const { owner } = await seed();
  await openKeys(page, owner);
  const key = await createKey(page, "Assistant");

  await newKey(page).getByRole("button", { name: "Copy", exact: true }).click();
  const status = newKey(page).getByRole("status").filter({ hasText: copyFailed });
  await expect(status).toHaveCount(1);
  await expect(status).toBeVisible();
  await expect(newKey(page).getByText("Copied")).toHaveCount(0);

  await keyField(page).focus();
  expect(
    await keyField(page).evaluate((e: HTMLInputElement) => [e.selectionStart, e.selectionEnd]),
  ).toEqual([0, key.length]);

  await page.keyboard.press("Escape");
  await expect(newKey(page).getByText(question)).toBeVisible();
  await expect(newKey(page)).toBeVisible();
});

test("a name of 0 and of 101 characters", async ({ page }) => {
  const { owner } = await seed();
  await openKeys(page, owner);
  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/api-keys") sent.push("x");
  });
  const name = page.getByLabel("Name", { exact: true });

  await create(page, "   ");
  await expect(name).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByText("Use 1 to 100 characters.")).toBeVisible();

  // The field takes 100 characters at most.
  await name.fill("k".repeat(101));
  await expect(name).toHaveValue("k".repeat(100));
  // Without the limit of the field, the backend refuses the name.
  await name.evaluate((e) => e.removeAttribute("maxlength"));
  // The field is still marked from the first try: wait for the answer of the second.
  const second = page.waitForResponse(
    (r) => r.request().method() === "POST" && new URL(r.url()).pathname === "/api/v1/api-keys",
  );
  await create(page, "k".repeat(101));
  expect((await second).status()).toBe(400);
  expect((await second).request().postDataJSON()).toEqual({ name: "k".repeat(101) });
  await expect(name).toHaveAttribute("aria-invalid", "true");
  await expect(page.getByText("Use 1 to 100 characters.")).toBeVisible();
  await expect(newKey(page)).toHaveCount(0);
  expect(sent).toHaveLength(2);
});

test("revoke, and a key that another place revoked", async ({ page, baseURL }) => {
  const { owner } = await seed();
  await openKeys(page, owner);
  await createKey(page, "Assistant");
  await saved(page);
  await revoke(page, row(page, "Assistant"));
  await expect(row(page, "Assistant")).toHaveCount(0);
  await expect(page.getByText(emptyText, { exact: true })).toBeVisible();

  await createKey(page, "Gone");
  await saved(page);
  const list = await page.request.get("/api/v1/api-keys");
  const id = ((await list.json()) as { items: { id: string }[] }).items[0]?.id as string;
  const res = await page.request.delete(`/api/v1/api-keys/${id}`, {
    headers: { Origin: new URL(baseURL ?? "").origin },
  });
  expect(res.status()).toBe(204);
  await revoke(page, row(page, "Gone"));
  const failure = page.locator('[data-slot="failure"]');
  await expect(failure).toContainText("This key does not exist now. It was revoked.");
  await expect(row(page, "Gone")).toHaveCount(0);
});

test("a lost answer of the create lists the new keys with that name", async ({ page }) => {
  const { owner } = await seed();
  await openKeys(page, owner);
  await createKey(page, "Old");
  await saved(page);

  await expect(row(page, "Old")).toBeVisible();

  await loseCreate(page);
  await create(page, "Old");
  const lost = page.locator('[data-slot="lost-keys"]');
  await expect(lost).toContainText("Calyx did not answer. Revoke these keys and create a new one.");
  // Only the key of this request: the older key with the same name is not in the notice.
  await expect(lost.locator("li")).toHaveCount(1);
  await expect(newKey(page)).toHaveCount(0);

  await revoke(page, lost.locator("li"));
  await expect(lost).toHaveCount(0);
  await expect(page.locator('[data-slot="failure"]')).toHaveCount(0);
  await expect(row(page, "Old")).toHaveCount(1);
});

// A key that another place made, as another tab does: the page gets no event for it.
async function createElsewhere(page: Page, baseURL: string | undefined, name: string) {
  const res = await page.request.post("/api/v1/api-keys", {
    headers: { Origin: new URL(baseURL ?? "").origin },
    data: { name },
  });
  expect(res.status()).toBe(201);
}

// The next create request gets no answer. The reads of the list pass.
async function loseCreate(page: Page): Promise<void> {
  let lost = false;
  await page.route("**/api/v1/api-keys", (route) => {
    if (route.request().method() !== "POST" || lost) return route.fallback();
    lost = true;
    return loseAnswer(route);
  });
}

test("a lost answer after a slow first read lists only the new key", async ({ page, baseURL }) => {
  const { owner } = await seed();
  await login(page, owner);
  await expect(page).toHaveURL(/\/$/);
  await createElsewhere(page, baseURL, "Old");

  // The first read of the list waits until the test lets it go.
  let release = () => {};
  const held = new Promise<void>((r) => {
    release = r;
  });
  let reads = 0;
  await page.route("**/api/v1/api-keys", async (route) => {
    if (route.request().method() === "GET" && reads++ === 0) await held;
    await route.fallback();
  });
  await loseCreate(page);
  await page.goto("/developers/api-keys");
  await page.getByRole("button", { name: "Create API key" }).click();
  await page.getByLabel("Name", { exact: true }).fill("Old");
  await page.getByLabel("Name", { exact: true }).press("Enter");
  release();
  await expect(row(page, "Old").first()).toBeVisible();
  await page.getByRole("button", { name: "Save", exact: true }).click();

  const lost = page.locator('[data-slot="lost-keys"]');
  await expect(lost).toContainText("Calyx did not answer. Revoke these keys and create a new one.");
  await expect(lost.locator("li")).toHaveCount(1);
  await expect(row(page, "Old")).toHaveCount(2);
});

test("a lost answer after a key from another tab lists only the new key", async ({
  page,
  baseURL,
}) => {
  const { owner } = await seed();
  await openKeys(page, owner);
  await expect(page.getByText(emptyText, { exact: true })).toBeVisible();
  await createElsewhere(page, baseURL, "Old");

  await loseCreate(page);
  await create(page, "Old");
  const lost = page.locator('[data-slot="lost-keys"]');
  await expect(lost).toContainText("Calyx did not answer. Revoke these keys and create a new one.");
  await expect(lost.locator("li")).toHaveCount(1);
  await expect(row(page, "Old")).toHaveCount(2);
});

// A Cancel during the create would lose the answer, and the new key with it.
test("Cancel is off from the read before the create to its answer", async ({ page }) => {
  const { owner } = await seed();
  await openKeys(page, owner);
  await expect(page.getByText("You have no API keys.")).toBeVisible();

  // The next read of the list and the next create wait until the test lets each one go.
  let releaseRead = () => {};
  const readHeld = new Promise<void>((r) => {
    releaseRead = r;
  });
  let releasePost = () => {};
  const postHeld = new Promise<void>((r) => {
    releasePost = r;
  });
  let gets = 0;
  let posts = 0;
  await page.route("**/api/v1/api-keys", async (route) => {
    const method = route.request().method();
    if (method === "GET" && gets++ === 0) await readHeld;
    if (method === "POST" && posts++ === 0) await postHeld;
    await route.fallback();
  });
  const cancel = page.getByRole("button", { name: "Cancel" });
  await create(page, "Assistant");
  await expect(cancel).toBeDisabled();
  const post = page.waitForRequest(
    (r) => r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/api-keys",
  );
  releaseRead();
  await post;
  await expect(cancel).toBeDisabled();
  releasePost();
  await expect(keyField(page)).toBeFocused();
  await saved(page);
  await expect(row(page, "Assistant")).toBeVisible();
});

test("a failed read keeps the last list, and Create stays usable", async ({ page }) => {
  const { owner } = await seed();
  await openKeys(page, owner);
  await createKey(page, "Assistant");
  await saved(page);
  await expect(row(page, "Assistant")).toBeVisible();

  // Each read of the list fails now. The read before a create is one of them.
  await page.route("**/api/v1/api-keys", (route) =>
    route.request().method() === "GET"
      ? route.fulfill({
          status: 503,
          contentType: "application/problem+json",
          body: JSON.stringify({ status: 503, code: "internal", detail: "down" }),
        })
      : route.fallback(),
  );
  const posts: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/api-keys") posts.push("x");
  });
  await create(page, "Second");
  await expect(page.locator('[data-slot="failure"]')).toBeVisible();
  await expect(row(page, "Assistant")).toBeVisible();
  await expect(row(page, "Assistant").getByRole("button", { name: "Revoke" })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Save", exact: true })).toBeEnabled();
  // With no fresh list, the page does not send the create.
  expect(posts).toEqual([]);

  await page.unroute("**/api/v1/api-keys");
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(newKey(page)).toBeVisible();
  await saved(page);
  await expect(row(page, "Second")).toBeVisible();
  await expect(page.locator('[data-slot="failure"]')).toHaveCount(0);
});

test("a client user, the panels for each role, a reload and Back", async ({ page }) => {
  const { owner, shopper } = await seed();
  const mcp = (p: Page) => p.getByRole("button", { name: "Connect an MCP client" });
  const ownerLine =
    "Do not give your key to a program of a client. Ask the client user to create a key.";

  await openKeys(page, shopper);
  await page.goto("/developers/connect");
  await expect(page.getByRole("heading", { name: "Connect a program", level: 1 })).toBeVisible();
  await expect(page.getByText("Authorization: Bearer cx_...")).toBeVisible();
  // A core client is for the private network, so the screen lists no core-only package.
  await expect(page.getByText("calyx/client-sdk", { exact: true })).toBeVisible();
  await expect(page.getByText("calyx/client-sdk-core")).toHaveCount(0);
  await expect(page.getByText("calyx-client-sdk-core")).toHaveCount(0);
  // The connect screen shows every part open.
  await expect(mcp(page)).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByText(`${new URL(page.url()).origin}/mcp`)).toBeVisible();
  await expect(
    page.getByText(
      "A program with this key can do all that you can do, on every instance that you can see, bulk jobs included. It cannot log in, change your password or create keys.",
    ),
  ).toBeVisible();
  for (const tool of [
    "list_instances",
    "get_instance",
    "send_message",
    "create_bulk_job",
    "get_bulk_job",
    "cancel_bulk_job",
  ]) {
    await expect(page.getByText(tool, { exact: true })).toBeVisible();
  }
  await expect(page.getByText(ownerLine)).toHaveCount(0);
  await page.goto("/developers/api-keys");
  await createKey(page, "Shop app");
  await saved(page);
  await expect(row(page, "Shop app")).toBeVisible();

  await page.reload();
  await expect(page.getByRole("heading", { name: "API keys", level: 1 })).toBeVisible();
  await expect(row(page, "Shop app")).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(/\/developers\/connect$/);
  await page.goForward();
  await expect(page).toHaveURL(/\/developers\/api-keys$/);

  await page.context().clearCookies();
  await openKeys(page, owner);
  // The owner sees only the own keys.
  await expect(row(page, "Shop app")).toHaveCount(0);
  await page.goto("/developers/connect");
  await expect(page.getByText(ownerLine)).toBeVisible();
});

// The key works for the HTTP API and for MCP, and a revoke ends it.
async function callMe(request: APIRequestContext, key: string) {
  return request.get("/api/v1/me", { headers: { Authorization: `Bearer ${key}` } });
}
async function callMcp(request: APIRequestContext, key: string, method: string, params?: unknown) {
  return request.post("/mcp", {
    headers: { Authorization: `Bearer ${key}` },
    data: { jsonrpc: "2.0", id: 1, method, ...(params === undefined ? {} : { params }) },
  });
}

test("the key calls the API and MCP, and stops after the revoke", async ({ page, request }) => {
  const { shopper } = await seed();
  await openKeys(page, shopper);
  const key = await createKey(page, "Assistant");
  await saved(page);

  const me = await callMe(request, key);
  expect(me.status()).toBe(200);
  expect(((await me.json()) as { user: { email: string } }).user.email).toBe(shopper.email);

  const init = await callMcp(request, key, "initialize", {
    protocolVersion: "2025-11-25",
    capabilities: {},
    clientInfo: { name: "test", version: "1" },
  });
  expect(init.status()).toBe(200);
  expect(
    ((await init.json()) as { result: { serverInfo: { name: string } } }).result.serverInfo.name,
  ).toBe("calyx");
  const list = await callMcp(request, key, "tools/list");
  expect(list.status()).toBe(200);
  const tools = ((await list.json()) as { result: { tools: { name: string }[] } }).result.tools;
  expect(tools.map((t) => t.name).sort()).toEqual([
    "cancel_bulk_job",
    "create_bulk_job",
    "get_bulk_job",
    "get_instance",
    "list_instances",
    "send_message",
  ]);

  // "Last used" changes after a use of the key.
  await page.reload();
  await expect(row(page, "Assistant")).toContainText("Last used");
  await expect(row(page, "Assistant")).not.toContainText("Never used");

  await revoke(page, row(page, "Assistant"));
  expect((await callMe(request, key)).status()).toBe(401);
  expect((await callMcp(request, key, "tools/list")).status()).toBe(401);
});
