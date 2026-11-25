import { readFileSync } from "node:fs";
import type { Locator, Page } from "@playwright/test";
import { people } from "../../src/domain/people.ts";
import { control, expect, healthButton, login, mainLink, type Person, test } from "./support.ts";

// The small details of the panel.

async function seedOwner() {
  const { status, body } = await control<{ organization_id: string; users: Person[] }>(
    "POST",
    "/seed",
    {
      organization: { name: "Acme", acknowledged: true },
      users: [{ label: "owner", role: "owner" }],
    },
  );
  expect(status).toBe(200);
  return { owner: body.users[0] as Person, orgId: body.organization_id };
}

test.describe("the owner sees the instance limit and who changes it", () => {
  test("the limit, the commands for the server admin and who is who", async ({ page }) => {
    const { owner, orgId } = await seedOwner();
    await login(page, owner);
    await (await mainLink(page, "Account")).waitFor();
    await page.goto("/organization");
    const main = page.locator("main");
    await expect(page.getByRole("heading", { name: "Organization", level: 1 })).toBeVisible();

    await expect(main.getByText("Instances: 0 of 10.")).toBeVisible();
    const limit = main.getByText("Only the server admin can change this limit.", { exact: true });
    await expect(limit).toBeVisible();

    // The help is a popover on the row of the limit. It floats: the section keeps its size.
    const trigger = main.getByRole("button", { name: "How do I change the limit?" });
    await expect(trigger).toHaveAttribute("aria-expanded", "false");
    if (test.info().project.name === "desktop-1280") {
      const a = await limit.boundingBox();
      const b = await trigger.boundingBox();
      expect(
        Math.abs((a?.y ?? 0) + (a?.height ?? 0) / 2 - ((b?.y ?? 0) + (b?.height ?? 0) / 2)),
      ).toBeLessThan(4);
    }
    const section = page.getByRole("region", { name: "Organization" });
    const height = (await section.boundingBox())?.height;
    const popover = page.locator('[data-slot="popover-content"]');

    // The keyboard opens it, and the focus goes to the selected tab.
    await trigger.focus();
    await page.keyboard.press("Enter");
    await expect(popover).toBeVisible();
    await expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect((await section.boundingBox())?.height).toBe(height);
    await expect(popover).toHaveAccessibleName("How do I change the limit?");
    const tabs = popover.getByRole("tablist", { name: "How do you run Calyx?" });
    const dockerTab = tabs.getByRole("tab", { name: "Docker" });
    const manualTab = tabs.getByRole("tab", { name: "Manual" });
    await expect(dockerTab).toHaveAttribute("aria-selected", "true");
    await expect(dockerTab).toBeFocused();
    // The selected tab has the primary colour of the Save button.
    const background = (l: Locator) => l.evaluate((el) => getComputedStyle(el).backgroundColor);
    const primary = await background(main.getByRole("button", { name: "Save" }));
    expect(await background(dockerTab)).toBe(primary);
    expect(await background(manualTab)).not.toBe(primary);

    const docker = `docker compose exec backend backend org set --id ${orgId} --max-instances <number>`;
    const manual = `backend org set --id ${orgId} --max-instances <number>`;
    const panel = popover.getByRole("tabpanel");
    await expect(panel.locator("code")).toHaveText(docker);
    await expect(panel.locator("code")).toHaveCSS("font-family", /mono/i);
    await expect(popover.getByRole("button", { name: "Copy the Docker command" })).toBeVisible();
    await expect(popover.getByRole("button", { name: /^Copy/ })).toHaveCount(1);

    // Tab goes from the selected tab to the panel, and the panel shows a focus ring.
    await page.keyboard.press("Tab");
    await expect(panel).toBeFocused();
    await expect(panel).not.toHaveCSS("box-shadow", "none");
    await page.keyboard.press("Shift+Tab");
    await expect(dockerTab).toBeFocused();

    await page.keyboard.press("ArrowRight");
    await expect(manualTab).toBeFocused();
    await expect(manualTab).toHaveAttribute("aria-selected", "true");
    await expect(panel).toHaveCount(1);
    await expect(panel.locator("code")).toHaveText(manual);
    await expect(popover.getByRole("button", { name: "Copy the command" })).toBeVisible();
    await expect(popover.getByRole("button", { name: /^Copy/ })).toHaveCount(1);
    await page.keyboard.press("ArrowLeft");
    await expect(dockerTab).toHaveAttribute("aria-selected", "true");

    // Escape closes it and gives the focus back to the trigger.
    await page.keyboard.press("Escape");
    await expect(popover).toHaveCount(0);
    await expect(trigger).toBeFocused();
    await expect(trigger).toHaveAttribute("aria-expanded", "false");
    await expect(main.locator("details")).toHaveCount(0);

    // A click opens it too, with Docker selected again.
    await trigger.click();
    await expect(popover).toBeVisible();
    await expect(dockerTab).toHaveAttribute("aria-selected", "true");
    await manualTab.click();
    await expect(panel).toHaveCount(1);
    await expect(panel.locator("code")).toHaveText(manual);
    await page.keyboard.press("Escape");
    await expect(popover).toHaveCount(0);

    await expect(main.getByRole("heading", { name: "Who is who", level: 3 })).toBeVisible();
    for (const person of people) {
      await expect(main.getByText(person.text, { exact: true })).toBeVisible();
      await expect(main.getByText(person.name, { exact: true })).toBeVisible();
    }
  });
});

test.describe("the limit help at a width of 320 px", () => {
  test.use({ viewport: { width: 320, height: 640 } });
  test.beforeEach(() => test.skip(test.info().project.name !== "phone-360", "320 px once"));

  test("the popover fits in the viewport and the page does not scroll sideways", async ({
    page,
  }) => {
    const { owner } = await seedOwner();
    await login(page, owner);
    await (await mainLink(page, "Account")).waitFor();
    await page.goto("/organization");
    await page.locator("main").getByRole("button", { name: "How do I change the limit?" }).click();
    const popover = page.locator('[data-slot="popover-content"]');
    await expect(popover).toBeVisible();
    const fit = await page.evaluate(() => ({
      scroll: document.documentElement.scrollWidth,
      inner: window.innerWidth,
    }));
    expect(fit.scroll).toBeLessThanOrEqual(fit.inner);
    const box = await popover.boundingBox();
    expect(box?.x ?? -1).toBeGreaterThanOrEqual(0);
    expect((box?.x ?? 0) + (box?.width ?? 0)).toBeLessThanOrEqual(320);
  });
});

test.describe("the API keys screen creates a key in place", () => {
  test("the button, the empty line and the form in the section", async ({ page }) => {
    const { owner } = await seedOwner();
    await login(page, owner);
    await (await mainLink(page, "Account")).waitFor();
    await page.goto("/developers/api-keys");
    const section = page.getByRole("region", { name: "Your API keys" });
    await expect(
      section.getByText(
        "You have no API keys. A key lets your software use Calyx. To connect your program, read Connect a program.",
        {
          exact: true,
        },
      ),
    ).toBeVisible();
    await section.getByRole("button", { name: "Create API key" }).click();
    await expect(section.getByRole("button", { name: "Save", exact: true })).toBeVisible();
    await expect(section.getByRole("button", { name: "Cancel", exact: true })).toBeVisible();
    await expect(page.getByRole("region", { name: "Create a key" })).toHaveCount(0);
  });
});

test.describe("the small texts and the chevrons", () => {
  const turned = (summary: Locator) =>
    summary.locator("svg").evaluate((el) => getComputedStyle(el).rotate);

  test("the Add a client text, the filter chevrons and Details", async ({ page }) => {
    const { owner } = await seedOwner();
    await login(page, owner);
    await (await mainLink(page, "Account")).waitFor();
    await page.goto("/");
    const main = page.locator("main");

    await main.getByRole("button", { name: "Add a client" }).click();
    const ask = page.getByRole("dialog", { name: "Add a client" });
    await expect(
      ask
        .getByLabel("Can this client connect numbers?")
        .locator("option", { hasText: "Yes, up to 1 for now. You can change it later in Limits." }),
    ).toHaveCount(1);
    await ask.getByRole("button", { name: "Cancel" }).click();

    const fold = main.getByRole("button", { name: /^Filters/ });
    if (await fold.isVisible()) await fold.click();
    for (const [name, openAtStart] of [
      ["State", true],
      ["Clients", false],
    ] as const) {
      const summary = main.locator("summary", { hasText: name });
      const chevron = summary.locator('svg[aria-hidden="true"]');
      await expect(chevron).toHaveCount(1);
      const details = main
        .locator("details")
        .filter({ has: page.locator("summary", { hasText: name }) });
      if (!openAtStart) await summary.click();
      await expect(details).toHaveAttribute("open", "");
      await expect.poll(() => turned(summary)).toBe("180deg");
      await summary.click();
      await expect(details).not.toHaveAttribute("open", "");
      await expect.poll(() => turned(summary)).toBe("none");
    }

    const health = await healthButton(page, /^Backend and core fully operational/);
    await expect(health).toContainText("Details");
    await expect(health.locator('svg[aria-hidden="true"]')).toHaveCount(1);
  });
});

test.describe("the last card of the grid of each client", () => {
  const shop = { name: "Shop", can_create_instances: true };
  const main = {
    name: "Main",
    client: "Shop",
    provider: "WHATSAPP_WEB",
    status: "CONNECTED",
  };

  async function enter(page: Page, body: Record<string, unknown>) {
    const { status, body: s } = await control<{ users: Person[] }>("POST", "/seed", {
      organization: { name: "Acme", acknowledged: true },
      users: [{ label: "owner", role: "owner" }],
      ...body,
    });
    expect(status).toBe(200);
    await login(page, s.users[0] as Person);
    const group = page.locator("main").getByRole("region", { name: "Shop", exact: true });
    await expect(group).toBeVisible();
    return group;
  }
  const card = (group: Locator) => group.locator('[data-slot="connect-card"]');
  const button = (group: Locator) => group.getByRole("button", { name: "+ Connect a number" });

  test("the client can create an instance: the enabled button, last, and not in the header", async ({
    page,
  }) => {
    const group = await enter(page, { clients: [shop], instances: [main] });
    await expect(button(group)).toBeEnabled();
    await expect(group.getByRole("button", { name: /Connect a number/ })).toHaveCount(1);
    await expect(card(group).locator("xpath=following-sibling::*")).toHaveCount(0);
    await expect(group.locator("> div").first().getByRole("button")).toHaveCount(0);
    await button(group).click();
    await expect(page).toHaveURL(/\/instances\/new\?client=.+/);
  });

  test("the core is down: the button is disabled", async ({ page }) => {
    const group = await enter(page, { clients: [shop], instances: [main] });
    await expect(button(group)).toBeEnabled();
    await control("POST", "/core", { action: "stop" });
    await expect(button(group)).toBeDisabled();
  });

  test("the client has a creation problem: the reason in place of the button", async ({ page }) => {
    const group = await enter(page, {
      clients: [{ ...shop, max_instances: 1 }],
      instances: [main],
    });
    await expect(card(group).locator('[data-slot="creation-problem"]')).toHaveCount(1);
    await expect(card(group)).toContainText("There is no free place for a number.");
    await expect(group.getByRole("button", { name: /Connect a number/ })).toHaveCount(0);
  });

  test("the organization blocks the creation: no card", async ({ page }) => {
    const group = await enter(page, {
      organization: { name: "Acme", acknowledged: true, whatsapp_web_allowed: false },
      clients: [shop],
      instances: [main],
    });
    await expect(group.getByText("Main")).toBeVisible();
    await expect(card(group)).toHaveCount(0);
  });

  test("a search or a filter is active: no card", async ({ page }) => {
    const group = await enter(page, { clients: [shop], instances: [main] });
    await expect(card(group)).toHaveCount(1);
    const find = page.locator("main").getByRole("searchbox", { name: "Find a number" });
    await find.fill("Mai");
    await expect(group.getByText("Main")).toBeVisible();
    await expect(card(group)).toHaveCount(0);
    await find.fill("");
    await expect(card(group)).toHaveCount(1);
  });

  test("a group with no instance: the card in place of the line", async ({ page }) => {
    const group = await enter(page, { clients: [shop] });
    await expect(group.getByText("No numbers yet.")).toHaveCount(0);
    await expect(button(group)).toBeEnabled();
  });
});

test.describe("the owner accepts the terms first", () => {
  const noticeText = [
    "Accept the WhatsApp Web terms before you connect a number.",
    "You accept them once, also if you use only the Meta Cloud API.",
  ];
  const blockedText =
    "The server admin blocked WhatsApp Web for this organization. You can still accept the terms now.";

  async function seedTerms(organization: Record<string, unknown>) {
    const { status, body } = await control<{
      users: Person[];
      instances: { name: string; id: string; path: string }[];
    }>("POST", "/seed", {
      organization: { name: "Acme", ...organization },
      clients: [{ name: "Shop", can_create_instances: true }],
      users: [
        { label: "owner", role: "owner" },
        { label: "shopper", role: "client", client: "Shop" },
      ],
      instances: [
        { name: "Main", client: "Shop", provider: "WHATSAPP_WEB", status: "CONNECTED" },
        { name: "Meta line", client: "Shop", provider: "META_CLOUD", status: "CONNECTED" },
      ],
    });
    expect(status).toBe(200);
    return {
      owner: body.users[0] as Person,
      shopper: body.users[1] as Person,
      main: body.instances[0] as { id: string; path: string },
    };
  }

  const notice = (page: Page) => page.locator('[data-slot="terms-notice"]');
  const banner = (page: Page) => page.locator('[data-slot="terms-banner"]');

  // Only the notice: no list, no group, no card and no drawer.
  async function onlyNotice(page: Page) {
    const main = page.locator("main");
    await expect(notice(page)).toBeVisible();
    for (const t of noticeText) await expect(notice(page)).toContainText(t);
    const link = notice(page).getByRole("link", { name: "Read and accept the terms" });
    await expect(link).toHaveAttribute("href", "/organization/whatsapp-web");
    await expect(main.getByText("Main", { exact: true })).toHaveCount(0);
    await expect(main.getByRole("region", { name: "Shop", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: /Connect/ })).toHaveCount(0);
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(banner(page)).toHaveCount(0);
  }

  test("before the acknowledgement: only the notice, also on a drawer path", async ({ page }) => {
    const { owner, main } = await seedTerms({ acknowledged: false });
    await login(page, owner);
    await expect(page).toHaveURL(/\/$/);
    await onlyNotice(page);
    await expect(notice(page)).not.toContainText(blockedText);

    await page.goto("/instances/new");
    await onlyNotice(page);
    await page.goto(main.path);
    await onlyNotice(page);
    await page.goto(`${main.path}/webhooks`);
    await onlyNotice(page);

    // The other screens show the banner with the same text and link.
    await page.goto("/organization");
    for (const t of noticeText) await expect(banner(page)).toContainText(t);
    await expect(
      banner(page).getByRole("link", { name: "Read and accept the terms" }),
    ).toHaveAttribute("href", "/organization/whatsapp-web");
    await expect(notice(page)).toHaveCount(0);
  });

  test("with WhatsApp Web blocked: the notice has the extra sentence", async ({ page }) => {
    const { owner } = await seedTerms({ acknowledged: false, whatsapp_web_allowed: false });
    await login(page, owner);
    await onlyNotice(page);
    await expect(notice(page)).toContainText(blockedText);
  });

  test("after the acknowledgement: the list and the card", async ({ page }) => {
    const { owner } = await seedTerms({ acknowledged: false });
    await login(page, owner);
    await notice(page).getByRole("link", { name: "Read and accept the terms" }).click();
    await expect(page).toHaveURL(/\/organization\/whatsapp-web$/);
    await page
      .getByLabel("I understand the risk, and I will tell my clients before they link a number.")
      .check();
    // The page waits for the new read of the person, so the notice never shows after "Accept".
    await page.evaluate(() => {
      const w = window as unknown as { noticeShown: boolean };
      w.noticeShown = false;
      new MutationObserver(() => {
        if (document.querySelector('[data-slot="terms-notice"]')) w.noticeShown = true;
      }).observe(document.body, { childList: true, subtree: true });
    });
    await page.getByRole("button", { name: "Accept" }).click();
    await expect(page).toHaveURL(/\/$/);
    const group = page.locator("main").getByRole("region", { name: "Shop", exact: true });
    await expect(group.getByText("Main", { exact: true })).toBeVisible();
    await expect(group.getByRole("button", { name: "+ Connect a number" })).toBeEnabled();
    await expect(notice(page)).toHaveCount(0);
    await expect(banner(page)).toHaveCount(0);
    expect(
      await page.evaluate(() => (window as unknown as { noticeShown: boolean }).noticeShown),
    ).toBe(false);
  });

  test("a client user: the list and the existing reason", async ({ page }) => {
    const { shopper } = await seedTerms({ acknowledged: false });
    await login(page, shopper);
    const main = page.locator("main");
    await expect(main.getByText("Main", { exact: true })).toBeVisible();
    await expect(main.getByText("Meta line", { exact: true })).toBeVisible();
    const reason = main.locator('[data-slot="creation-problem"]');
    await expect(reason).toContainText("The terms of WhatsApp Web are not accepted yet.");
    await expect(reason).toContainText("Ask Acme.");
    await expect(notice(page)).toHaveCount(0);
    await expect(banner(page)).toHaveCount(0);
  });
});

test.describe("the health details show the versions", () => {
  test("Panel, Backend and Core", async ({ page }) => {
    const { owner } = await seedOwner();
    await login(page, owner);
    await (await mainLink(page, "Account")).waitFor();
    await page.goto("/");
    await (await healthButton(page, /^Backend and core fully operational/)).click();
    const card = page.locator('[data-slot="health-card"]');
    await expect(card.getByText("Versions", { exact: true })).toBeVisible();
    // The build reads the version from the root package.json, so the test reads it too.
    const { version: rootVersion } = JSON.parse(
      readFileSync(new URL("../../../../package.json", import.meta.url), "utf8"),
    ) as { version: string };
    const value = (name: string) =>
      card.locator('[data-slot="health-version"]', { hasText: name }).locator("span").last();
    await expect(value("Panel")).toHaveText(rootVersion);
    await expect(value("Backend")).toHaveText("dev");
    await expect(value("Core")).toHaveText("0.0.0-fake");
  });
});
