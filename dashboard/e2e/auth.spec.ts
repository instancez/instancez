import { expect, test } from "@playwright/test";

// The admin-key gate is the front door of the shipped dashboard: a wrong key
// must be rejected, the right one must open the app. This is the skeleton spec
// that proves the whole harness (binary boot + embedded SPA + /status auth)
// hangs together before the richer edit/save/drift flows lean on it.
const ADMIN_KEY = process.env.INSTANCEZ_ADMIN_KEY || "e2e-admin-key";

test("admin-key gate rejects a wrong key and accepts the right one", async ({ page }) => {
  await page.goto("/dashboard");

  // Login screen is shown when no key is stored.
  await expect(page.getByText("Welcome back")).toBeVisible();

  // Wrong key → stays on the login screen with an error.
  await page.locator("#admin-key").fill("definitely-wrong");
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByText(/Invalid admin key/i)).toBeVisible();

  // Right key → lands on the Overview (its cards render once authenticated).
  await page.locator("#admin-key").fill(ADMIN_KEY);
  await page.getByRole("button", { name: "Continue" }).click();

  await expect(page.getByText("Welcome back")).toBeHidden();
  await expect(page.getByText("Tables", { exact: true })).toBeVisible();
});
