import type { Locator, Page } from "@playwright/test";
import {
  closeMenu,
  control,
  expect,
  healthButton,
  healthState,
  login,
  mainLink,
  type Person,
  seedBasic,
  test,
} from "./support.ts";

// A check runs each 15 s, and the state needs a second check 2 s later.
const detect = { timeout: 45_000 };

test.use({ tolerateFailedRequests: true });

type Role = {
  name: string;
  person: (s: Awaited<ReturnType<typeof seedBasic>>) => Person;
  ok: string;
  core: string;
  noAnswer: string;
  coreNotice: string;
  backendNotice: string;
  services: string[]; // the names on the card
  coreValue: Record<string, string>; // value of each service while the core is stopped
  backendValue: Record<string, string>; // value of each service while the backend does not answer
};

const roles: Role[] = [
  {
    name: "owner",
    person: (s) => s.owner,
    ok: "Backend and core fully operational",
    core: "Core stopped",
    noAnswer: "Calyx does not answer",
    coreNotice: "The core does not answer.",
    backendNotice: "Calyx does not answer.",
    services: ["Backend", "Core", "Database", "Mail"],
    coreValue: {
      Backend: "Operational",
      Core: "Stopped",
      Database: "Operational",
      Mail: "Configured",
    },
    backendValue: { Backend: "No answer", Core: "Unknown", Database: "Unknown", Mail: "Unknown" },
  },
  {
    name: "client user",
    person: (s) => s.client,
    ok: "Calyx is fully operational",
    core: "Calyx cannot reach WhatsApp",
    noAnswer: "Calyx does not answer",
    coreNotice: "Calyx cannot reach WhatsApp now.",
    backendNotice: "Calyx does not answer.",
    services: ["Calyx", "WhatsApp service"],
    coreValue: { Calyx: "Operational", "WhatsApp service": "Stopped" },
    backendValue: { Calyx: "No answer", "WhatsApp service": "Unknown" },
  },
];

// The live region of the health status. At the phone width its button is in the closed Sheet.
const header = (page: Page, text: string): Locator => healthState(page, new RegExp(`^${text}`));
const card = (page: Page) => page.locator('[data-slot="health-card"]');
const service = (page: Page, name: string) =>
  card(page).locator('[data-slot="health-service"]', {
    has: page.getByText(name, { exact: true }),
  });
const notice = (page: Page) => page.getByRole("alert");
// The cell of the owner, or the screen of the one number of the client user.
const instance = (page: Page) => page.locator('[data-slot="instance"]', { hasText: "Main" });

async function expectCard(page: Page, role: Role, values: Record<string, string>, head: string) {
  // The card opens with focus. Hover and touch open it too.
  await (await healthButton(page, new RegExp(`^${head}`))).focus();
  await expect(card(page)).toBeVisible();
  for (const name of role.services) {
    await expect(service(page, name)).toContainText(values[name] as string);
  }
}

for (const role of roles) {
  test.describe(role.name, () => {
    test.beforeEach(async ({ page }) => {
      await login(page, role.person(await seedBasic()));
      await expect(header(page, role.ok)).toBeVisible();
      await expect(instance(page)).toContainText("Connected");
    });

    test("the core stops and starts", async ({ page }) => {
      await control("POST", "/core", { action: "stop" });
      await expect(header(page, role.core)).toBeVisible(detect);
      await expect(notice(page)).toContainText(role.coreNotice);
      // The last known state is not shown as current.
      await expect(instance(page)).toContainText("Unknown");
      await expectCard(page, role, role.coreValue, role.core);
      await expect(card(page)).toContainText("Last good check at");
      await page.keyboard.press("Escape");
      await expect(card(page)).toBeHidden();
      await closeMenu(page);

      await control("POST", "/core", { action: "start" });
      await expect(header(page, role.ok)).toBeVisible(detect);
      await expect(notice(page)).toHaveCount(0);
      await expect(instance(page)).toContainText("Connected");
    });

    test("the backend stops and starts", async ({ page }) => {
      await control("POST", "/backend", { action: "stop" });
      await expect(header(page, role.noAnswer)).toBeVisible(detect);
      await expect(notice(page)).toContainText(role.backendNotice);
      // The data stays on the screen.
      await expect(instance(page)).toContainText("Unknown");
      await expect(instance(page)).toContainText("Main");
      await expectCard(page, role, role.backendValue, role.noAnswer);
      await expect(card(page)).toContainText("Last good check at");
      await page.keyboard.press("Escape");
      await expect(card(page)).toBeHidden();
      await closeMenu(page);

      await control("POST", "/backend", { action: "start", mail: false });
      await expect(header(page, role.ok)).toBeVisible(detect);
      await expect(notice(page)).toHaveCount(0);
      // After a new read, with no action.
      await expect(instance(page)).toContainText("Connected");
    });

    test("the core stops, and then the backend", async ({ page }) => {
      await control("POST", "/core", { action: "stop" });
      await expect(header(page, role.core)).toBeVisible(detect);
      await control("POST", "/backend", { action: "stop" });
      await expect(header(page, role.noAnswer)).toBeVisible(detect);
      await expect(instance(page)).toContainText("Unknown");

      await control("POST", "/backend", { action: "start", mail: false });
      await expect(header(page, role.core)).toBeVisible(detect); // The core is still stopped.
      await control("POST", "/core", { action: "start" });
      await expect(header(page, role.ok)).toBeVisible(detect);
      await expect(instance(page)).toContainText("Connected");
    });
  });
}

// The program has no control for a stopped database, so the state `database` has only the unit test
// of health.test.ts.

// While the state is `core`, `database` or `backend`, the changes that need the core are
// disabled. Rename, remove and log out stay enabled.
test.describe("the changes that need the core", () => {
  type Seeded = { users: Person[]; instances: { name: string; id: string }[] };

  // `extra`: more instances, for example one that shows "Reconnect".
  async function seedOpen(extra: Record<string, unknown>[] = []): Promise<Seeded> {
    const { status, body } = await control<Seeded>("POST", "/seed", {
      organization: { name: "Acme", acknowledged: true },
      clients: [{ name: "Shop", can_create_instances: true }],
      users: [
        { label: "owner", role: "owner" },
        { label: "shopper", role: "client", client: "Shop" },
      ],
      instances: [
        { name: "Main", client: "Shop", provider: "WHATSAPP_WEB", status: "CONNECTED" },
        ...extra,
      ],
    });
    expect(status).toBe(200);
    return body;
  }

  // The card of the instance: in the drawer for the owner, the screen for the client user.
  async function expectCardControls(scope: Locator, removeName: string) {
    await expect(scope.getByRole("switch", { name: "Calyx access of Main" })).toBeDisabled();
    await expect(scope.getByRole("combobox", { name: "Use" })).toBeDisabled();
    await expect(scope.getByRole("button", { name: "Rename" })).toBeEnabled();
    await expect(scope.getByRole("button", { name: removeName })).toBeEnabled();
  }

  // "Lost" shows an enabled "Reconnect" before the outage. During it, no enabled "Reconnect"
  // shows: "Lost" is "Unknown", and a "Reconnect" would be disabled.
  const lost = [{ name: "Lost", provider: "WHATSAPP_WEB", status: "NEEDS_AUTH" }];
  const enabledReconnect = (page: Page) =>
    page
      .locator("main")
      .getByRole("button", { name: "Reconnect", exact: true })
      .and(page.locator(":enabled"));

  async function expectLogOutEnabled(page: Page) {
    await (await mainLink(page, "Account")).click();
    await expect(page.getByRole("button", { name: "Log out" })).toBeEnabled();
  }

  test("owner, the core is stopped", async ({ page }) => {
    const s = await seedOpen(lost);
    await login(page, s.users[0] as Person);
    await expect(header(page, "Backend and core fully operational")).toBeVisible();
    const main = page.getByRole("main");
    const connect = main
      .getByRole("region", { name: "Shop", exact: true })
      .getByRole("button", { name: "+ Connect a number" });
    await expect(connect).toBeEnabled();
    await expect(enabledReconnect(page)).toHaveCount(1);

    await control("POST", "/core", { action: "stop" });
    await expect(header(page, "Core stopped")).toBeVisible(detect);
    await expect(connect).toBeDisabled();
    await expect(enabledReconnect(page)).toHaveCount(0);
    for (const c of await main.getByRole("button", { name: "+ Connect a number" }).all()) {
      await expect(c).toBeDisabled();
    }
    await expect(instance(page).getByRole("switch")).toBeDisabled();

    await instance(page).getByRole("link").click();
    const drawer = page.locator('[role="dialog"][data-slot="sheet-content"]');
    await expect(drawer).toBeVisible();
    await expectCardControls(drawer, "Remove this instance");
    await drawer.getByRole("button", { name: "Close" }).first().click();
    await expectLogOutEnabled(page);
  });

  test("client user, the core is stopped", async ({ page }) => {
    const s = await seedOpen();
    await login(page, s.users[1] as Person);
    await expect(header(page, "Calyx is fully operational")).toBeVisible();
    const another = page.getByRole("button", { name: "Connect another number" });
    await expect(another).toBeEnabled();

    await control("POST", "/core", { action: "stop" });
    await expect(header(page, "Calyx cannot reach WhatsApp")).toBeVisible(detect);
    await expect(another).toBeDisabled();
    await expectCardControls(instance(page), "Remove this WhatsApp number");
    await expectLogOutEnabled(page);
  });

  test("owner, the backend does not answer", async ({ page }) => {
    const s = await seedOpen([...lost, { name: "Front desk", client: "Shop", state: "creating" }]);
    await login(page, s.users[0] as Person);
    await expect(header(page, "Backend and core fully operational")).toBeVisible();
    // The data stays on the screen only when the page has it.
    await expect(instance(page)).toContainText("Connected");
    await expect(enabledReconnect(page)).toHaveCount(1);
    const linking = page.locator('[data-slot="instance"]', { hasText: "Front desk" });
    await expect(linking).toContainText("Linking. Continue");

    await control("POST", "/backend", { action: "stop" });
    await expect(header(page, "Calyx does not answer")).toBeVisible(detect);
    // Unknown wins over the state of the data, also for a number that is linking.
    await expect(linking).toContainText("Unknown");
    await expect(linking).not.toContainText("Linking");
    await expect(
      page.getByRole("main").getByRole("button", { name: "+ Connect a number" }).first(),
    ).toBeDisabled();
    await expect(enabledReconnect(page)).toHaveCount(0);
    await instance(page).getByRole("link").click();
    const drawer = page.locator('[role="dialog"][data-slot="sheet-content"]');
    await expectCardControls(drawer, "Remove this instance");
  });
});

// After the return of the backend, the old state does not look current until a
// new read of the list succeeds. The test holds that read for 4 s.
for (const role of roles) {
  test(`${role.name}: no old state looks current after the return of the backend`, async ({
    page,
  }) => {
    await login(page, role.person(await seedBasic()));
    await expect(header(page, role.ok)).toBeVisible();
    // The word of the state: the cell of the owner, or the status of the screen of the client
    // user (its card also says "Last known state: Connected").
    const word = role.name === "owner" ? instance(page) : instance(page).getByRole("status");
    await expect(word).toContainText("Connected");

    await control("POST", "/backend", { action: "stop" });
    await expect(header(page, role.noAnswer)).toBeVisible(detect);
    await expect(word).toContainText("Unknown");

    await page.route("**/api/v1/instances", async (route) => {
      await new Promise((r) => setTimeout(r, 4000));
      await route.continue().catch(() => undefined);
    });
    await control("POST", "/backend", { action: "start", mail: false });
    await expect(header(page, role.ok)).toBeVisible(detect);
    await expect(word).toContainText("Unknown");
    await expect(word).not.toContainText("Connected");
    await expect(word).toContainText("Connected", { timeout: 15_000 });
  });
}
