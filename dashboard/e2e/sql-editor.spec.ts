import { expect, test } from "@playwright/test";

// The Postgres parser is wasm loaded at runtime, so only the shipped binary in a real browser proves it loads.
const SECRET_KEY = process.env.INSTANCEZ_SECRET_KEY || "inz_secret_e2e";

test.beforeEach(async ({ page }) => {
  await page.addInitScript((key) => {
    sessionStorage.setItem("instancez_secret_key", key);
  }, SECRET_KEY);
});

test("underlines a syntax error, then runs only the statement under the cursor", async ({ page }) => {
  await page.goto("/dashboard/sql");
  const content = page.locator(".cm-content").first();
  await content.click();
  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.press("Delete");

  await page.keyboard.insertText("select from where;");
  await expect(page.locator(".cm-lintRange-error").first()).toBeVisible({ timeout: 15_000 });

  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.insertText("select 1 as first_col;\nselect 2 as second_col;");
  await expect(page.locator(".cm-lintRange-error")).toHaveCount(0, { timeout: 15_000 });

  await page.getByRole("button", { name: "Run" }).click();
  await expect(page.getByRole("button", { name: "Sort by second_col" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sort by first_col" })).toHaveCount(0);
});
