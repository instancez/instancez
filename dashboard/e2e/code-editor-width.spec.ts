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
  const width = () => editor.evaluate((el) => el.getBoundingClientRect().width);
  const before = await width();
  expect(before).toBeGreaterThan(300);

  await editor.locator(".cm-content").click();
  await page.keyboard.insertText(" -- " + "x".repeat(400) + " " + "word ".repeat(80));

  await expect(editor.getByText("x".repeat(400), { exact: false })).toBeVisible();
  expect(await width()).toBeLessThanOrEqual(before + 1);
  const [scrollWidth, viewportWidth] = await page.evaluate(() => [
    document.documentElement.scrollWidth,
    window.innerWidth,
  ]);
  expect(scrollWidth).toBeLessThanOrEqual(viewportWidth);
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
