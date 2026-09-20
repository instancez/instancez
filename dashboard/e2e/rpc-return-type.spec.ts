import { expect, test } from "@playwright/test";

// Typing a custom RPC return type (setof/table(...)) happens directly inside
// the CodeMirror-rendered function signature now, not a plain text field.
// That's a real contenteditable + custom change-filter/caret-fence, which
// jsdom can't drive (verified: neither `fireEvent.input` nor a dispatched
// `beforeinput` mutates a CodeMirror 6 doc under jsdom) — only a real browser
// proves the golden path actually works.
const SECRET_KEY = process.env.INSTANCEZ_SECRET_KEY || "inz_secret_e2e";

test.beforeEach(async ({ page }) => {
  await page.addInitScript((key) => {
    sessionStorage.setItem("instancez_secret_key", key);
  }, SECRET_KEY);
});

test("switching Return Type to Custom opens an editable RETURNS line that accepts real typing", async ({
  page,
}) => {
  await page.goto("/dashboard/rpc/ping");

  const returnType = page.getByRole("combobox", { name: "Return Type" });
  await expect(returnType).toHaveValue("text");

  await returnType.selectOption("__custom__");
  await expect(page.getByText("RETURNS table(id int)")).toBeVisible();

  // Focus the editor and jump to document start; the caret fence clamps that
  // into the nearest editable region, which is the hole (it comes before the
  // body in document order).
  await page.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+Home");
  await page.keyboard.type("u");

  await expect(page.getByText("RETURNS utable(id int)")).toBeVisible();
  await expect(page.getByRole("button", { name: /save changes/i })).toBeVisible();
});

test("typing through a preset's exact name mid-edit does not exit Custom mode or drop keystrokes", async ({
  page,
}) => {
  // "time" is itself a preset, and a prefix of "timestamptz" — typing the
  // latter passes through the former. Custom mode must stay put so the rest
  // of the word keeps landing in the hole instead of wherever the caret ends
  // up once the RETURNS line suddenly locks.
  await page.goto("/dashboard/rpc/ping");

  const returnType = page.getByRole("combobox", { name: "Return Type" });
  await returnType.selectOption("__custom__");
  await expect(page.getByText("RETURNS table(id int)")).toBeVisible();

  await page.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+a"); // clamped to the hole alone
  await page.keyboard.type("timestamptz");

  await expect(page.getByText("RETURNS timestamptz")).toBeVisible();
  await expect(returnType).toHaveValue("__custom__");
  await expect(page.getByRole("button", { name: /save changes/i })).toBeVisible();
});
