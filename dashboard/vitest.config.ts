import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    // Vitest owns the jsdom component tests under src/. The Playwright smoke
    // suite lives in e2e/ and runs via `npm run test:e2e` against a real
    // booted binary. Keep the two from colliding by scoping vitest to src/.
    include: ["src/**/*.test.{ts,tsx}"],
    exclude: ["e2e/**", "node_modules/**"],
  },
});
