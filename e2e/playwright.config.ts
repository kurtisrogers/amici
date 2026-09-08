import { defineConfig, devices } from "@playwright/test";

/**
 * Playwright configuration for Amici.
 *
 * The suite builds and starts a real server against a throwaway SQLite file,
 * with fixtures enabled. It talks to that server the way a person would: no
 * seeded cookies, no injected state, no calling into Go from JavaScript. The
 * only shortcut it takes is POST /fixtures/reset, which rebuilds the same cast
 * of people the local development seed uses, so a spec can start from a world
 * it can reason about instead of clicking through eight sign-up forms to
 * arrange one friendship.
 *
 * AMICI_ENABLE_FIXTURES is what exposes that endpoint, and config.Load refuses
 * to accept it when AMICI_ENV is production.
 */

const port = Number(process.env.AMICI_E2E_PORT ?? 8081);
const baseURL = `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: "./tests",
  // A specification is a statement about the product, so a flake is a bug in
  // the specification. Nothing is retried locally; CI gets one retry to
  // absorb a slow runner rather than to paper over a race.
  retries: process.env.CI ? 1 : 0,
  forbidOnly: !!process.env.CI,
  // The fixture reset endpoint is global state: two workers resetting the
  // same database would fight. One worker keeps every spec deterministic, and
  // the whole suite still runs in well under a minute.
  workers: 1,
  fullyParallel: false,
  timeout: 30_000,
  expect: { timeout: 5_000 },
  reporter: process.env.CI
    ? [["list"], ["html", { open: "never" }], ["github"]]
    : [["list"], ["html", { open: "never" }]],
  use: {
    baseURL,
    // Artefacts only for failures, so a green run leaves nothing behind.
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
    // Amici sends no-referrer and expects same-origin form posts, which is
    // exactly what a normal browser does. Nothing is relaxed here.
    ignoreHTTPSErrors: false,
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    // A phone-sized viewport, because a family social network is mostly read
    // on a phone and the layout has to hold up.
    { name: "mobile", use: { ...devices["Pixel 7"] } },
  ],
  webServer: {
    // Built rather than `go run`, so a compile error fails fast and loudly
    // instead of looking like a slow start.
    command: "./scripts/start-server.sh",
    url: `${baseURL}/healthz`,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    stdout: "pipe",
    stderr: "pipe",
    env: {
      AMICI_E2E_PORT: String(port),
    },
  },
});
