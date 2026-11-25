import type { Locator } from "@playwright/test";
import { expect, test } from "../support.ts";

test.beforeEach(async ({ page }) => {
  await page.goto("/test/ui/csp/index.html");
});

// The popup must be visible, and it or a parent must be positioned. Blocked styles leave it in the flow.
async function expectStyled(popup: Locator) {
  await expect(popup).toBeVisible();
  const positioned = await popup.evaluate((el) => {
    for (let n: Element | null = el; n; n = n.parentElement) {
      if (/fixed|absolute/.test(getComputedStyle(n).position)) return true;
    }
    return false;
  });
  expect(positioned).toBe(true);
}

test("the plain components render", async ({ page }) => {
  await expect(page.getByRole("button", { name: "Plain button" })).toBeVisible();
  await expect(page.getByRole("checkbox", { name: "Accept" })).toBeVisible();
  await expect(page.getByRole("switch", { name: "Enabled" })).toBeVisible();
  await expect(page.getByText("Alert text")).toBeVisible();
  await expect(page.getByRole("img", { name: "Calyx" })).toBeVisible();
});

test("the dialog opens", async ({ page }) => {
  await page.getByRole("button", { name: "Open dialog" }).click();
  await expectStyled(page.getByRole("dialog"));
});

test("the sheet opens", async ({ page }) => {
  await page.getByRole("button", { name: "Open sheet" }).click();
  await expectStyled(page.getByRole("dialog"));
});

test("the hover card opens", async ({ page }) => {
  await page.getByRole("button", { name: "Hover card" }).hover();
  await expectStyled(page.getByText("Hover card text"));
});

test("the popover opens", async ({ page }) => {
  await page.getByRole("button", { name: "Open popover" }).click();
  await expectStyled(page.getByText("Popover text"));
});

test("the select opens", async ({ page }) => {
  await page.getByRole("combobox", { name: "Choice" }).click();
  await expectStyled(page.getByRole("listbox"));
  await page.getByRole("option", { name: "Option B" }).click();
  await expect(page.getByRole("combobox", { name: "Choice" })).toContainText("Option B");
});

test("the tooltip opens", async ({ page }) => {
  await page.getByRole("button", { name: "Tooltip" }).hover();
  await expectStyled(page.locator("[data-slot=tooltip-content]"));
});

test("the collapsible opens", async ({ page }) => {
  await page.getByRole("button", { name: "Open collapsible" }).click();
  await expect(page.getByText("Collapsible text")).toBeVisible();
});
