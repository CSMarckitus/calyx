import { randomInt } from "node:crypto";
import { type Locator, type Page, request } from "@playwright/test";
import { control, expect, healthState, login, type Person, test } from "./support.ts";

// A removal that the core does not confirm, and a refused change, are answers that the browser
// logs as failed requests.
test.use({ tolerateFailedRequests: true });

type Seed = {
  organization?: Record<string, unknown>;
  clients?: Record<string, unknown>[];
  users?: Record<string, unknown>[];
  instances?: Record<string, unknown>[];
};
type Seeded = {
  clients: { name: string; id: string }[];
  users: (Person & { id: string })[];
  instances: { name: string; id: string; path: string }[];
};

async function seed(body: Seed): Promise<Seeded> {
  const { status, body: answer } = await control<Seeded>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    ...body,
  });
  expect(status).toBe(200);
  return answer;
}

const person = (s: Seeded, label: string) => s.users.find((u) => u.label === label) as Person;
const clientId = (s: Seeded, name: string) => s.clients.find((c) => c.name === name)?.id as string;

// A second login, outside the browser: the owner changes data that sends no event.
async function api(baseURL: string, who: Person) {
  const origin = new URL(baseURL).origin;
  const context = await request.newContext({
    baseURL,
    extraHTTPHeaders: {
      Origin: origin,
      "X-Forwarded-For": `10.${randomInt(256)}.${randomInt(256)}.${randomInt(1, 255)}`,
    },
  });
  const res = await context.post("/api/v1/auth/login", {
    data: { email: who.email, password: who.password },
  });
  expect(res.ok()).toBe(true);
  return context;
}

// The list, and not a drawer, which is outside <main>.
const main = (page: Page) => page.getByRole("main");
const cells = (page: Page) => main(page).locator('[data-slot="instance"]');
const cell = (page: Page, name: string) =>
  main(page).locator('[data-slot="instance"]', { has: page.getByText(name, { exact: true }) });
const group = (page: Page, name: string) => main(page).getByRole("region", { name, exact: true });
const reason = (page: Page) => main(page).locator('[data-slot="creation-problem"]');
const terms = (page: Page) => main(page).locator('[data-slot="terms-notice"]');
const search = (page: Page) => main(page).getByRole("searchbox", { name: "Find a number" });
const drawer = (page: Page) => page.getByRole("dialog").first();
const question = (page: Page, name: string) => page.getByRole("dialog", { name });

// At a narrow width the filters are behind the button "Filters".
async function stateFilter(page: Page, name: string): Promise<Locator> {
  const fold = main(page).getByRole("button", { name: /^Filters/ });
  if (await fold.isVisible()) {
    if ((await fold.getAttribute("aria-expanded")) !== "true") await fold.click();
  }
  return main(page)
    .getByRole("group", { name: "State" })
    .getByRole("button", { name: new RegExp(`^${name}`) });
}

const ownerHeader = (page: Page) => healthState(page, /^Backend and core fully operational/);

// The stream sends one `changed` at its start, and the page reads again after it. A change made
// before that read would show with no "Check again". This waits for that second good `getMe`.
function firstReads(page: Page) {
  let me = 0;
  page.on("response", (r) => {
    if (new URL(r.url()).pathname === "/api/v1/me" && r.status() === 200) me++;
  });
  return async () => {
    await expect.poll(() => me).toBeGreaterThanOrEqual(2);
  };
}

// Counts the requests of the page to each path.
function countRequests(page: Page) {
  const counts = new Map<string, number>();
  page.on("request", (r) => {
    const path = new URL(r.url()).pathname;
    counts.set(path, (counts.get(path) ?? 0) + 1);
  });
  return (path: string) => counts.get(path) ?? 0;
}

test.describe("a person that cannot connect a number", () => {
  test("Check again: a client user", async ({ page, baseURL }) => {
    const s = await seed({
      clients: [{ name: "Shop", can_create_instances: false }],
      users: [
        { label: "owner", role: "owner" },
        { label: "shopper", role: "client", client: "Shop" },
      ],
    });
    const settled = firstReads(page);
    await login(page, person(s, "shopper"));
    await expect(page.getByRole("heading", { name: "Connect your WhatsApp" })).toBeVisible();
    await settled();
    await expect(reason(page)).toContainText("You cannot connect numbers now.");
    await expect(reason(page)).toContainText("Ask Acme to allow it.");
    await expect(main(page).getByRole("button", { name: /^Connect/ })).toHaveCount(0);

    const owner = await api(baseURL as string, person(s, "owner"));
    const res = await owner.patch(`/api/v1/clients/${clientId(s, "Shop")}`, {
      data: { can_create_instances: true },
    });
    expect(res.status()).toBe(200);
    await owner.dispose();
    // No event comes for this change, so the page shows the old reason until Check again.
    await page.waitForTimeout(1500);
    await expect(reason(page)).toBeVisible();

    const reads = countRequests(page);
    await reason(page).getByRole("button", { name: "Check again" }).click();
    await expect(
      main(page).getByRole("button", { name: "Connect a WhatsApp number" }),
    ).toBeVisible();
    await expect(reason(page)).toHaveCount(0);
    expect(reads("/api/v1/me")).toBeGreaterThanOrEqual(1);
  });

  test("an owner with no acknowledgement: only the terms notice until the acknowledgement", async ({
    page,
    baseURL,
  }) => {
    const s = await seed({
      organization: { name: "Acme", acknowledged: false },
      users: [{ label: "owner", role: "owner" }],
    });
    await login(page, person(s, "owner"));
    await expect(terms(page)).toBeVisible();
    await expect(
      terms(page).getByRole("link", { name: "Read and accept the terms" }),
    ).toHaveAttribute("href", "/organization/whatsapp-web");
    await expect(group(page, "My numbers")).toHaveCount(0);
    await expect(reason(page)).toHaveCount(0);

    const owner = await api(baseURL as string, person(s, "owner"));
    const res = await owner.post("/api/v1/organization/whatsapp-web-acknowledgement", {
      data: { accept: true },
    });
    expect(res.status()).toBe(200);
    await owner.dispose();
    await page.reload();
    await expect(terms(page)).toHaveCount(0);
    await expect(
      group(page, "My numbers").getByRole("button", { name: "+ Connect a number" }),
    ).toBeEnabled();
  });

  type Case = {
    name: string;
    seed: Seed;
    // "notice": the owner sees only the terms notice.
    owner: { cause: string; next: string; where: "top" | "Shop" } | "notice" | null;
    client: { cause: string; next: string };
  };
  const cases: Case[] = [
    {
      name: "WhatsApp Web blocked",
      seed: { organization: { name: "Acme", acknowledged: true, whatsapp_web_allowed: false } },
      owner: {
        cause: "WhatsApp Web is blocked for this organization.",
        next: "Ask the server admin.",
        where: "top",
      },
      client: { cause: "WhatsApp Web is blocked for this organization.", next: "Ask Acme." },
    },
    {
      name: "no acknowledgement",
      seed: { organization: { name: "Acme", acknowledged: false } },
      owner: "notice",
      client: { cause: "The terms of WhatsApp Web are not accepted yet.", next: "Ask Acme." },
    },
    {
      name: "the organization is full",
      seed: {
        organization: { name: "Acme", acknowledged: true, max_instances: 1 },
        instances: [{ name: "Main", client: "Shop", status: "CONNECTED" }],
      },
      owner: {
        cause: "There is no free place for a number.",
        next: "Raise the limit of the client. When the organization is full, ask the server admin.",
        where: "top",
      },
      client: { cause: "There is no free place for a number.", next: "Ask Acme." },
    },
    {
      name: "the client is full",
      seed: {
        clients: [{ name: "Shop", can_create_instances: true, max_instances: 1 }],
        instances: [{ name: "Main", client: "Shop", status: "CONNECTED" }],
      },
      owner: {
        cause: "There is no free place for a number.",
        next: "Raise the limit of the client. When the organization is full, ask the server admin.",
        where: "Shop",
      },
      client: { cause: "There is no free place for a number.", next: "Ask Acme." },
    },
    {
      name: "no permission",
      seed: { clients: [{ name: "Shop", can_create_instances: false }] },
      // `can_create_instances` does not limit an owner.
      owner: null,
      client: { cause: "You cannot connect numbers now.", next: "Ask Acme to allow it." },
    },
  ];

  for (const c of cases) {
    test(`each reason: ${c.name}`, async ({ page }) => {
      const s = await seed({
        clients: [{ name: "Shop", can_create_instances: true }],
        users: [
          { label: "owner", role: "owner" },
          { label: "shopper", role: "client", client: "Shop" },
        ],
        ...c.seed,
      });

      await login(page, person(s, "owner"));
      if (c.owner === "notice") {
        await expect(terms(page)).toBeVisible();
        await expect(group(page, "Shop")).toHaveCount(0);
        await expect(reason(page)).toHaveCount(0);
        await expect(main(page).getByRole("button", { name: /Connect/ })).toHaveCount(0);
      } else {
        await expect(group(page, "Shop")).toBeVisible();
      }
      const connectMine = group(page, "My numbers").getByRole("button", {
        name: "+ Connect a number",
      });
      const connectShop = group(page, "Shop").getByRole("button", { name: "+ Connect a number" });
      if (c.owner === null) {
        await expect(reason(page)).toHaveCount(0);
        await expect(connectMine).toBeVisible();
        await expect(connectShop).toBeVisible();
      } else if (c.owner !== "notice") {
        await expect(reason(page)).toHaveCount(1);
        await expect(reason(page)).toContainText(c.owner.cause);
        await expect(reason(page)).toContainText(c.owner.next);
        await expect(reason(page).getByRole("button", { name: "Check again" })).toBeVisible();
        await expect(connectShop).toHaveCount(0);
        if (c.owner.where === "top") {
          await expect(
            group(page, "My numbers").locator('[data-slot="creation-problem"]'),
          ).toHaveCount(0);
          await expect(connectMine).toHaveCount(0);
        } else {
          await expect(group(page, "Shop").locator('[data-slot="creation-problem"]')).toHaveCount(
            1,
          );
          await expect(connectMine).toBeVisible();
        }
      }

      // The next person logs in on a new page.
      await page.context().clearCookies();
      await login(page, person(s, "shopper"));
      await expect(reason(page)).toContainText(c.client.cause);
      await expect(reason(page)).toContainText(c.client.next);
      await expect(reason(page).getByRole("button", { name: "Check again" })).toBeVisible();
      // Also the card "+ Connect a number".
      await expect(main(page).getByRole("button", { name: /Connect/ })).toHaveCount(0);
    });
  }

  test("an owner connects in My numbers of a new organization", async ({ page }) => {
    const s = await seed({ users: [{ label: "owner", role: "owner" }] });
    await login(page, person(s, "owner"));
    await expect(page.getByRole("heading", { name: "Instances", level: 1 })).toBeVisible();
    await expect(group(page, "My numbers")).toBeVisible();
    await expect(reason(page)).toHaveCount(0);
    await group(page, "My numbers").getByRole("button", { name: "+ Connect a number" }).click();
    await expect(page).toHaveURL(`/instances/new?client=${clientId(s, "My numbers")}`);
  });
});

// 23 numbers in 8 clients. Two of them need a person.
const many: [string, string, Record<string, unknown>][] = [
  ["Shop", "Bakery Lluna", { status: "NEEDS_AUTH" }],
  ["Wholesale orders", "Bakery Lluna", {}],
  ["Appointments", "Clinic Serra", {}],
  ["Reminders", "Clinic Serra", {}],
  ["Lab results", "Clinic Serra", {}],
  ["Reception", "Clinic Serra", { status: "CONNECTING" }],
  ["Support", "Garage Puig", {}],
  ["Old line", "Garage Puig", { enabled: false }],
  ["Invoices", "Northwind Systems", {}],
  ["Test line", "Northwind Systems", { enabled: false }],
  ["Bookings", "Hotel Miramar", {}],
  ["Reception desk", "Hotel Miramar", { status: "ERROR" }],
  ["Spa", "Hotel Miramar", {}],
  ["Events", "Hotel Miramar", {}],
  ["Orders", "Olives Ferrer", {}],
  ["Carriers", "Olives Ferrer", {}],
  ["Billing", "Transports Vidal", {}],
  ["Drivers north", "Transports Vidal", {}],
  ["Drivers south", "Transports Vidal", {}],
  ["Dispatch", "Transports Vidal", {}],
  ["Night shift", "Transports Vidal", { enabled: false, status: "NEEDS_AUTH" }],
  ["Enrolment", "Academy Brisa", {}],
  ["Teachers", "Academy Brisa", {}],
];

test("an owner with many clients finds one number", async ({ page }) => {
  const s = await seed({
    clients: [...new Set(many.map(([, c]) => c))].map((name) => ({ name })),
    users: [{ label: "owner", role: "owner" }],
    instances: many.map(([name, client, extra]) => ({
      name,
      client,
      status: "CONNECTED",
      ...extra,
    })),
  });
  await login(page, person(s, "owner"));
  await expect(cells(page)).toHaveCount(23);
  await expect(main(page).getByText("2 numbers need a person.")).toBeVisible();

  // A part of a name.
  await search(page).fill("rec");
  await expect(cells(page)).toHaveCount(2);
  await expect(cell(page, "Reception")).toBeVisible();
  await expect(cell(page, "Reception desk")).toBeVisible();
  await expect(cell(page, "Reception").locator("mark")).toHaveText("Rec");

  // A part of the name of a client gives its numbers.
  await search(page).fill("garage");
  await expect(cells(page)).toHaveCount(2);

  await search(page).fill("no such name");
  await expect(main(page).getByText("Nothing matches.")).toBeVisible();
  await main(page).getByRole("button", { name: "Show all" }).click();
  await expect(cells(page)).toHaveCount(23);

  // The filter "Need a person".
  const person_ = await stateFilter(page, "Need a person");
  await expect(person_).toContainText("2");
  await person_.click();
  await expect(person_).toHaveAttribute("aria-pressed", "true");
  await expect(cells(page)).toHaveCount(2);
  await expect(cell(page, "Shop")).toContainText("Disconnected");
  await expect(cell(page, "Reception desk")).toContainText("Problem");
  await (await stateFilter(page, "All")).click();
  await expect(cells(page)).toHaveCount(23);

  // "Show only these" sets the same filter.
  await main(page).getByRole("button", { name: "Show only these" }).click();
  await expect(cells(page)).toHaveCount(2);
  await (await stateFilter(page, "All")).click();

  // Open a number low in the list, and close it: the list stays at the same position.
  const target = cell(page, "Night shift");
  await target.scrollIntoViewIfNeeded();
  await page.evaluate(() => window.scrollBy(0, 40));
  const before = await page.evaluate(() => window.scrollY);
  expect(before).toBeGreaterThan(0);
  await target.getByRole("link", { name: /^Night shift/ }).click();
  await expect(page).toHaveURL(s.instances.find((i) => i.name === "Night shift")?.path as string);
  await expect(drawer(page).getByRole("heading", { name: "Night shift" })).toBeVisible();
  await drawer(page).getByRole("button", { name: "Close" }).first().click();
  await expect(page).toHaveURL("/");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(Math.abs((await page.evaluate(() => window.scrollY)) - before)).toBeLessThanOrEqual(2);

  // The search stays after the drawer closes.
  await search(page).fill("lab");
  await cell(page, "Lab results")
    .getByRole("link", { name: /^Lab results/ })
    .click();
  await expect(drawer(page).getByRole("heading", { name: "Lab results" })).toBeVisible();
  await drawer(page).getByRole("button", { name: "Close" }).first().click();
  await expect(search(page)).toHaveValue("lab");
  await expect(cells(page)).toHaveCount(1);
});

test.describe("an instance in Problem", () => {
  for (const role of ["owner", "client user"] as const) {
    for (const answer of [204, 202] as const) {
      test(`${role} removes it, with ${answer}`, async ({ page }) => {
        const s = await seed({
          clients: [{ name: "Hotel", can_create_instances: true }],
          users: [
            { label: "owner", role: "owner" },
            { label: "hotel", role: "client", client: "Hotel" },
          ],
          instances: [{ name: "Reception desk", client: "Hotel", status: "ERROR" }],
        });
        if (answer === 202) {
          await control("POST", "/fault", { method: "RemoveInstance", code: "UNAVAILABLE" });
        }
        const owner = role === "owner";
        await login(page, person(s, owner ? "owner" : "hotel"));

        // An owner opens it from the list. A client user with one number sees it as its screen.
        let card: Locator;
        if (owner) {
          await expect(cell(page, "Reception desk")).toContainText("Problem");
          await cell(page, "Reception desk")
            .getByRole("link", { name: /^Reception desk/ })
            .click();
          card = drawer(page).locator('[data-slot="instance"]');
        } else {
          card = main(page).locator('[data-slot="instance"]');
        }
        await expect(card.getByRole("heading", { name: "Reception desk" })).toBeVisible();
        await expect(card).toContainText("Problem");
        await expect(card).toContainText("A new connection does not remove a ban.");
        await expect(card.getByRole("button", { name: "Reconnect" })).toHaveCount(0);

        await card.getByRole("button", { name: "Remove", exact: true }).click();
        const ask = question(page, "Remove Reception desk?");
        await expect(ask).toContainText("its webhooks and its bulk jobs");
        if (owner) {
          await expect(ask).not.toContainText("you need permission");
        } else {
          await expect(ask).toContainText(
            "To connect it again, you need permission to connect numbers.",
          );
        }
        const removal = page.waitForResponse(
          (r) => r.request().method() === "DELETE" && r.url().includes("/api/v1/instances/"),
        );
        await ask.getByRole("button", { name: "Remove", exact: true }).click();
        expect((await removal).status()).toBe(answer);

        if (answer === 204) {
          await expect(page).toHaveURL("/");
          await expect(page.getByRole("dialog")).toHaveCount(0);
          await expect(cell(page, "Reception desk")).toHaveCount(0);
          // The card "+ Connect a number", or "Connect your WhatsApp", appears by the normal rules.
          if (owner) {
            await expect(
              group(page, "Hotel").getByRole("button", { name: "+ Connect a number" }),
            ).toBeVisible();
          } else {
            await expect(
              page.getByRole("heading", { name: "Connect your WhatsApp" }),
            ).toBeVisible();
            await expect(
              main(page).getByRole("button", { name: "Connect a WhatsApp number" }),
            ).toBeVisible();
          }
        } else {
          await expect(card).toContainText("Removing");
          await expect(card).toContainText(
            "Calyx is removing this number. This can take some seconds.",
          );
          await expect(card.getByRole("button", { name: /^Remove/ })).toHaveCount(0);
          if (owner) {
            await drawer(page).getByRole("button", { name: "Close" }).first().click();
            await expect(cell(page, "Reception desk")).toContainText("Removing");
          }
        }
      });
    }
  }
});

test("200 instances in 60 clients", async ({ page }, testInfo) => {
  const clients = Array.from({ length: 60 }, (_, i) => `Client ${String(i + 1).padStart(2, "0")}`);
  const statuses = ["CONNECTED", "CONNECTED", "CONNECTED", "CONNECTING", "NEEDS_AUTH", "ERROR"];
  const s = await seed({
    clients: clients.map((name) => ({ name })),
    users: [{ label: "owner", role: "owner" }],
    instances: Array.from({ length: 200 }, (_, i) => ({
      name: `Line ${String(i + 1).padStart(3, "0")}`,
      client: clients[i % 60],
      status: statuses[i % statuses.length],
      enabled: i % 17 !== 0,
    })),
  });
  await login(page, person(s, "owner"));
  await expect(ownerHeader(page)).toBeVisible();
  await expect(cells(page)).toHaveCount(200);

  // The start of the page: at most two reads of each kind, and one stream (Performance and size).
  const reads = countRequests(page);
  await page.goto("/");
  await expect(cells(page)).toHaveCount(200);
  await page.waitForTimeout(4000);
  const counts = {
    getMe: reads("/api/v1/me"),
    listInstances: reads("/api/v1/instances"),
    listClients: reads("/api/v1/clients"),
    healthz: reads("/healthz"),
    followEvents: reads("/api/v1/events"),
  };
  testInfo.annotations.push({ type: "requests at the start", description: JSON.stringify(counts) });
  console.log(`[${testInfo.project.name}] requests at the start: ${JSON.stringify(counts)}`);
  expect(counts.getMe).toBeLessThanOrEqual(2);
  expect(counts.listInstances).toBeLessThanOrEqual(2);
  expect(counts.listClients).toBeLessThanOrEqual(2);
  expect(counts.healthz).toBeLessThanOrEqual(2);
  expect(counts.followEvents).toBe(1);

  // The time of a letter, on a medium phone: the CPU runs 4 times slower. It is from the key
  // event to the frame after the new list.
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: 4 });
  const times: number[] = [];
  await search(page).focus();
  for (const letter of ["l", "i", "n", "e", " ", "1"]) {
    await page.evaluate(() => {
      const w = window as unknown as { letterMs?: number };
      w.letterMs = undefined;
      document.addEventListener(
        "input",
        (e) => {
          const start = e.timeStamp;
          requestAnimationFrame(() =>
            setTimeout(() => {
              w.letterMs = performance.now() - start;
            }, 0),
          );
        },
        { once: true, capture: true },
      );
    });
    await page.keyboard.type(letter);
    const handle = await page.waitForFunction(
      () => (window as unknown as { letterMs?: number }).letterMs,
    );
    times.push(Math.round((await handle.jsonValue()) as number));
  }
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: 1 });
  await expect(cells(page)).toHaveCount(
    Array.from({ length: 200 }, (_, i) => `Line ${String(i + 1).padStart(3, "0")}`).filter((n) =>
      n.toLowerCase().includes("line 1"),
    ).length,
  );
  const letter = `${Math.max(...times)} ms (each letter: ${times.join(", ")} ms; CPU 4x slower)`;
  testInfo.annotations.push({ type: "time of a letter", description: letter });
  console.log(`[${testInfo.project.name}] time of a letter: ${letter}`);
});
