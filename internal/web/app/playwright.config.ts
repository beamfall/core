import { join } from "node:path";
import { defineConfig, devices } from "@playwright/test";

// E2E against the embedded React app served by the real Go server binary at
// /web/ (the canonical post-cutover mount). The global-setup harness
// (e2e/global-setup.ts) either boots its
// own `beamfall serve -seed development` (with a known admin password, so the
// Console admin lane is exercised) on E2E_PORT, or — when BEAMFALL_E2E_URL is
// set — reuses that already-running server. baseURL must point at whichever one
// global-setup used.
const E2E_PORT = Number(process.env.BEAMFALL_E2E_PORT ?? 8830);
const baseURL = process.env.BEAMFALL_E2E_URL ?? `http://127.0.0.1:${E2E_PORT}`;
const suite = process.env.BEAMFALL_WEB_E2E_SUITE ?? "full";
const globalTimeout = suite === "smoke" ? 10 * 60_000 : 30 * 60_000;

// Keyed by port so two concurrent runs never overwrite each other's
// storageStates or harness manifest. global-setup derives the same path.
const authDir = join(import.meta.dirname, "e2e", ".auth", String(E2E_PORT));
const renderPhase = process.env.BEAMFALL_WEB_E2E_RENDER_PHASE === "1";
const visualBaselineDir = join(import.meta.dirname, "e2e", "goldens");
// Linux goldens are rendered against a pinned font set (see the conf file);
// other platforms keep their native system fonts.
const linuxFontconfig =
  process.platform === "linux"
    ? {
        env: {
          ...process.env,
          FONTCONFIG_FILE: join(import.meta.dirname, "e2e", "linux-fonts.conf"),
        },
      }
    : {};
const archivedVisualBaselineDir = join(
  import.meta.dirname,
  "..",
  "..",
  "..",
  "artifacts",
  "techdur-web-2",
  "visual-baselines",
);

export default defineConfig({
  testDir: "./e2e",
  snapshotPathTemplate: join(
    archivedVisualBaselineDir,
    "{projectName}",
    "{testFilePath}",
    "{arg}{ext}",
  ),
  updateSnapshots: "none",
  // Includes fresh web/server builds plus serial browser projects. Ten minutes
  // bounds each smoke-workflow invocation; the complete suite gets thirty. This is
  // deliberately separate from (and does not raise) per-test timeouts.
  globalTimeout,
  fullyParallel: false,
  workers: renderPhase ? 1 : undefined,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: [["list"]],
  globalSetup: "./e2e/global-setup.ts",
  globalTeardown: "./e2e/global-teardown.ts",
  expect: {
    toHaveScreenshot: {
      // Live goldens are per platform: font rasterisation differs between hosts.
      pathTemplate: join(
        visualBaselineDir,
        "{platform}",
        "{projectName}",
        "{testFilePath}",
        "{arg}{ext}",
      ),
    },
  },
  use: {
    baseURL,
    locale: "en-US",
    timezoneId: "UTC",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    launchOptions: linuxFontconfig,
  },
  projects: [
    // Unauthenticated surfaces (smoke + the enroll flow). No storageState: a
    // fresh, unenrolled browser.
    {
      name: "unauth",
      testMatch: /(smoke|auth)\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    // VIEWER session: an enrolled browser bound to the adult Default profile.
    {
      name: "viewer",
      testMatch: /viewer\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: join(authDir, "viewer.json"),
      },
    },
    {
      name: "player-desktop",
      testMatch: /player\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: join(authDir, "viewer.json"),
      },
    },
    {
      name: "player-mobile",
      testMatch: /player\.spec\.ts/,
      use: {
        ...devices["Pixel 7"],
        storageState: join(authDir, "viewer.json"),
      },
    },
    // MANAGER session: an enrolled browser promoted to the manager role.
    {
      name: "manage",
      testMatch: /manage\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: join(authDir, "manager.json"),
      },
    },
    // CONSOLE: the manager browser with an Admin Session opened server-side.
    {
      name: "console",
      testMatch: /console\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: join(authDir, "console.json"),
      },
    },
    {
      name: "text-expansion",
      testMatch: /text-expansion\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: join(authDir, "console.json"),
      },
    },
    {
      name: "a11y-browser",
      testMatch: /a11y-browser\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: join(authDir, "console.json"),
        // axe-core is test instrumentation injected by Playwright. The app's
        // strict CSP correctly rejects addScriptTag unless this test context
        // explicitly bypasses CSP for the accessibility scan.
        bypassCSP: true,
      },
    },
    // AUTH MATRIX: role/profile combinations that need multiple storage states
    // in one spec, so the test creates its own contexts.
    {
      name: "auth-matrix",
      testMatch: /auth-matrix\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "web-compat-firefox",
      testMatch: /browser-compat\.spec\.ts/,
      use: {
        ...devices["Desktop Firefox"],
        storageState: join(authDir, "viewer.json"),
      },
    },
    {
      name: "web-compat-webkit",
      testMatch: /browser-compat\.spec\.ts/,
      use: {
        ...devices["Desktop Safari"],
        storageState: join(authDir, "viewer.json"),
      },
    },
  ],
});
