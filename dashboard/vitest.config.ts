import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    css: false,
    // userEvent types one character at a time on a real timer. With many
    // editor-bearing suites running in parallel the CPU saturates and a
    // correct-but-slow keystroke test can blow past the default 5s ceiling, so
    // give it more headroom rather than let it flake.
    testTimeout: 20000,
    // Vitest owns the jsdom component tests under src/. The Playwright smoke
    // suite lives in e2e/ and runs via `npm run test:e2e` against a real
    // booted binary. Keep the two from colliding by scoping vitest to src/.
    include: ["src/**/*.test.{ts,tsx}"],
    exclude: ["e2e/**", "node_modules/**"],
  },
});
