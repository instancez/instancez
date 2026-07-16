import { expect, test } from "@playwright/test";

// Read path: an authenticated dashboard renders the entities declared in the
// served instancez.yaml. This proves the SPA → admin API → Postgres round-trip
// for each capability-gated section, against the real seeded fixture (one
// table, one rpc, one bucket).
const SECRET_KEY = process.env.INSTANCEZ_SECRET_KEY || "inz_secret_e2e";

// Seed the secret key before any page script runs so App's sessionStorage gate
// opens straight to the app, skipping the login form (covered by auth.spec).
test.beforeEach(async ({ page }) => {
  await page.addInitScript((key) => {
    sessionStorage.setItem("instancez_secret_key", key);
  }, SECRET_KEY);
});

test("Tables lists the seeded table", async ({ page }) => {
  await page.goto("/dashboard/tables");
  await expect(page.getByText("notes", { exact: true })).toBeVisible();
});

test("Database Functions lists the seeded rpc", async ({ page }) => {
  await page.goto("/dashboard/rpc");
  await expect(page.getByText("ping", { exact: true })).toBeVisible();
});

test("Storage lists the seeded bucket", async ({ page }) => {
  await page.goto("/dashboard/storage");
  await expect(page.getByText("avatars", { exact: true })).toBeVisible();
});
