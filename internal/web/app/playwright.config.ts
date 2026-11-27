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
// The smoke suite runs in about 9.7 minutes on an idle Mac, so a 10 minute
// budget timed out under gate load and reported the unrun tests as uncovered
// UI states (CORE-2788). Twenty minutes leaves room for a loaded host.
const globalTimeout = suite === "smoke" ? 20 * 60_000 : 30 * 60_000;

// Keyed by port so two concurrent runs never overwrite each other's
// storageStates or harness manifest. global-setup derives the same path.
const authDir = join(import.meta.dirname, "e2e", ".auth", String(E2E_PORT));
const renderPhase = process.env.BEAMFALL_WEB_E2E_RENDER_PHASE === "1";
const updateWebGoldens = process.env.BEAMFALL_UPDATE_WEB_GOLDENS === "1";
const visualBaselineDir = join(import.meta.dirname, "e2e", "goldens");
const archivedVisualBaselineDir = join(
  import.meta.dirname,
  "..",
  "..",
  "..",
  "artifacts",
  "techdur-web-2",
  "visual-baselines",
);

const config = defineConfig({
  testDir: "./e2e",
  snapshotPathTemplate: join(
    archivedVisualBaselineDir,
    "{projectName}",
    "{testFilePath}",
    "{arg}{ext}",
  ),
  // Live goldens (e2e/goldens/) are only updated through the supported
  // BEAMFALL_UPDATE_WEB_GOLDENS=1 workflow (e.g. `make web-e2e-rerecord-goldens`),
  // never via ad-hoc --update-snapshots against the archived parity snapshots.
  updateSnapshots: updateWebGoldens ? "all" : "none",
  // Includes fresh web/server builds plus serial browser projects. Ten minutes
  // bounds each smoke-workflow invocation; the complete suite gets thirty. This is
  // deliberately separate from (and does not raise) per-test timeouts.
  globalTimeout,
  fullyParallel: false,
  workers: renderPhase || updateWebGoldens ? 1 : undefined,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: [["list"], ["./e2e/lib/progress-heartbeat-reporter.mjs"]],
  globalSetup: "./e2e/global-setup.ts",
  globalTeardown: "./e2e/global-teardown.ts",
  expect: {
    toHaveScreenshot: {
      pathTemplate: join(visualBaselineDir, "{projectName}", "{testFilePath}", "{arg}{ext}"),
    },
  },
  use: {
    baseURL,
    locale: "en-US",
    timezoneId: "UTC",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    // Unauthenticated surfaces (smoke + the enroll flow). No storageState: a
    // fresh, unenrolled browser.
    {
      name: "unauth",
      testMatch: /(smoke|auth|brand-splash)\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    // VIEWER session: an enrolled browser bound to the adult Default profile.
    {
      name: "viewer",
      testMatch: /(viewer|poster-size|profile-precondition)\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: join(authDir, "viewer.json"),
      },
    },
    {
      name: "player-desktop",
      testMatch: /player(-tracks)?\.spec\.ts/,
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
    // UI STATE INVENTORY (CORE-1305): rows of e2e/lib/ui-states.mjs no other spec covers, at
    // desktop width and on a phone. The console storage state reaches every area.
    {
      name: "ui-states",
      testMatch: /ui-states\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: join(authDir, "console.json"),
      },
    },
    {
      name: "ui-states-mobile",
      testMatch: /ui-states\.spec\.ts/,
      use: {
        ...devices["Pixel 7"],
        storageState: join(authDir, "console.json"),
      },
    },
    // AUTH MATRIX: role/profile combinations that need multiple storage states
    // in one spec, so the test creates its own contexts.
    {
      name: "auth-matrix",
      testMatch: /auth-matrix\.spec\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    // MUSIC (CORE-1640): a real Music-plugin Library of generated tracks played in the
    // browser. Last among the Chromium projects so the Library it adds (and deletes
    // again) never shows on another project's shelves.
    {
      name: "music",
      testMatch: /music\.spec\.ts/,
      use: {
        ...devices["Desktop Chrome"],
        storageState: join(authDir, "viewer.json"),
      },
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

// Tests that play media never sound through the host's speakers: every Chromium project
// launches with --mute-audio. It silences only the output device, so decoding, Web Audio
// analysers and the player's own mute state behave as they do with sound. Firefox and
// WebKit take no such flag and are left alone.
config.projects = config.projects?.map((project) =>
  project.use?.defaultBrowserType === "chromium"
    ? {
        ...project,
        use: {
          ...project.use,
          launchOptions: {
            ...project.use.launchOptions,
            args: [...(project.use.launchOptions?.args ?? []), "--mute-audio"],
          },
        },
      }
    : project,
);

export default config;
