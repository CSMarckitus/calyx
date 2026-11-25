import type { Page } from "@playwright/test";
import { control, expect, healthState, mainLink, type Person, test } from "./support.ts";

// A wrong password, a 410, a 429 and the end of a login session are answers that the browser
// logs as failed requests.
test.use({ tolerateFailedRequests: true });

type Link = Person & { invite_path?: string; reset_path?: string };
type Access = { owner: Link; shopper: Link; invitee: Link };

// One organization with an owner (with a reset link), a client user of "Shop" with the instance
// "Main", and an invitation for the client "Bakery".
async function seedAccess(): Promise<Access> {
  const { status, body } = await control<{ users: Link[] }>("POST", "/seed", {
    organization: { name: "Acme", acknowledged: true },
    clients: [{ name: "Shop" }, { name: "Bakery" }],
    users: [
      { label: "owner", role: "owner", reset: true },
      { label: "shopper", role: "client", client: "Shop" },
      { label: "invitee", role: "client", client: "Bakery", pending: true },
    ],
    instances: [{ name: "Main", client: "Shop", provider: "WHATSAPP_WEB", status: "CONNECTED" }],
  });
  expect(status).toBe(200);
  const [owner, shopper, invitee] = body.users as [Link, Link, Link];
  return { owner, shopper, invitee };
}

// The health status gives the state of Calyx in the words of the role. Only a page with a user has it.
const header = (page: Page, role: "owner" | "client") =>
  healthState(
    page,
    role === "owner" ? /^Backend and core fully operational/ : /^Calyx is fully operational/,
  );

// Fills the form that is on the screen now, so the address (and its `next`) stays.
async function fillLogin(page: Page, email: string, password: string) {
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Log in" }).click();
}

// Ends the login session in another place: the cookie stays in the browser, but the backend
// does not accept it any more.
async function logoutElsewhere(page: Page, baseURL: string | undefined) {
  const res = await page.request.post("/api/v1/auth/logout", {
    headers: { Origin: new URL(baseURL ?? "").origin },
  });
  expect(res.status()).toBe(204);
}

const alert = (page: Page) => page.locator('[data-slot="failure"]');

// An owner with the instance "Owner line" in "My numbers", and a client user of "Shop" with "Main".
// Only the owner can see "Owner line".
async function seedTwoPeople(acknowledged = true): Promise<{ owner: Person; shopper: Person }> {
  const { status, body } = await control<{ users: Person[] }>("POST", "/seed", {
    organization: { name: "Acme", acknowledged },
    clients: [{ name: "Shop" }],
    users: [
      { label: "owner", role: "owner" },
      { label: "shopper", role: "client", client: "Shop" },
    ],
    instances: [
      { name: "Main", client: "Shop", provider: "WHATSAPP_WEB", status: "CONNECTED" },
      { name: "Owner line", provider: "WHATSAPP_WEB", status: "CONNECTED" },
    ],
  });
  expect(status).toBe(200);
  const [owner, shopper] = body.users as [Person, Person];
  return { owner, shopper };
}

// Records if a text shows in the page at any moment from now, also for one render only.
async function watchFor(page: Page, text: string) {
  await page.evaluate((t) => {
    const w = window as unknown as { seen: boolean };
    w.seen = false;
    const check = () => {
      if (document.body.textContent?.includes(t)) w.seen = true;
    };
    new MutationObserver(check).observe(document.body, {
      subtree: true,
      childList: true,
      characterData: true,
    });
  }, text);
}
const seen = (page: Page) => page.evaluate(() => (window as unknown as { seen: boolean }).seen);

// The next person logs in, and sees its own data: the one number "Main", and never "Owner line".
async function nextPersonSeesOwnData(page: Page, shopper: Person) {
  await watchFor(page, "Owner line");
  await fillLogin(page, shopper.email, shopper.password);
  await expect(header(page, "client")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Main" })).toBeVisible();
  await expect(header(page, "owner")).toHaveCount(0);
  expect(await seen(page)).toBe(false);
}

const termsBanner = (page: Page) => page.locator('[data-slot="terms-banner"]');
const termsNotice = (page: Page) => page.locator('[data-slot="terms-notice"]');
const termsText = [
  "Accept the WhatsApp Web terms before you connect a number.",
  "You accept them once, also if you use only the Meta Cloud API.",
];

test.describe("log in", () => {
  test("a wrong password stays on the form", async ({ page }) => {
    const { owner } = await seedAccess();
    await page.goto("/login");
    await fillLogin(page, owner.email, "not-the-password");

    await expect(alert(page)).toContainText("The email or the password is not correct.");
    await expect(alert(page)).toContainText('Try again, or select "Forgot password".');
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByLabel("Email")).toHaveValue(owner.email);
    await expect(header(page, "owner")).toHaveCount(0);
  });

  test("a 429 shows the wait of Retry-After", async ({ page }) => {
    const { owner } = await seedAccess();
    await page.goto("/login");
    // The backend allows 5 attempts for each email in a minute.
    let last = 0;
    for (let i = 0; i < 6; i++) {
      const answer = page.waitForResponse((r) => r.url().endsWith("/api/v1/auth/login"));
      await fillLogin(page, owner.email, `wrong-password-${i}`);
      last = (await answer).status();
    }
    expect(last).toBe(429);
    await expect(alert(page)).toContainText("Too many attempts.");
    await expect(alert(page)).toContainText("Wait 60 seconds and try again.");
  });

  test("the fields let a password manager work", async ({ page }) => {
    await page.goto("/login");
    await expect(page.getByLabel("Email")).toHaveAttribute("autocomplete", "username");
    await expect(page.getByLabel("Password", { exact: true })).toHaveAttribute(
      "autocomplete",
      "current-password",
    );
  });
});

test.describe("next", () => {
  test("a path for logged-in people goes to the login, and the login returns to it", async ({
    page,
  }) => {
    const { owner } = await seedAccess();
    await page.goto("/?x=1");
    await expect(page).toHaveURL(/\/login\?next=%2F%3Fx%3D1$/);
    await fillLogin(page, owner.email, owner.password);
    await expect(page).toHaveURL(/\/\?x=1$/);
    await expect(header(page, "owner")).toBeVisible();

    // With a user, /login and /forgot-password go to /.
    await page.goto("/login");
    await expect(page).toHaveURL(/\/$/);
    await page.goto("/forgot-password");
    await expect(page).toHaveURL(/\/$/);
  });

  test("a next of another origin, or of a public path, goes to /", async ({
    page,
    context,
    baseURL,
  }) => {
    const { owner } = await seedAccess();
    for (const next of ["//other.example/x", "https://other.example/x", "/invite?token=x"]) {
      await context.clearCookies();
      await page.goto(`/login?next=${encodeURIComponent(next)}`);
      await fillLogin(page, owner.email, owner.password);
      await expect(header(page, "owner")).toBeVisible();
      await expect(page).toHaveURL(`${baseURL}/`);
    }
  });
});

test.describe("the end of a login session", () => {
  test("the page goes to the login, and the login returns to the same place", async ({
    page,
    baseURL,
  }) => {
    const { owner } = await seedAccess();
    await page.goto("/login?next=%2F%3Fx%3D1");
    await fillLogin(page, owner.email, owner.password);
    await expect(header(page, "owner")).toBeVisible();

    await logoutElsewhere(page, baseURL);
    // The stream ends within 15 s after a logout, and its next start gets 401.
    await expect(page).toHaveURL(/\/login\?next=%2F%3Fx%3D1$/, { timeout: 30_000 });
    await fillLogin(page, owner.email, owner.password);
    await expect(page).toHaveURL(/\/\?x=1$/);
    await expect(header(page, "owner")).toBeVisible();
  });

  test("the cache and the drafts are cleared after a logout", async ({ page, baseURL }) => {
    const { owner, shopper } = await seedTwoPeople();
    await page.goto("/login");
    await fillLogin(page, owner.email, owner.password);
    await expect(header(page, "owner")).toBeVisible();
    await expect(page.getByText("Owner line")).toBeVisible();
    await page.evaluate(() =>
      sessionStorage.setItem("calyx.draft.client.x", JSON.stringify({ name: "Old" })),
    );

    await logoutElsewhere(page, baseURL);
    await expect(page).toHaveURL(/\/login/, { timeout: 30_000 });
    expect(await page.evaluate(() => sessionStorage.getItem("calyx.draft.client.x"))).toBeNull();
    await expect(header(page, "owner")).toHaveCount(0);

    // The next person sees no data of the person before.
    await nextPersonSeesOwnData(page, shopper);
  });
});

test.describe("invitation", () => {
  test("/invite with nobody logged in: the token leaves the address, and the person sets a password", async ({
    page,
  }) => {
    const { invitee } = await seedAccess();
    let accepts = 0;
    page.on("request", (r) => {
      if (r.url().endsWith("/api/v1/invitations/accept")) accepts++;
    });
    await page.goto(invitee.invite_path ?? "");

    await expect(page.getByRole("heading", { name: "Welcome" })).toBeVisible();
    await expect(page.getByText("Acme invites you")).toBeVisible();
    // The browser does not translate the word "Calyx".
    await expect(page.locator('[translate="no"]', { hasText: /^Calyx$/ })).toBeVisible();
    await expect(page).toHaveURL(/\/invite$/);
    const email = page.getByLabel("Email");
    await expect(email).toHaveValue(invitee.email);
    await expect(email).toHaveAttribute("readonly", "");
    await expect(email).toHaveAttribute("autocomplete", "username");
    const password = page.getByLabel("Choose a password");
    await expect(password).toHaveAttribute("autocomplete", "new-password");
    await expect(page.getByText("At least 10 characters.")).toBeVisible();

    // The password rules: the page does not send a password that the backend refuses.
    await password.fill("short");
    await page.getByRole("button", { name: "Set password and continue" }).click();
    await expect(password).toHaveAttribute("aria-invalid", "true");
    expect(accepts).toBe(0);

    await password.fill("a-good-long-password");
    await page.getByRole("button", { name: "Set password and continue" }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(header(page, "client")).toBeVisible();
    expect(accepts).toBe(1);
  });

  test("/invite with a user logged in says who is logged in first", async ({ page }) => {
    const { owner, invitee } = await seedAccess();
    await page.goto("/login");
    await fillLogin(page, owner.email, owner.password);
    await expect(header(page, "owner")).toBeVisible();

    await page.goto(invitee.invite_path ?? "");
    await expect(page.getByText(`You are logged in as ${owner.email}`)).toBeVisible();
    await expect(page.locator('[translate="no"]', { hasText: owner.email })).toBeVisible();
    await expect(page.getByLabel("Choose a password")).toHaveCount(0);
    await page.getByRole("button", { name: "Continue with the invitation" }).click();

    await page.getByLabel("Choose a password").fill("a-good-long-password");
    await page.getByRole("button", { name: "Set password and continue" }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(header(page, "client")).toBeVisible();
  });

  test("another person in a second tab: the first tab loads again as that person", async ({
    page,
    context,
  }) => {
    const { owner, invitee } = await seedAccess();
    await page.goto("/login");
    await fillLogin(page, owner.email, owner.password);
    await expect(header(page, "owner")).toBeVisible();

    const second = await context.newPage();
    await second.goto(invitee.invite_path ?? "");
    await second.getByRole("button", { name: "Continue with the invitation" }).click();
    await second.getByLabel("Choose a password").fill("a-good-long-password");
    await second.getByRole("button", { name: "Set password and continue" }).click();
    await expect(header(second, "client")).toBeVisible();

    // A change makes the first tab read `me` again. It gets another user, and loads again.
    await control("POST", "/status", { instance: "Main", status: "CONNECTED" });
    await expect(header(page, "client")).toBeVisible({ timeout: 15_000 });
    await expect(header(page, "owner")).toHaveCount(0);
  });

  test("an invitation with 410 says why, with Log in", async ({ page, context }) => {
    const { invitee } = await seedAccess();
    await page.goto(invitee.invite_path ?? "");
    await page.getByLabel("Choose a password").fill("a-good-long-password");
    await page.getByRole("button", { name: "Set password and continue" }).click();
    await expect(header(page, "client")).toBeVisible();

    await context.clearCookies();
    await page.goto(invitee.invite_path ?? "");
    await expect(alert(page)).toContainText(
      "This link was used, replaced or cancelled, or it expired.",
    );
    await expect(alert(page)).toContainText(
      "Log in, or ask the person who invited you for a new one.",
    );
    await expect(page.getByLabel("Choose a password")).toHaveCount(0);
    await page.getByRole("link", { name: "Log in" }).click();
    await expect(page).toHaveURL(/\/login$/);
  });

  test("a reload with no token says to open the link again", async ({ page }) => {
    const { invitee } = await seedAccess();
    await page.goto(invitee.invite_path ?? "");
    await expect(page.getByRole("heading", { name: "Welcome" })).toBeVisible();
    await page.reload();
    await expect(page.getByText("Open the link of the mail again")).toBeVisible();
    await expect(page.getByLabel("Choose a password")).toHaveCount(0);
  });
});

test.describe("reset", () => {
  test("Forgot password sends a mail, and its link sets a new password", async ({ page }) => {
    const { owner } = await seedAccess();
    await page.goto("/login");
    await page.getByRole("link", { name: "Forgot your password?" }).click();
    await expect(page).toHaveURL(/\/forgot-password$/);
    await expect(page.getByRole("heading", { name: "Reset your password" })).toBeVisible();
    await page.getByLabel("Email").fill(owner.email);
    await page.getByRole("button", { name: "Send the link" }).click();
    await expect(page.getByText("Check your mail.")).toBeVisible();

    // The backend sends the mail in the background.
    let path = "";
    await expect
      .poll(async () => {
        const { body } = await control<{ to: string[]; paths: string[] }[]>("GET", "/mails");
        const mail = body.find((m) => m.to.includes(owner.email));
        path = mail?.paths.find((p) => p.startsWith("/reset-password?token=")) ?? "";
        return path;
      })
      .not.toBe("");

    await page.goto(path);
    await expect(page).toHaveURL(/\/reset-password$/);
    await expect(page.getByLabel("Email")).toHaveValue(owner.email);
    const password = page.getByLabel("New password");
    await expect(password).toHaveAttribute("autocomplete", "new-password");

    await password.fill("short");
    await page.getByRole("button", { name: "Set the password" }).click();
    await expect(password).toHaveAttribute("aria-invalid", "true");

    await password.fill("a-new-long-password");
    await page.getByRole("button", { name: "Set the password" }).click();
    await expect(page).toHaveURL(/\/login$/);
    await expect(page.getByText("Your password is changed.")).toBeVisible();
    await expect(page.getByLabel("Email")).toHaveValue(owner.email);

    // The email does not stay in the history, and a reload does not show the notice again.
    expect(await page.evaluate(() => JSON.stringify(history.state))).not.toContain(owner.email);
    await page.reload();
    await expect(page.getByRole("heading", { name: "Log in" })).toBeVisible();
    await expect(page.getByText("Your password is changed.")).toHaveCount(0);

    await fillLogin(page, owner.email, "a-new-long-password");
    await expect(header(page, "owner")).toBeVisible();
  });

  test("a reset with another person logged in keeps the notice", async ({ page }) => {
    const { owner, shopper } = await seedAccess();
    await page.goto("/login");
    await fillLogin(page, shopper.email, shopper.password);
    await expect(header(page, "client")).toBeVisible();

    await page.goto(owner.reset_path ?? "");
    await page.getByLabel("New password").fill("a-new-long-password");
    await page.getByRole("button", { name: "Set the password" }).click();
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByText("Your password is changed.")).toBeVisible();
    await expect(page.getByLabel("Email")).toHaveValue(owner.email);

    await page.getByLabel("Password", { exact: true }).fill("a-new-long-password");
    await page.getByRole("button", { name: "Log in" }).click();
    await expect(header(page, "owner")).toBeVisible();
  });

  test("a reset with 410 says why, with Forgot password", async ({ page }) => {
    const { owner } = await seedAccess();
    await page.goto(owner.reset_path ?? "");
    await page.getByLabel("New password").fill("a-new-long-password");
    await page.getByRole("button", { name: "Set the password" }).click();
    await expect(page).toHaveURL(/\/login$/);

    await page.goto(owner.reset_path ?? "");
    await expect(alert(page)).toContainText(
      "This link was used, replaced or cancelled, or it expired.",
    );
    await expect(alert(page)).toContainText('Select "Forgot password".');
    await expect(page.getByLabel("New password")).toHaveCount(0);
    await page.getByRole("link", { name: "Forgot password" }).click();
    await expect(page).toHaveURL(/\/forgot-password$/);
  });

  test("a reload with no token says to open the link again", async ({ page }) => {
    const { owner } = await seedAccess();
    await page.goto(owner.reset_path ?? "");
    await expect(page.getByLabel("New password")).toBeVisible();
    await expect(page).toHaveURL(/\/reset-password$/);
    await page.reload();
    await expect(page.getByText("Open the link of the mail again")).toBeVisible();
    await expect(page.getByLabel("New password")).toHaveCount(0);
  });

  test("with mail off, Forgot password says to ask the organization admin or the server admin", async ({
    page,
  }) => {
    await control("POST", "/backend", { action: "stop" });
    await control("POST", "/backend", { action: "start", mail: false });
    await page.goto("/forgot-password");
    await expect(page.getByText("This server sends no mail.")).toBeVisible();
    await expect(
      page.getByText(
        "Ask your organization admin for a reset link. If you are the organization admin, ask the server admin.",
      ),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Send the link" })).toHaveCount(0);
    await page.getByRole("link", { name: "Back to log in" }).click();
    await expect(page).toHaveURL(/\/login$/);
  });
});

test.describe("settings", () => {
  test("the password: a wrong current password, the rules, and the change", async ({ page }) => {
    const { owner } = await seedAccess();
    let changes = 0;
    page.on("request", (r) => {
      if (r.url().endsWith("/api/v1/me/password")) changes++;
    });
    await page.goto("/login");
    await fillLogin(page, owner.email, owner.password);
    await (await mainLink(page, "Account")).click();
    await expect(page).toHaveURL(/\/account$/);
    await expect(page.getByRole("heading", { name: "Account", level: 1 })).toBeVisible();
    await expect(await mainLink(page, "Account")).toHaveAttribute("aria-current", "page");
    // At the phone width, mainLink opened the Sheet: close it.
    await page.keyboard.press("Escape");
    await expect(page.locator('[translate="no"]', { hasText: owner.email })).toBeVisible();

    const current = page.getByLabel("Current password");
    const next = page.getByLabel("New password");
    await expect(current).toHaveAttribute("autocomplete", "current-password");
    await expect(next).toHaveAttribute("autocomplete", "new-password");

    // The rules of a new password: the page sends nothing.
    await current.fill(owner.password);
    await next.fill("short");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(next).toHaveAttribute("aria-invalid", "true");
    expect(changes).toBe(0);

    // A wrong current password marks its field.
    await current.fill("not-the-password");
    await next.fill("a-new-long-password");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(alert(page)).toContainText("Some values are not valid.");
    await expect(current).toHaveAttribute("aria-invalid", "true");
    await expect(next).not.toHaveAttribute("aria-invalid", "true");

    await current.fill(owner.password);
    await next.fill("a-new-long-password");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByText("Your password is changed.")).toBeVisible();
    await expect(alert(page)).toHaveCount(0);
    await expect(current).toHaveValue("");

    // This login session stays. The next login needs the new password.
    await page.getByRole("button", { name: "Log out" }).click();
    await expect(page).toHaveURL(/\/login$/);
    await fillLogin(page, owner.email, "a-new-long-password");
    await expect(header(page, "owner")).toBeVisible();
  });

  test("Log out ends the login session, and the next person sees no data of the person before", async ({
    page,
  }) => {
    const { owner, shopper } = await seedTwoPeople();
    await page.goto("/login");
    await fillLogin(page, owner.email, owner.password);
    await expect(page.getByText("Owner line")).toBeVisible();
    await page.evaluate(() =>
      sessionStorage.setItem("calyx.draft.client.x", JSON.stringify({ name: "Old" })),
    );

    await (await mainLink(page, "Account")).click();
    await page.getByRole("button", { name: "Log out" }).click();
    await expect(page).toHaveURL(/\/login$/);
    expect(await page.evaluate(() => sessionStorage.getItem("calyx.draft.client.x"))).toBeNull();
    const me = await page.request.get("/api/v1/me");
    expect(me.status()).toBe(401);

    await nextPersonSeesOwnData(page, shopper);
  });

  test("the name of the organization", async ({ page }) => {
    const { owner } = await seedAccess();
    await page.goto("/login");
    await fillLogin(page, owner.email, owner.password);
    await expect(header(page, "owner")).toBeVisible();
    await page.goto("/organization");
    await expect(page.getByRole("heading", { name: "Organization", level: 1 })).toBeVisible();
    const name = page.getByLabel("Name", { exact: true });
    await expect(name).toHaveValue("Acme");
    await expect(name).toHaveAttribute("translate", "no");
    await expect(page.getByText("Instances: 1 of 10.")).toBeVisible();

    await name.fill("Acme Ltd");
    const saved = page.waitForResponse((r) => r.url().endsWith("/api/v1/organization"));
    await page.getByRole("button", { name: "Save" }).click();
    expect((await saved).status()).toBe(200);
    await page.reload();
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue("Acme Ltd");
  });

  test("the terms and their banner", async ({ page }) => {
    const { owner } = await seedTwoPeople(false);
    let acknowledgements = 0;
    page.on("request", (r) => {
      if (r.url().endsWith("/whatsapp-web-acknowledgement")) acknowledgements++;
    });
    await page.goto("/login");
    await fillLogin(page, owner.email, owner.password);

    // Until the acceptance, the Instances screen shows only the notice, and the other screens of
    // the owner show the banner with the same text.
    for (const t of termsText) await expect(termsNotice(page)).toContainText(t);
    await expect(termsBanner(page)).toHaveCount(0);
    await page.goto("/organization");
    for (const t of termsText) await expect(termsBanner(page)).toContainText(t);
    await expect(
      termsBanner(page).getByRole("link", { name: "Read and accept the terms" }),
    ).toBeVisible();
    await expect(page.getByText("Not accepted yet")).toBeVisible();

    // Until the acceptance, the Organization section has a button to the terms, and no text link.
    const section = page.getByRole("region", { name: "Organization" });
    await expect(section.getByRole("link", { name: "Read the WhatsApp Web terms" })).toHaveCount(0);
    const read = section.getByRole("link", { name: "Read and accept the terms" });
    await expect(read).toHaveClass(/\bbg-primary\b/);

    // "Not now" changes nothing.
    await read.click();
    await expect(page).toHaveURL(/\/organization\/whatsapp-web$/);
    await page.getByRole("link", { name: "Not now" }).click();
    await expect(page).toHaveURL(/\/$/);
    expect(acknowledgements).toBe(0);

    await termsNotice(page).getByRole("link", { name: "Read and accept the terms" }).click();
    await expect(page).toHaveURL(/\/organization\/whatsapp-web$/);
    await expect(page.getByRole("heading", { name: "WhatsApp Web", level: 1 })).toBeVisible();
    await expect(
      page.getByText("Using it is against the Terms of Service of WhatsApp."),
    ).toBeVisible();
    await expect(page.getByText("It does not remove the risk.")).toBeVisible();
    await expect(termsBanner(page)).toHaveCount(0);

    const accept = page.getByRole("button", { name: "Accept" });
    await expect(accept).toBeDisabled();
    await page
      .getByLabel("I understand the risk, and I will tell my clients before they link a number.")
      .check();
    await accept.click();
    await expect(page).toHaveURL(/\/$/);
    await page.goto("/organization");
    await expect(page.getByText("Accepted", { exact: true })).toBeVisible();
    await expect(termsBanner(page)).toHaveCount(0);
    // After the acceptance, a text link next to the fact replaces the button.
    await expect(section.getByRole("link", { name: "Read and accept the terms" })).toHaveCount(0);
    const terms = section.getByRole("link", { name: "Read the WhatsApp Web terms" });
    await expect(terms).not.toHaveClass(/\bbg-primary\b/);
    await expect(section.locator("dd", { hasText: "Accepted" })).toContainText(
      "Read the WhatsApp Web terms",
    );
    await terms.click();
    await expect(page).toHaveURL(/\/organization\/whatsapp-web$/);
    expect(acknowledgements).toBe(1);

    await page.goto("/");
    await expect(header(page, "owner")).toBeVisible();
    await expect(page.getByText("Owner line")).toBeVisible();
    await expect(termsBanner(page)).toHaveCount(0);
    await expect(termsNotice(page)).toHaveCount(0);
    await page.goto("/organization/whatsapp-web");
    await expect(page.getByText("The terms are accepted.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Accept" })).toHaveCount(0);
  });

  test("a client user has its account, and no organization and no terms", async ({ page }) => {
    const { shopper } = await seedTwoPeople(false);
    await page.goto("/login");
    await fillLogin(page, shopper.email, shopper.password);
    await expect(page.getByRole("heading", { name: "Main" })).toBeVisible();
    await expect(termsBanner(page)).toHaveCount(0);

    await (await mainLink(page, "Account")).click();
    await expect(page.getByRole("heading", { name: "Account", level: 1 })).toBeVisible();
    await expect(page.getByText(shopper.email)).toBeVisible();
    await expect(page.getByLabel("Current password")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Organization" })).toHaveCount(0);
    await expect(page.getByRole("link", { name: "WhatsApp Web", exact: true })).toHaveCount(0);

    await page.goto("/organization/whatsapp-web");
    await expect(page.getByRole("heading", { name: "This page does not exist" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Accept" })).toHaveCount(0);
  });
});

test.describe("this page does not exist", () => {
  test("an unknown path says so, and links to the list", async ({ page }) => {
    const { owner } = await seedAccess();
    await page.goto("/login");
    await fillLogin(page, owner.email, owner.password);
    await expect(header(page, "owner")).toBeVisible();
    await page.goto("/no-such-page");
    await expect(page.getByRole("heading", { name: "This page does not exist" })).toBeVisible();
    await expect(header(page, "owner")).toBeVisible();
    await page.getByRole("link", { name: "Go to the list" }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole("heading", { name: "Instances", level: 1 })).toBeVisible();
  });
});
