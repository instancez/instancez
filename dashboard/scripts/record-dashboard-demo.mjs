// Records docs/site/src/assets/dashboard.gif: adding a table field and a
// storage bucket in the `inz dev` dashboard. Every save flows through the
// dashboard's own "Review changes before saving" dialog, which renders a real
// unified diff of instancez.yaml — that's what proves on camera that the
// dashboard write-path syncs straight into the YAML, no fabricated overlay
// needed.
//
// This does not boot instancez itself — point it at an already-running
// `inz dev` (or `inz serve --dashboard readwrite`) instance:
//
//   SECRET_KEY=inz_secret_xxx \
//   BASE_URL=http://localhost:8080 \
//   node scripts/record-dashboard-demo.mjs
//
// Requires: `npm ci` in dashboard/ (for @playwright/test) and Playwright's
// chromium browser installed (`npx playwright install chromium`).
import { chromium } from "@playwright/test";
import { renameSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const BASE_URL = process.env.BASE_URL || "http://localhost:8080";
const SECRET_KEY = process.env.SECRET_KEY;
const OUT = process.env.OUT || join(process.cwd(), "dashboard-demo.webm");

if (!SECRET_KEY) {
  console.error("SECRET_KEY env var is required (INSTANCEZ_SECRET_KEY from .development.env)");
  process.exit(1);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Fills the CONFIRM phrase and applies the pending review dialog, then waits
// for it (and the save bar behind it) to close.
async function confirmSave(page) {
  await page.getByText("Review changes before saving").waitFor();
  await sleep(1800); // hold on the diff so it's readable in the gif
  await page.locator("#confirm-phrase").fill("CONFIRM");
  await sleep(200);
  await page.getByRole("button", { name: "Confirm & Save" }).click();
  await page.getByText("Review changes before saving").waitFor({ state: "hidden" });
  await page.getByRole("button", { name: "Save Changes" }).waitFor({ state: "hidden" }).catch(() => {});
  await sleep(500);
}

async function main() {
  const videoDir = mkdtempSync(join(tmpdir(), "inz-dash-demo-"));
  const browser = await chromium.launch();
  const context = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    recordVideo: { dir: videoDir, size: { width: 1280, height: 800 } },
  });
  const page = await context.newPage();

  // --- Login ---
  await page.goto(`${BASE_URL}/dashboard`);
  await page.getByText("Welcome back").waitFor();
  await sleep(500);
  await page.locator("#secret-key").fill(SECRET_KEY);
  await page.getByRole("button", { name: "Continue" }).click();
  await page.getByText("Welcome back").waitFor({ state: "hidden" });
  await sleep(900);

  // --- Tables: add a field to `todos` ---
  await page.getByRole("link", { name: "Tables", exact: true }).click();
  await page.getByText("todos", { exact: true }).waitFor();
  await sleep(500);
  await page.getByText("todos", { exact: true }).click();
  await page.getByRole("button", { name: "Add Field" }).waitFor();
  await sleep(700);
  await page.getByRole("button", { name: "Add Field" }).click();

  const newFieldBtn = page.getByRole("button", { name: /^new_field_/ });
  await newFieldBtn.waitFor();
  await sleep(300);
  await newFieldBtn.click();
  await sleep(200);
  await page.keyboard.press("Control+A");
  await page.keyboard.type("priority");
  await page.keyboard.press("Enter");
  await sleep(300);

  const priorityRow = page.locator("tr", { hasText: "priority" });
  await priorityRow.locator("select").first().selectOption("integer");
  await sleep(250);
  await priorityRow.locator('input[placeholder="—"]').fill("1");
  await sleep(250);
  await priorityRow.getByRole("switch", { name: "required" }).click({ force: true });
  await sleep(600);

  await page.getByRole("button", { name: "Save Changes" }).click();
  await confirmSave(page);

  // --- Storage: add a bucket, then flip it public ---
  await page.goto(`${BASE_URL}/dashboard/storage`);
  await page.getByText("avatars", { exact: true }).waitFor();
  await sleep(600);
  await page.getByRole("button", { name: "Add Bucket" }).click();
  await page.locator("#dialog-input").fill("exports");
  await sleep(300);
  await page.getByRole("button", { name: "Create" }).click();
  await confirmSave(page);
  await page.getByText("Bucket Settings").waitFor();
  await sleep(600);

  await page.getByText("Public bucket").waitFor();
  await sleep(600);
  await page.getByRole("switch").first().click({ force: true });
  await sleep(500);
  await page.getByRole("button", { name: "Save Changes" }).click();
  await confirmSave(page);

  await sleep(600);

  const video = page.video();
  await context.close();
  const videoPath = await video.path();
  await browser.close();

  renameSync(videoPath, OUT);
  console.log(`wrote ${OUT}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
