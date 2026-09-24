import { expect, test, type Page } from "@playwright/test";

// Layout is invisible to jsdom, so only a real browser can catch the editor growing with its content.
const SECRET_KEY = process.env.INSTANCEZ_SECRET_KEY || "inz_secret_e2e";

test.beforeEach(async ({ page }) => {
  await page.addInitScript((key) => {
    sessionStorage.setItem("instancez_secret_key", key);
  }, SECRET_KEY);
});

async function expectLongLineWraps(page: Page) {
  const editor = page.locator(".cm-editor").first();
  await expect(editor).toBeVisible();
  const before = (await editor.boundingBox())!.width;
  expect(before).toBeGreaterThan(300);

  await editor.locator(".cm-content").click();
  await page.keyboard.insertText(" -- " + "x".repeat(400) + " " + "word ".repeat(80));

  await expect(editor.getByText("x".repeat(400), { exact: false })).toBeVisible();
  expect((await editor.boundingBox())!.width).toBeLessThanOrEqual(before + 1);
  const pageWidth = await page.evaluate(() => document.documentElement.scrollWidth);
  expect(pageWidth).toBeLessThanOrEqual(page.viewportSize()!.width);
}

test("a long function body line wraps instead of widening the editor", async ({ page }) => {
  await page.goto("/dashboard/rpc/ping");
  await expectLongLineWraps(page);
});

test("a long RLS policy line wraps instead of widening the editor", async ({ page }) => {
  await page.goto("/dashboard/tables/notes");
  await page.getByRole("tab", { name: "RLS" }).click();
  await expectLongLineWraps(page);
});
