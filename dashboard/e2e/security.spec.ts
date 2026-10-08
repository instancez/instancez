import { expect, test } from "@playwright/test";

// Security renders the engine's vet report: either findings or the all-clear state.
const SECRET_KEY = process.env.INSTANCEZ_SECRET_KEY || "inz_secret_e2e";

test.beforeEach(async ({ page }) => {
  await page.addInitScript((key) => {
    sessionStorage.setItem("instancez_secret_key", key);
  }, SECRET_KEY);
});

test("Security renders findings or the all-clear state", async ({ page }) => {
  await page.goto("/dashboard/security");
  const clear = page.getByText("No security findings", { exact: true });
  const findings = page.getByRole("button", { name: /^(critical|high|medium|low|info) \d+$/ }).first();
  await expect(clear.or(findings)).toBeVisible();
});
