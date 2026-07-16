import { defineConfig, devices } from "@playwright/test";

// Full-stack smoke suite. Unlike the vitest component tests (jsdom, mocked
// backend), these drive a browser against the real inz binary serving the
// embedded SPA over HTTP against Postgres, the artifact that actually ships.
// One server, so run serially.
const PORT = process.env.PORT || "8080";
const baseURL = process.env.E2E_BASE_URL || `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL,
    trace: "on-first-retry",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
  ],
  webServer: {
    command: "node e2e/boot.mjs",
    url: `${baseURL}/dashboard`,
    timeout: 120_000,
    reuseExistingServer: !process.env.CI,
    stdout: "pipe",
    stderr: "pipe",
  },
});
