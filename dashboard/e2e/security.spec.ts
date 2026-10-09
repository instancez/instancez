import { expect, test } from "@playwright/test";

// Security renders the engine's vet report: a banner, then a list and detail, or the all-clear state.
const SECRET_KEY = process.env.INSTANCEZ_SECRET_KEY || "inz_secret_e2e";

test.beforeEach(async ({ page }) => {
  await page.addInitScript((key) => {
    sessionStorage.setItem("instancez_secret_key", key);
  }, SECRET_KEY);
});

test("Security shows the banner, then findings or the all-clear state", async ({ page }) => {
  await page.goto("/dashboard/security");
  await expect(page.getByText(/of \d+ checks passed|All clear/).first()).toBeVisible();

  await expect(page.getByRole("button", { name: /Re-scan/ })).toHaveCount(0);

  const rows = page.locator("button").filter({ has: page.locator("p") });
  const n = await rows.count();
  if (n === 0) {
    await expect(page.getByText("Nothing to fix")).toBeVisible();
    return;
  }
  await expect(rows.first()).toHaveAttribute("aria-current", "true");
  await expect(page.getByText("HOW TO FIX")).toBeVisible();

  if (n > 1) {
    const title = (await rows.nth(1).locator("p").first().textContent()) ?? "";
    await rows.nth(1).click();
    await expect(rows.nth(1)).toHaveAttribute("aria-current", "true");
    await expect(rows.first()).not.toHaveAttribute("aria-current", "true");
    await expect(page.getByText(title, { exact: true })).toHaveCount(2);
  }
});
