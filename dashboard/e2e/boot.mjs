// Boots the real `inz` binary for the Playwright smoke suite: production
// `serve` against an external Postgres, serving the embedded dashboard SPA.
// Playwright's webServer runs this and waits for /dashboard to answer.
//
// Inputs (all optional, with dev-friendly defaults):
//   INZ_BIN                       path to the inz binary (default: <repo>/inz)
//   INSTANCEZ_TEST_DATABASE_URL   superuser DSN; serve self-provisions roles
//   INSTANCEZ_ADMIN_KEY           admin key the spec logs in with
//   PORT                          listen port (default 8080)
//
// A missing binary or unreachable DB surfaces as a non-zero exit with an
// actionable message, which Playwright reports as a webServer failure. Run
// `npm run test:e2e` only with the binary built and Postgres reachable.
import { spawn } from "node:child_process";
import { copyFileSync, existsSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..", "..");

const bin = process.env.INZ_BIN || join(repoRoot, "inz");
const fixture = resolve(here, "fixtures", "instancez.yaml");
const port = process.env.PORT || "8080";
const dsn =
  process.env.INSTANCEZ_TEST_DATABASE_URL ||
  "postgres://instancez:instancez@localhost:5432/instancez?sslmode=disable";
const adminKey = process.env.INSTANCEZ_ADMIN_KEY || "e2e-admin-key";

if (!existsSync(bin)) {
  console.error(
    `[e2e] inz binary not found at ${bin}\n` +
      `      build it first: (cd ${repoRoot} && go build -o inz ./cmd/inz)\n` +
      `      or point INZ_BIN at an existing binary.`,
  );
  process.exit(1);
}

// Run from a throwaway cwd so local-provider storage and any stray files land
// outside the repo, and no ambient .production.env is picked up. Serve a copy
// of the fixture, not the committed file: the dashboard save flow rewrites its
// config source, and the smoke suite must never dirty a tracked fixture.
const workdir = mkdtempSync(join(tmpdir(), "inz-e2e-"));
const configPath = join(workdir, "instancez.yaml");
copyFileSync(fixture, configPath);

const child = spawn(
  bin,
  // serve defaults --dashboard to disabled; readwrite mounts the SPA and the
  // config-mutation endpoints the edit/save flow exercises.
  ["serve", "--config", configPath, "--migrate", "--port", port, "--dashboard", "readwrite"],
  {
    cwd: workdir,
    stdio: "inherit",
    env: {
      ...process.env,
      INSTANCEZ_DATABASE_URL: dsn,
      INSTANCEZ_ADMIN_KEY: adminKey,
    },
  },
);

for (const sig of ["SIGTERM", "SIGINT"]) {
  process.on(sig, () => child.kill(sig));
}
child.on("exit", (code, signal) => {
  if (signal) process.kill(process.pid, signal);
  else process.exit(code ?? 0);
});
