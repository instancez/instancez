import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { writeFileSync } from "node:fs";
import { resolve } from "node:path";

const apiTarget = process.env.API_URL || "http://localhost:8080";

// emptyOutDir: true wipes dashboard/dist/.gitkeep on every build. The
// Go binary embeds dist/ via //go:embed, which requires at least one
// file present at compile time; .gitkeep is the sentinel for fresh
// checkouts where `npm run build` hasn't been run. Restore it after
// every build so the chain is self-healing regardless of how vite is
// invoked (make build, raw npm run build, IDE plugins, etc.).
const preserveGitkeep = {
  name: "ultrabase:preserve-gitkeep",
  closeBundle() {
    writeFileSync(resolve(__dirname, "dist", ".gitkeep"), "");
  },
};

export default defineConfig({
  plugins: [react(), tailwindcss(), preserveGitkeep],
  base: "/dashboard",
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
