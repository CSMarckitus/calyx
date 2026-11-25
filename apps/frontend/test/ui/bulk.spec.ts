import type { Locator, Page, Request, Route } from "@playwright/test";
import { control, expect, login, type Person, setBulkJob, test } from "./support.ts";

// The bulk jobs view of an instance and the row of its card. A 503 and a
// lost answer are failed requests in the browser log.
test.use({ tolerateFailedRequests: true });

type Job = {
  status: string;
  total: number;
  sent: number;
  failed: number;
  progress_percent: number;
  estimated_finish_in_seconds?: number;
  created_ago_seconds: number;
  created_by: string;
};
type Seeded = {
  users: (Person & { id: string })[];
  instances: { name: string; id: string; path: string; bulk_job_ids: string[] }[];
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
const main = (bulk_jobs: Job[] = [], extra: Record<string, unknown> = {}) => ({
  name: "Main",
  client: "Shop",
  provider: "WHATSAPP_WEB",
  status: "CONNECTED",
  bulk_jobs,
  ...extra,
});

// The job of the first test: 500 messages, 120 sent, 3 failed, and an estimated end in one hour.
const running: Job = {
  status: "RUNNING",
  total: 500,
  sent: 120,
  failed: 3,
  progress_percent: 24.6,
  estimated_finish_in_seconds: 3600,
  created_ago_seconds: 600,
  created_by: "owner",
};

const person = (s: Seeded, label: string) => s.users.find((u) => u.label === label) as Person;
const instanceOf = (s: Seeded, name: string) =>
  s.instances.find((i) => i.name === name) as Seeded["instances"][number];
const idOf = (s: Seeded, name: string) => instanceOf(s, name).id;

const unreadable = "Calyx cannot read the bulk jobs now.";
const empty = "No bulk jobs. Programs start them through the API or MCP.";
const kept = "The 50 newest jobs that Calyx keeps.";
const stopNote =
  "To stop a program, revoke its key first. An organization admin can also remove the user of a client.";
const dialogText =
  "Messages that Calyx already sent stay sent. A message that goes out now can still arrive.";

// The drawer, and not the card of services, which is a dialog too.
const drawer = (page: Page) => page.locator('[role="dialog"][data-slot="sheet-content"]');
const card = (page: Page) => drawer(page).locator('[data-slot="instance"]');
// The view, in the drawer or in place of the card of a client user with one instance.
const view = (page: Page) => page.locator('[data-slot="bulk-jobs"]');
const jobs = (page: Page) => view(page).locator('[data-slot="bulk-job"]');
const readAt = (page: Page) => view(page).getByText(/^Read at \d\d:\d\d:\d\d$/);
const checkAgain = (page: Page) => view(page).getByRole("button", { name: "Check again" });
const stopDialog = (page: Page) => page.getByRole("dialog", { name: "Stop sending?" });
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

const isList = (id: string) => (r: Request) =>
  r.method() === "GET" && new URL(r.url()).pathname === `/api/v1/instances/${id}/bulk-jobs`;
const isCancel = (jobId: string) => (r: Request) =>
  r.method() === "POST" && new URL(r.url()).pathname === `/api/v1/bulk-jobs/${jobId}/cancel`;

// The requests of one kind that the page sent.
function watch(page: Page, match: (r: Request) => boolean): Request[] {
  const sent: Request[] = [];
  page.on("request", (r) => {
    if (match(r)) sent.push(r);
  });
  return sent;
}

// The link "View" of the row "Bulk jobs". The check first fails fast when the row is absent.
async function viewLink(where: Locator): Promise<void> {
  const link = fact(where, "Bulk jobs").getByRole("link", { name: "View" });
  await expect(link).toBeVisible();
  await link.click();
}

async function openView(page: Page, who: Person, id: string): Promise<void> {
  await login(page, who);
  // Waits for the first screen, so the next `goto` does not cut the login.
  await expect(page.locator("main").locator('[data-slot="instance"]').first()).toBeVisible();
  await page.goto(`/instances/${id}/bulk-jobs`);
  await expect(view(page).getByRole("heading", { name: "Bulk jobs" })).toBeVisible();
  await expect(readAt(page)).toBeVisible();
}

test("the row of the card: Bulk jobs with View, in every state, and no read of the jobs", async ({
  page,
}) => {
  const s = await seed([main([running]), main([], { name: "Lost", state: "missing" })]);
  const id = idOf(s, "Main");
  const reads = watch(page, isList(id));
  await login(page, person(s, "owner"));

  await expect(cell(page, "Main")).toBeVisible();
  await page.goto(`/instances/${idOf(s, "Lost")}`);
  await expect(fact(card(page), "Bulk jobs").getByRole("link", { name: "View" })).toBeVisible();
  await page.goto(`/instances/${idOf(s, "Lost")}/bulk-jobs`);
  await expect(view(page).getByText(empty)).toBeVisible();

  await page.goto(`/instances/${id}`);
  await expect(card(page)).toBeVisible();
  // The row shows no count, so the card reads no jobs.
  await expect(fact(card(page), "Bulk jobs")).toHaveText("View");
  const before = reads.length;
  await page.waitForTimeout(1000);
  expect(reads.length).toBe(before);
  await page.goto("/");
  await cell(page, "Main").getByRole("link").click();
  await expect(card(page)).toBeVisible();
  await page.waitForTimeout(1000);
  expect(reads.length).toBe(before);

  await viewLink(card(page));
  await expect(page).toHaveURL(`/instances/${id}/bulk-jobs`);
  await expect(page.getByRole("dialog", { name: "Bulk jobs of Main" })).toBeVisible();
  await expect(drawer(page).getByRole("heading", { name: "Bulk jobs", level: 2 })).toBeFocused();
});

test("a program starts a bulk job, and the person stops it", async ({ page }) => {
  const s = await seed([main([running])]);
  const id = idOf(s, "Main");
  const jobId = instanceOf(s, "Main").bulk_job_ids[0] as string;
  const cancels = watch(page, isCancel(jobId));
  await openView(page, person(s, "owner"), id);

  const job = jobs(page);
  await expect(job).toHaveCount(1);
  await expect(job).toContainText("Sending");
  await expect(job).toContainText("500 messages");
  await expect(job).toContainText(/Created Today, \d\d:\d\d/);
  await expect(job).toContainText("120 sent, 3 not sent (failed or unknown), 377 waiting");
  await expect(job).toContainText(/About \d\d:\d\d/);
  await expect(job.locator("progress")).toHaveAttribute("value", "24.6");
  await expect(job.locator("progress")).toHaveAttribute("max", "100");
  await expect(job).toContainText("123 of 500 tried");

  // "Keep sending" closes the dialog and sends nothing.
  await job.getByRole("button", { name: "Stop sending" }).click();
  await expect(stopDialog(page)).toContainText(dialogText);
  await stopDialog(page).getByRole("button", { name: "Keep sending" }).click();
  await expect(stopDialog(page)).toHaveCount(0);
  expect(cancels).toHaveLength(0);

  await job.getByRole("button", { name: "Stop sending" }).click();
  const answer = page.waitForResponse((r) => isCancel(jobId)(r.request()));
  await stopDialog(page).getByRole("button", { name: "Stop sending" }).click();
  expect((await answer).status()).toBe(200);
  await expect(job).toContainText("Stopped");
  await expect(job).toContainText(
    "120 sent, 3 not sent (failed or unknown), 377 not sent (stopped)",
  );
  await expect(job.getByRole("button", { name: "Stop sending" })).toHaveCount(0);
  expect(cancels).toHaveLength(1);
});

test("the core does not answer, and the view keeps the last counters", async ({ page }) => {
  const s = await seed([main([running])]);
  await openView(page, person(s, "owner"), idOf(s, "Main"));
  const counts = "120 sent, 3 not sent (failed or unknown), 377 waiting";
  await expect(jobs(page)).toContainText(counts);
  const last = await readAt(page).textContent();

  await control("POST", "/core", { action: "stop" });
  // One second later, a new read would show a new time.
  await page.waitForTimeout(1100);
  await checkAgain(page).click();
  await expect(view(page).getByText(unreadable)).toBeVisible({ timeout: 20_000 });
  await expect(jobs(page)).toContainText(counts);
  await expect(readAt(page)).toHaveText(last as string);
  await expect(view(page).getByText(empty)).toHaveCount(0);
});

test("Check again shows the new counters and the new time of the read", async ({ page }) => {
  const s = await seed([main([running])]);
  const id = idOf(s, "Main");
  const jobId = instanceOf(s, "Main").bulk_job_ids[0] as string;
  await openView(page, person(s, "owner"), id);
  const last = await readAt(page).textContent();

  await setBulkJob(jobId, {
    status: "RUNNING",
    sent: 300,
    failed: 5,
    progress_percent: 61,
    estimated_finish_in_seconds: 1200,
  });
  await page.waitForTimeout(1100);
  await checkAgain(page).click();
  await expect(jobs(page)).toContainText("300 sent, 5 not sent (failed or unknown), 195 waiting");
  await expect(jobs(page)).toContainText("305 of 500 tried");
  await expect(readAt(page)).not.toHaveText(last as string);
});

test("each open of the view reads the jobs, and no timer reads them", async ({ page }) => {
  const s = await seed([main([running])]);
  const id = idOf(s, "Main");
  const reads = watch(page, isList(id));
  // The health checks that are on the network now.
  let checks = 0;
  const isCheck = (r: Request) => new URL(r.url()).pathname === "/healthz";
  page.on("request", (r) => void (isCheck(r) && checks++));
  page.on("requestfinished", (r) => void (isCheck(r) && checks--));
  page.on("requestfailed", (r) => void (isCheck(r) && checks--));
  // A fake clock in the page: the test moves it 30 s in steps, and does not wait 30 s. The clock
  // runs normally until the first step.
  await page.clock.install();
  await openView(page, person(s, "owner"), id);
  // The stream sends one `changed` at its start, and the page reads its screen again (Shape: a
  // side effect, not a timer). The count starts after it.
  await page.waitForTimeout(2000);
  const settled = reads.length;
  expect(settled).toBeGreaterThanOrEqual(1);

  // No timer: 30 s later, the page has read nothing more. The clock moves in steps of 1 s.
  // After each step, the test waits until no health check is on the network. One jump of 30 s
  // would fire the 5 s limit of a health check that is still on the network: the page would then
  // see a lost backend and read again. The 100 ms let a request that a timer started begin.
  for (let i = 0; i < 30; i++) {
    await page.clock.runFor(1000);
    await page.waitForTimeout(100);
    await expect.poll(() => checks).toBe(0);
  }
  expect(reads).toHaveLength(settled);

  await view(page).getByRole("link", { name: "Back" }).click();
  await expect(card(page)).toBeVisible();
  await viewLink(card(page));
  await expect(readAt(page)).toBeVisible();
  await expect.poll(() => reads.length).toBe(settled + 1);
});

test("each status has its words, and only a waiting or a running job has Stop sending", async ({
  page,
}) => {
  const job = (status: string, ago: number, extra: Partial<Job> = {}): Job => ({
    status,
    total: 10,
    sent: 4,
    failed: 1,
    progress_percent: 50,
    created_ago_seconds: ago,
    created_by: "owner",
    ...extra,
  });
  const s = await seed([
    main([
      job("QUEUED", 10, { sent: 0, failed: 0, progress_percent: 0 }),
      job("COMPLETED", 20, { sent: 9, failed: 1, progress_percent: 100 }),
      job("CANCELLED", 30),
      job("FAILED", 40),
    ]),
  ]);
  await openView(page, person(s, "owner"), idOf(s, "Main"));
  // The newest job comes first.
  await expect(jobs(page)).toHaveCount(4);
  const [queued, completed, cancelled, failed] = [0, 1, 2, 3].map((i) => jobs(page).nth(i));
  await expect(queued as Locator).toContainText("Waiting to start");
  await expect(queued as Locator).toContainText("0 of 10 tried");
  await expect((queued as Locator).getByRole("button", { name: "Stop sending" })).toBeVisible();
  await expect(completed as Locator).toContainText("Finished");
  await expect(completed as Locator).toContainText("9 sent, 1 not sent (failed or unknown)");
  await expect(cancelled as Locator).toContainText("Stopped");
  await expect(cancelled as Locator).toContainText(
    "4 sent, 1 not sent (failed or unknown), 5 not sent (stopped)",
  );
  await expect(failed as Locator).toContainText("Stopped by an error");
  await expect(failed as Locator).toContainText(
    "4 sent, 1 not sent (failed or unknown), 5 not sent (stopped)",
  );
  for (const done of [completed, cancelled, failed])
    await expect((done as Locator).getByRole("button", { name: "Stop sending" })).toHaveCount(0);
});

test("the empty list and the two notes", async ({ page }) => {
  const s = await seed([main()]);
  await openView(page, person(s, "shopper"), idOf(s, "Main"));
  await expect(view(page).getByText(empty)).toBeVisible();
  await expect(jobs(page)).toHaveCount(0);
  await expect(view(page).getByText(kept)).toBeVisible();
  await expect(view(page)).toContainText(stopNote);
  await view(page).getByRole("link", { name: "revoke its key" }).click();
  await expect(page).toHaveURL("/developers/api-keys");
});

test("Stop sending on a job that finished in the meantime shows its final status", async ({
  page,
}) => {
  const s = await seed([main([running])]);
  const jobId = instanceOf(s, "Main").bulk_job_ids[0] as string;
  await openView(page, person(s, "owner"), idOf(s, "Main"));
  await expect(jobs(page)).toContainText("Sending");

  await setBulkJob(jobId, { status: "COMPLETED", sent: 497, failed: 3, progress_percent: 100 });
  await jobs(page).getByRole("button", { name: "Stop sending" }).click();
  const answer = page.waitForResponse((r) => isCancel(jobId)(r.request()));
  await stopDialog(page).getByRole("button", { name: "Stop sending" }).click();
  expect((await answer).status()).toBe(200);
  await expect(jobs(page)).toContainText("Finished");
  await expect(jobs(page)).toContainText("497 sent, 3 not sent (failed or unknown)");
  await expect(jobs(page).getByRole("button", { name: "Stop sending" })).toHaveCount(0);
});

test("a 503 of Stop sending shows its text, and the page does not send it again", async ({
  page,
}) => {
  const s = await seed([main([running])]);
  const jobId = instanceOf(s, "Main").bulk_job_ids[0] as string;
  const cancels = watch(page, isCancel(jobId));
  await openView(page, person(s, "owner"), idOf(s, "Main"));
  await control("POST", "/fault", { method: "CancelBulkJob", code: "UNAVAILABLE" });

  await jobs(page).getByRole("button", { name: "Stop sending" }).click();
  await stopDialog(page).getByRole("button", { name: "Stop sending" }).click();
  await expect(view(page).getByText("Calyx could not stop the job now.")).toBeVisible();
  await expect(
    view(page).getByText("Select Check again. If the job still sends, select Stop sending again."),
  ).toBeVisible();
  await expect(jobs(page)).toContainText("Sending");
  await page.waitForTimeout(1000);
  expect(cancels).toHaveLength(1);
});

test("a 404 of Stop sending shows its text, and the page does not send it again", async ({
  page,
}) => {
  const s = await seed([main([running])]);
  const jobId = instanceOf(s, "Main").bulk_job_ids[0] as string;
  const cancels = watch(page, isCancel(jobId));
  await openView(page, person(s, "owner"), idOf(s, "Main"));
  await control("POST", "/fault", { method: "CancelBulkJob", code: "NOT_FOUND" });

  await jobs(page).getByRole("button", { name: "Stop sending" }).click();
  await stopDialog(page).getByRole("button", { name: "Stop sending" }).click();
  await expect(view(page).getByText("This bulk job does not exist now.")).toBeVisible();
  await expect(view(page).getByText("Select Check again.", { exact: true })).toBeVisible();
  await page.waitForTimeout(1000);
  expect(cancels).toHaveLength(1);
});

test("a lost answer of Stop sending reads the jobs again, and the page does not send it again", async ({
  page,
}) => {
  const s = await seed([main([running])]);
  const id = idOf(s, "Main");
  const jobId = instanceOf(s, "Main").bulk_job_ids[0] as string;
  const cancels = watch(page, isCancel(jobId));
  await openView(page, person(s, "owner"), id);

  // The backend stops the job, and the browser loses the answer.
  await page.route(`**/api/v1/bulk-jobs/${jobId}/cancel`, async (route: Route) => {
    await route.fetch();
    await route.abort("connectionreset");
  });
  const read = page.waitForRequest(isList(id));
  await jobs(page).getByRole("button", { name: "Stop sending" }).click();
  await stopDialog(page).getByRole("button", { name: "Stop sending" }).click();
  await read;
  await expect(jobs(page)).toContainText("Stopped");
  await page.waitForTimeout(1000);
  expect(cancels).toHaveLength(1);
});

// A reload, the Back of the browser and the "Back" of the view, on the path of the view.
// `before` is the path that "View" left: the browser goes back there.
async function walkPaths(page: Page, id: string, before: string): Promise<void> {
  await page.reload();
  await expect(view(page).getByRole("heading", { name: "Bulk jobs" })).toBeVisible();
  await expect(readAt(page)).toBeVisible();
  await page.goBack();
  await expect(page).toHaveURL(before);
  await expect(view(page)).toHaveCount(0);
  await page.goForward();
  await expect(page).toHaveURL(`/instances/${id}/bulk-jobs`);
  await expect(view(page).getByRole("heading", { name: "Bulk jobs" })).toBeVisible();
  await view(page).getByRole("link", { name: "Back" }).click();
  await expect(page).toHaveURL(`/instances/${id}`);
  await expect(view(page)).toHaveCount(0);
}

test("a reload and Back on the path of the view, for the owner and for a client user", async ({
  page,
}) => {
  const s = await seed([main([running]), main([], { name: "Second" })]);
  const id = idOf(s, "Main");
  for (const who of ["owner", "shopper"]) {
    await page.context().clearCookies();
    await login(page, person(s, who));
    await cell(page, "Main").getByRole("link").click();
    await viewLink(card(page));
    await expect(page.getByRole("dialog", { name: "Bulk jobs of Main" })).toBeVisible();
    await walkPaths(page, id, `/instances/${id}`);
    await expect(card(page)).toBeVisible();
  }
});

test("a client user with one instance sees the view in place of its card", async ({ page }) => {
  const s = await seed([main([running])]);
  const id = idOf(s, "Main");
  await login(page, person(s, "shopper"));
  const own = page.getByRole("main").locator('[data-slot="instance"]');
  await expect(
    page.getByRole("main").getByRole("heading", { name: "Main", level: 1 }),
  ).toBeVisible();
  await viewLink(own);
  await expect(page).toHaveURL(`/instances/${id}/bulk-jobs`);
  await expect(
    page.getByRole("main").getByRole("heading", { name: "Bulk jobs", level: 1 }),
  ).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(jobs(page)).toContainText("Sending");
  // The card of a client user with one instance is at `/`, so "View" leaves `/`.
  await walkPaths(page, id, "/");
  await expect(
    page.getByRole("main").getByRole("heading", { name: "Main", level: 1 }),
  ).toBeVisible();
});
