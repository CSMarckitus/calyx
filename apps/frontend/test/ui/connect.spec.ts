import type { Page, Request, Route } from "@playwright/test";
import { control, expect, login, type Person, test } from "./support.ts";

// "Connect a number" and "Reconnect". The fake core gives the QR and the
// code, so no test touches real WhatsApp. A refused start, a cut stream and a lost answer are
// failed requests in the browser log.
test.use({ tolerateFailedRequests: true });

type Seeded = {
  clients: { name: string; id: string }[];
  users: (Person & { id: string })[];
  instances: { name: string; id: string; path: string }[];
};

async function seed(body: Record<string, unknown>): Promise<Seeded> {
  const { status, body: answer } = await control<Seeded>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    clients: [{ name: "Shop", can_create_instances: true }],
    users: [
      { label: "owner", role: "owner" },
      { label: "shopper", role: "client", client: "Shop" },
    ],
    ...body,
  });
  expect(status).toBe(200);
  return answer;
}

const person = (s: Seeded, label: string) => s.users.find((u) => u.label === label) as Person;
const clientId = (s: Seeded, name: string) => s.clients.find((c) => c.name === name)?.id as string;
const idOf = (s: Seeded, name: string) => s.instances.find((i) => i.name === name)?.id as string;

type Step =
  | { step: "update"; qr?: string; code?: string }
  | { step: "wait" | "connected" | "expired" | "refused" | "nothing" };

// The script of the next pairing of the fake core.
async function pairing(...steps: Step[]): Promise<void> {
  expect((await control("POST", "/pairing", { steps })).status).toBe(200);
}
async function next(): Promise<void> {
  expect((await control("POST", "/pairing/next")).status).toBe(200);
}

// The drawer, and not the card of services, which is a dialog too: after Close on a phone, the
// mouse of the test can be over the header, and a hover opens that card.
const drawer = (page: Page) => page.locator('[role="dialog"][data-slot="sheet-content"]');
// A modal drawer hides the list from the accessibility tree, so the cell has a CSS locator.
const cell = (page: Page, name: string) =>
  page
    .locator("main")
    .locator('[data-slot="instance"]', { has: page.getByText(name, { exact: true }) });
const current = (page: Page) => drawer(page).locator('ol > li[aria-current="step"]');
const step = (page: Page, title: string) =>
  drawer(page).locator("ol > li", { has: page.getByText(title, { exact: true }) });
const button = (page: Page, name: string | RegExp) =>
  drawer(page).getByRole("button", { name, exact: typeof name === "string" });
const qr = (page: Page) => drawer(page).getByRole("img", { name: "QR code" });
const code = (page: Page) => drawer(page).locator('[data-slot="pairing-code"]');
const qrPath = (page: Page) => qr(page).locator("path").getAttribute("d");

const isCreate = (r: Request) =>
  r.method() === "POST" && new URL(r.url()).pathname === "/api/v1/instances";
const isPair = (r: Request) => r.method() === "POST" && new URL(r.url()).pathname.endsWith("/pair");
const followPath = "**/api/v1/instances/*/pairing";

// Name, use and the automatic settings: the first three steps of a new number.
async function firstSteps(page: Page, name: string, use?: "Only send"): Promise<void> {
  await expect(current(page)).toContainText("Name");
  await drawer(page).getByRole("textbox", { name: "Name" }).fill(name);
  await button(page, "Continue").click();
  await expect(current(page)).toContainText("What is it for?");
  if (use) await button(page, new RegExp(`^${use}`)).click();
  await button(page, "Continue").click();
  await expect(current(page)).toContainText("Optional settings");
  await button(page, "Continue with automatic").click();
  await expect(current(page)).toContainText("Where is WhatsApp?");
}

async function chooseQr(page: Page): Promise<void> {
  await button(page, /^No, on another phone/).click();
  await button(page, "Show the QR code").click();
}

async function chooseCode(page: Page, country: string, phone: string): Promise<void> {
  await button(page, /^Yes, on this device/).click();
  await drawer(page).getByRole("combobox", { name: "Country code" }).selectOption(country);
  await drawer(page).getByRole("textbox", { name: "Phone number" }).fill(phone);
  await button(page, "Get the code").click();
}

// The owner opens "Connect a number" of a group.
async function connectIn(page: Page, s: Seeded, client: string): Promise<void> {
  await page
    .locator("main")
    .getByRole("region", { name: client, exact: true })
    .getByRole("button", { name: "+ Connect a number" })
    .click();
  await expect(page).toHaveURL(`/instances/new?client=${clientId(s, client)}`);
  await expect(
    drawer(page).getByRole("heading", { name: "Connect a WhatsApp number", level: 2 }),
  ).toBeFocused();
}

// Headless Chromium keeps every tab "visible". The test sets the visibility of the page itself,
// as a person who goes to WhatsApp makes it.
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

// The streams of `followPairing` that are open now.
function watchFollow(page: Page) {
  let open = 0;
  const isFollow = (url: string) =>
    /\/api\/v1\/instances\/[^/]+\/pairing$/.test(new URL(url).pathname);
  page.on("request", (r) => {
    if (isFollow(r.url()) && r.method() === "GET") open++;
  });
  const end = (r: Request) => {
    if (isFollow(r.url()) && r.method() === "GET") open--;
  };
  page.on("requestfinished", end);
  page.on("requestfailed", end);
  return () => open;
}

// The instance as the backend has it, with the cookie of the page.
async function instanceState(page: Page, id: string): Promise<string> {
  const res = await page.request.get(`/api/v1/instances/${id}`);
  if (res.status() === 404) return "gone";
  const body = (await res.json()) as { state: string; status: string | null };
  return `${body.state} ${body.status}`;
}

const riskStart =
  "WhatsApp Web instances use an unofficial client (whatsmeow). Using it is against the Terms of Service of WhatsApp. WhatsApp can ban the number.";
const riskEnd =
  "Calyx limits how fast messages go out. This lowers the risk. It does not remove the risk.";

test.describe("on a phone with a touch screen", () => {
  test.use({ hasTouch: true, isMobile: true });

  test("a client user connects its first number with a code, on the same phone", async ({
    page,
  }) => {
    const s = await seed({ instances: [] });
    await login(page, person(s, "shopper"));
    await expect(
      page.getByRole("heading", { name: "Connect your WhatsApp", level: 1 }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Connect a WhatsApp number" }).click();
    await expect(page).toHaveURL(`/instances/new?client=${clientId(s, "Shop")}`);
    const title = drawer(page).getByRole("heading", {
      name: "Connect a WhatsApp number",
      level: 2,
    });
    await expect(title).toBeFocused();

    // The trace: an ordered list, with the current step marked.
    await expect(drawer(page).getByText("Step 1 of 6")).toBeVisible();
    await expect(drawer(page).locator("ol > li")).toHaveCount(6);
    await expect(current(page)).toContainText("Name");
    // The risk is in the step "Name", for both roles.
    await expect(drawer(page)).toContainText(riskStart);
    await expect(drawer(page)).toContainText(riskEnd);

    await firstSteps(page, "Shop");
    await expect(step(page, "Name")).toContainText("Shop");
    await expect(step(page, "What is it for?")).toContainText("Send and receive");
    await expect(step(page, "Optional settings")).toContainText("Automatic");
    // A coarse pointer selects the code.
    await expect(button(page, /^Yes, on this device/)).toHaveAttribute("aria-pressed", "true");
    await expect(drawer(page).getByText("In most countries, leave out the first 0.")).toBeVisible();

    await pairing({ step: "update", code: "K7QM2XVP" }, { step: "wait" }, { step: "connected" });
    const create = page.waitForRequest(isCreate);
    await drawer(page).getByRole("combobox", { name: "Country code" }).selectOption("ES");
    await drawer(page).getByRole("textbox", { name: "Phone number" }).fill("600 000 000");
    await button(page, "Get the code").click();
    const body = (await create).postDataJSON() as Record<string, unknown>;
    expect(body).toMatchObject({
      provider: "WHATSAPP_WEB",
      client_id: clientId(s, "Shop"),
      display_name: "Shop",
      send_only: false,
      pairing: "code",
      phone: "34600000000",
      locale: "en-US",
    });
    expect(body).not.toHaveProperty("device_identity");

    await expect(code(page)).toHaveText("K7QM-2XVP");
    await expect(drawer(page)).toContainText("+34600000000");
    await expect(current(page)).toContainText("Link the phone");
    await expect(drawer(page)).toContainText("Linked devices");
    await expect(page).toHaveURL(/\/instances\/[0-9a-f-]{36}$/);
    // "Change" does not show while a code waits.
    await expect(button(page, "Change")).toHaveCount(0);

    await next();
    await expect(drawer(page).getByRole("heading", { name: "Shop is connected" })).toBeVisible();
    await expect(drawer(page).getByText("Step 6 of 6")).toBeVisible();
    await expect(drawer(page)).toContainText(
      "Calyx sends a mail to the organization admin, Acme, so that they can tell you.",
    );
    await button(page, "Done").click();
    // Its one number is its screen.
    await expect(drawer(page)).toHaveCount(0);
    const screen = page.getByRole("main").locator('[data-slot="instance"]');
    await expect(screen.getByRole("heading", { name: "Shop", level: 1 })).toBeVisible();
    await expect(screen.getByRole("status")).toHaveText("Connected");
  });
});

test("an owner connects with a QR code on a computer, and a new code replaces the old one", async ({
  page,
}) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await expect(drawer(page).getByText("For Shop")).toBeVisible();
  await expect(drawer(page)).toContainText(riskStart);
  await firstSteps(page, "Front desk");
  // A fine pointer selects the QR code.
  await expect(button(page, /^No, on another phone/)).toHaveAttribute("aria-pressed", "true");

  await pairing(
    { step: "update", qr: "2@first" },
    { step: "wait" },
    { step: "update", qr: "2@second" },
    { step: "wait" },
    { step: "connected" },
  );
  const create = page.waitForRequest(isCreate);
  await button(page, "Show the QR code").click();
  const body = (await create).postDataJSON() as Record<string, unknown>;
  expect(body).toMatchObject({ client_id: clientId(s, "Shop"), pairing: "qr" });
  expect(body).not.toHaveProperty("phone");

  await expect(qr(page)).toBeVisible();
  const first = await qrPath(page);
  expect(first).toBeTruthy();
  await next();
  await expect.poll(() => qrPath(page)).not.toBe(first);
  await next();
  await expect(
    drawer(page).getByRole("heading", { name: "Front desk is connected" }),
  ).toBeVisible();
  await expect(drawer(page)).toContainText(
    `Calyx sends a mail to you, at ${person(s, "owner").email}.`,
  );
  await expect(cell(page, "Front desk")).toContainText("Connected");
  await button(page, "Done").click();
  // The drawer shows the number.
  await expect(drawer(page).locator('[data-slot="instance"]').getByRole("status")).toHaveText(
    "Connected",
  );
});

test("a code that expires, and Try again keeps the name and the choices", async ({ page }) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Reminders", "Only send");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "expired" });
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  await next();
  const failed = drawer(page).getByRole("alert");
  await expect(failed).toContainText(
    "The code expired. Nobody used it in time. Nothing was saved.",
  );
  await expect(current(page)).toHaveAttribute("data-state", "failed");
  await expect(qr(page)).toHaveCount(0);

  await button(page, "Try again").click();
  await expect(page).toHaveURL(`/instances/new?client=${clientId(s, "Shop")}`);
  await expect(current(page)).toContainText("Where is WhatsApp?");
  await expect(step(page, "Name")).toContainText("Reminders");
  await expect(step(page, "What is it for?")).toContainText("Only send");

  await pairing({ step: "update", qr: "2@again" }, { step: "wait" });
  const create = page.waitForRequest(isCreate);
  await chooseQr(page);
  expect((await create).postDataJSON()).toMatchObject({
    display_name: "Reminders",
    send_only: true,
    client_id: clientId(s, "Shop"),
  });
  await expect(qr(page)).toBeVisible();
});

test("a refused start, and a link that the core refuses", async ({ page }) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");

  // The core refuses the start: the text of `conflict` of createInstance, and the step stays.
  await pairing({ step: "refused" });
  await chooseQr(page);
  await expect(drawer(page).getByRole("alert")).toContainText(
    "WhatsApp or the core refused this start.",
  );
  await expect(current(page)).toContainText("Where is WhatsApp?");
  await expect(page).toHaveURL(`/instances/new?client=${clientId(s, "Shop")}`);
  await expect(cell(page, "Front desk")).toHaveCount(0);

  // The core refuses the link after the code.
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "refused" });
  await button(page, "Show the QR code").click();
  await expect(qr(page)).toBeVisible();
  await next();
  await expect(drawer(page).getByRole("alert")).toContainText("The link did not finish.");
  await expect(button(page, "Try again")).toBeVisible();
});

test("Close does not stop the pairing, and Continue opens the same step", async ({ page }) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" });
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  const first = await qrPath(page);

  await button(page, "Close").click();
  await expect(page).toHaveURL("/");
  await expect(drawer(page)).toHaveCount(0);
  await expect(cell(page, "Front desk")).toContainText("Linking. Continue");
  await cell(page, "Front desk").getByRole("link").click();
  await expect(qr(page)).toBeVisible();
  expect(await qrPath(page)).toBe(first);
  await expect(current(page)).toContainText("Link the phone");

  await next();
  await expect(
    drawer(page).getByRole("heading", { name: "Front desk is connected" }),
  ).toBeVisible();
});

test("a reload opens the same step, and Cancel of a new number removes it", async ({ page }) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" });
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  const first = await qrPath(page);
  const path = new URL(page.url()).pathname;

  await page.reload();
  await expect(page).toHaveURL(path);
  await expect(qr(page)).toBeVisible();
  expect(await qrPath(page)).toBe(first);
  await expect(step(page, "Name")).toContainText("Front desk");

  await button(page, "Cancel").click();
  await expect(page).toHaveURL("/");
  await expect(drawer(page)).toHaveCount(0);
  await expect(cell(page, "Front desk")).toHaveCount(0);
});

test("a hidden page closes the stream, and the return shows the result", async ({ page }) => {
  const s = await seed({ instances: [] });
  const open = watchFollow(page);
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  await pairing({ step: "update", code: "K7QM2XVP" }, { step: "wait" }, { step: "connected" });
  await chooseCode(page, "ES", "600000000");
  await expect(code(page)).toHaveText("K7QM-2XVP");
  await expect.poll(open).toBe(1);
  const id = new URL(page.url()).pathname.split("/").pop() as string;

  // The person goes to WhatsApp: the stream closes, and the phone links meanwhile.
  await setVisibility(page, "hidden");
  await expect.poll(open).toBe(0);
  await next();
  await expect.poll(() => instanceState(page, id)).toBe("active CONNECTED");

  // The return attaches, gets 404, and reads the instance.
  await setVisibility(page, "visible");
  await expect(drawer(page)).toContainText("The phone is linked");
  await expect(drawer(page).getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
});

test("a reload after the phone linked keeps a new number new", async ({ page }) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" });
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  const id = new URL(page.url()).pathname.split("/").pop() as string;

  // The browser discards the hidden tab, and the phone links meanwhile.
  await setVisibility(page, "hidden");
  await next();
  await expect.poll(() => instanceState(page, id)).toBe("active CONNECTED");
  await page.reload();
  await expect(
    drawer(page).getByRole("heading", { name: "Connect a WhatsApp number", level: 2 }),
  ).toBeVisible();
  await expect(drawer(page)).toContainText("The phone is linked");
  await expect(drawer(page).getByText("Step 6 of 6")).toBeVisible();
});

// A route cannot cut the real stream in its middle: route.fetch() waits for the whole body, and
// the pairing stream does not end. So the route answers one request with a 200 that has the first
// update of the fake core and then ends, as a proxy that cuts the connection does.
test("a cut stream: a 200 that ends with no final event attaches again", async ({ page }) => {
  const s = await seed({ instances: [] });
  let follows = 0;
  page.on("request", (r) => {
    if (r.method() === "GET" && /\/api\/v1\/instances\/[^/]+\/pairing$/.test(r.url())) follows++;
  });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" });
  const update = {
    status: "NEEDS_AUTH",
    pairing_code: null,
    qr_text: "2@first",
    message: "Please scan the QR code to authenticate.",
  };
  await page.route(
    followPath,
    (route) =>
      route.request().method() === "GET"
        ? route.fulfill({
            status: 200,
            contentType: "text/event-stream",
            body: `event: update\ndata: ${JSON.stringify(update)}\n\n`,
          })
        : route.fallback(),
    { times: 1 },
  );
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  const first = await qrPath(page);
  // The page attaches again after its wait, and never says that the pairing failed.
  await expect.poll(() => follows).toBeGreaterThanOrEqual(2);
  await expect(qr(page)).toBeVisible();
  expect(await qrPath(page)).toBe(first);
  await expect(drawer(page)).not.toContainText("did not finish");
  await next();
  await expect(
    drawer(page).getByRole("heading", { name: "Front desk is connected" }),
  ).toBeVisible();
});

test("a connection reset of the stream says Checking the link, and attaches again", async ({
  page,
}) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" });
  await page.route(followPath, (route) => route.abort("connectionreset"), { times: 1 });
  await chooseQr(page);
  await expect(drawer(page).getByText("Checking the link")).toBeVisible();
  await expect(qr(page)).toBeVisible();
  await next();
  await expect(
    drawer(page).getByRole("heading", { name: "Front desk is connected" }),
  ).toBeVisible();
});

test("a 502 in place of a 404 never says that the pairing failed", async ({ page }) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" });
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  const id = new URL(page.url()).pathname.split("/").pop() as string;

  await setVisibility(page, "hidden");
  await next();
  await expect.poll(() => instanceState(page, id)).toBe("active CONNECTED");
  // The stream would answer 404 now. A proxy answers 502 one time.
  await page.route(
    followPath,
    (route) => route.fulfill({ status: 502, contentType: "text/plain", body: "Bad Gateway" }),
    { times: 1 },
  );
  await setVisibility(page, "visible");
  await expect(drawer(page).getByText("Checking the link")).toBeVisible();
  await expect(drawer(page)).toContainText("The phone is linked");
  await expect(drawer(page)).not.toContainText("did not finish");
});

// The backend makes the change, and the browser loses its answer.
async function loseAnswer(route: Route): Promise<void> {
  await route.fetch();
  await route.abort("connectionreset");
}

test("a lost answer of createInstance: the list shows the number, and Continue opens it", async ({
  page,
}) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" });
  await page.route("**/api/v1/instances", (route) =>
    route.request().method() === "POST" ? loseAnswer(route) : route.fallback(),
  );
  await chooseQr(page);
  await expect(drawer(page)).toContainText("Calyx did not confirm the start.");
  await expect(drawer(page)).toContainText(
    "If the new number is in the list, select 'Linking. Continue'. If not, try again.",
  );
  await expect(current(page)).toContainText("Where is WhatsApp?");
  await expect(cell(page, "Front desk")).toContainText("Linking. Continue");
  await page.unroute("**/api/v1/instances");

  await button(page, "Close").click();
  await cell(page, "Front desk").getByRole("link").click();
  await expect(qr(page)).toBeVisible();
});

test("a lost answer of pairInstance attaches, and shows the code", async ({ page }) => {
  const s = await seed({
    instances: [{ name: "Main", client: "Shop", status: "NEEDS_AUTH", locale: "es-ES" }],
  });
  await login(page, person(s, "owner"));
  await cell(page, "Main").getByRole("button", { name: "Reconnect" }).click();
  await expect(
    drawer(page).getByRole("heading", { name: "Reconnect Main", level: 2 }),
  ).toBeFocused();
  await expect(drawer(page).getByText("Step 1 of 3")).toBeVisible();
  await expect(current(page)).toContainText("Where is WhatsApp?");

  await pairing({ step: "update", qr: "2@first" }, { step: "wait" });
  await page.route("**/api/v1/instances/*/pair", loseAnswer, { times: 1 });
  const pair = page.waitForRequest(isPair);
  await chooseQr(page);
  // The instance has a locale: the core keeps it.
  expect((await pair).postDataJSON()).toEqual({ pairing: "qr" });
  await expect(qr(page)).toBeVisible();
  await expect(current(page)).toContainText("Link the phone");
});

test("the other method cancels first, and goes back one step", async ({ page }) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  // Before Start, a finished step has "Change".
  await expect(step(page, "Name").getByRole("button", { name: "Change" })).toBeVisible();
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" });
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  await expect(button(page, "Change")).toHaveCount(0);

  await button(page, "Use a code").click();
  await expect(page).toHaveURL(`/instances/new?client=${clientId(s, "Shop")}`);
  await expect(current(page)).toContainText("Where is WhatsApp?");
  await expect(step(page, "Name")).toContainText("Front desk");
  await expect(button(page, /^Yes, on this device/)).toHaveAttribute("aria-pressed", "true");

  await pairing({ step: "update", code: "ABCD1234" }, { step: "wait" });
  await drawer(page).getByRole("combobox", { name: "Country code" }).selectOption("IT");
  await drawer(page).getByRole("textbox", { name: "Phone number" }).fill("391 234 5678");
  const create = page.waitForRequest(isCreate);
  await button(page, "Get the code").click();
  expect((await create).postDataJSON()).toMatchObject({
    pairing: "code",
    phone: "393912345678",
    display_name: "Front desk",
  });
  await expect(code(page)).toHaveText("ABCD-1234");
});

test("a cancel after the phone linked shows the number as it is", async ({ page }) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" });
  // The stream gives nothing: the page does not see the link.
  await page.route(followPath, (route) =>
    route.request().method() === "GET" ? undefined : route.fallback(),
  );
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  const id = new URL(page.url()).pathname.split("/").pop() as string;
  await next();
  await expect.poll(() => instanceState(page, id)).toBe("active CONNECTED");

  await button(page, "Cancel").click();
  await expect(drawer(page).getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
  await expect(page).toHaveURL(`/instances/${id}`);
  await expect(cell(page, "Front desk")).toContainText("Connected");
});

test("Try again after a reload keeps the client", async ({ page }) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "owner"));
  await connectIn(page, s, "Shop");
  await firstSteps(page, "Front desk");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "expired" });
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();

  await page.reload();
  await expect(qr(page)).toBeVisible();
  await next();
  await expect(drawer(page).getByRole("alert")).toContainText("The code expired.");
  await button(page, "Try again").click();
  await expect(page).toHaveURL(`/instances/new?client=${clientId(s, "Shop")}`);
  await expect(current(page)).toContainText("Where is WhatsApp?");

  await pairing({ step: "update", qr: "2@again" }, { step: "wait" });
  const create = page.waitForRequest(isCreate);
  await chooseQr(page);
  expect((await create).postDataJSON()).toMatchObject({ client_id: clientId(s, "Shop") });
});

test("a reload in a reconnection opens its code again", async ({ page }) => {
  const s = await seed({
    instances: [{ name: "Main", client: "Shop", status: "NEEDS_AUTH", locale: "es-ES" }],
  });
  await login(page, person(s, "owner"));
  await cell(page, "Main").getByRole("button", { name: "Reconnect" }).click();
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" });
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  const first = await qrPath(page);

  // A new entry of the history has no router state: only the draft opens the steps.
  await page.goto(`/instances/${idOf(s, "Main")}`);
  await expect(
    drawer(page).getByRole("heading", { name: "Reconnect Main", level: 2 }),
  ).toBeVisible();
  await expect(qr(page)).toBeVisible();
  expect(await qrPath(page)).toBe(first);
  // A reconnection that runs shows "Disconnected" in the list.
  await expect(cell(page, "Main")).toContainText("Disconnected");

  await next();
  await expect(drawer(page).getByRole("heading", { name: "Main is connected" })).toBeVisible();
  await button(page, "Done").click();
  await expect(drawer(page).locator('[data-slot="instance"]').getByRole("status")).toHaveText(
    "Connected",
  );
});

test("a reconnection in a second tab shows the same code", async ({ page, context }) => {
  const s = await seed({
    instances: [{ name: "Main", client: "Shop", status: "NEEDS_AUTH", locale: "es-ES" }],
  });
  await login(page, person(s, "owner"));
  await cell(page, "Main").getByRole("button", { name: "Reconnect" }).click();
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" });
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  const first = await qrPath(page);

  const other = await context.newPage();
  await other.goto(`/instances/${idOf(s, "Main")}`);
  await drawer(other).getByRole("button", { name: "Reconnect" }).click();
  await chooseQr(other);
  // pairInstance answers 409 pairing_in_progress, and the page attaches.
  await expect(qr(other)).toBeVisible();
  expect(await qrPath(other)).toBe(first);

  await next();
  await expect(drawer(page).getByRole("heading", { name: "Main is connected" })).toBeVisible();
  await expect(drawer(other).getByRole("heading", { name: "Main is connected" })).toBeVisible();
  await other.close();
});

test("a client user reconnects its one number, which has no locale", async ({ page }) => {
  const s = await seed({
    instances: [{ name: "Old", client: "Shop", status: "NEEDS_AUTH", locale: null }],
  });
  await login(page, person(s, "shopper"));
  const screen = page.getByRole("main").locator('[data-slot="instance"]');
  await expect(screen.getByRole("status")).toHaveText("Disconnected");
  await screen.getByRole("button", { name: "Reconnect" }).click();
  await expect(
    drawer(page).getByRole("heading", { name: "Reconnect Old", level: 2 }),
  ).toBeFocused();

  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" });
  const pair = page.waitForRequest(isPair);
  await chooseQr(page);
  // With no stored locale, the page sends the automatic one.
  expect((await pair).postDataJSON()).toEqual({ pairing: "qr", locale: "en-US" });
  await expect(qr(page)).toBeVisible();
  await next();
  await expect(drawer(page).getByRole("heading", { name: "Old is connected" })).toBeVisible();
  await button(page, "Done").click();
  await expect(drawer(page)).toHaveCount(0);
  await expect(screen.getByRole("status")).toHaveText("Connected");

  // The entry of "Reconnect" keeps no reconnection: a reload shows its screen again.
  await page.reload();
  await expect(screen.getByRole("status")).toHaveText("Connected");
  await expect(drawer(page)).toHaveCount(0);
});

test("the first pairing of a client user keeps Connect your WhatsApp, with Continue linking", async ({
  page,
}) => {
  const s = await seed({ instances: [] });
  await login(page, person(s, "shopper"));
  await page.getByRole("button", { name: "Connect a WhatsApp number" }).click();
  await firstSteps(page, "Shop");
  await pairing({ step: "update", qr: "2@first" }, { step: "wait" }, { step: "connected" });
  await chooseQr(page);
  await expect(qr(page)).toBeVisible();
  const first = await qrPath(page);

  await button(page, "Close").click();
  await expect(
    page.getByRole("heading", { name: "Connect your WhatsApp", level: 1 }),
  ).toBeVisible();
  // On a phone the Close button was over the header: the mouse of the test leaves it, so a hover
  // does not open the card of services over the screen.
  await page.mouse.move(1, (page.viewportSize()?.height ?? 800) - 1);
  const resume = page.getByRole("button", { name: "Continue linking Shop" });
  await expect(resume).toBeVisible();
  await resume.click();
  await expect(qr(page)).toBeVisible();
  expect(await qrPath(page)).toBe(first);
  // Behind the drawer, the screen does not change. A modal drawer hides it from the
  // accessibility tree, so the heading has a CSS locator.
  await expect(page.locator("main h1", { hasText: "Connect your WhatsApp" })).toBeAttached();

  await next();
  await expect(drawer(page).getByRole("heading", { name: "Shop is connected" })).toBeVisible();
});

test("a person that cannot connect gets the reason, and no form", async ({ page }) => {
  const s = await seed({
    clients: [{ name: "Shop", can_create_instances: false }],
    instances: [],
  });
  await login(page, person(s, "shopper"));
  await expect(
    page.getByRole("heading", { name: "Connect your WhatsApp", level: 1 }),
  ).toBeVisible();
  await page.goto(`/instances/new?client=${clientId(s, "Shop")}`);
  await expect(drawer(page)).toContainText("You cannot connect numbers now.");
  await expect(drawer(page).getByRole("textbox", { name: "Name" })).toHaveCount(0);
});
