// UI State Catalog capture harness for the embedded Viewer/Manage web UI
// (surface `web`, ADR-0007). Conforms to ../../../ui-catalog/SPEC.md.
//
// Pipeline:
//   1. boot `beamfall serve --seed=conformance` on an ephemeral port,
//   2. for each React-capturable ENTRY: fresh browser context with the entry's seeded
//      session cookies injected, navigate to /web/, run the entry's `drive`
//      step to reach the target state, screenshot to images/<id>.png and dump
//      compact visible hierarchy + hidden summaries + a11y to images/<id>.txt,
//   3. write manifest.json (captured + listed + device-only entries),
//   4. diff each captured PNG against baselines/<id>.png → diffs/<id>.png on
//      mismatch; --record re-blesses baselines (the only way they change).
//
// Determinism: the conformance seed pins the clock (Unix 0) and Stream-Token
// key; we disable animations/transitions/caret and force prefers-reduced-motion,
// fix the viewport, and never assert on timestamps. AGPL firewall: this tool
// only drives the public UI and reads the public API — no domain vocabulary.

import { chromium } from "@playwright/test";
import pixelmatch from "pixelmatch";
import { PNG } from "pngjs";
import { spawn } from "node:child_process";
import { mkdir, writeFile, readFile, rm, readdir } from "node:fs/promises";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join, resolve } from "node:path";
import net from "node:net";
import { ENTRIES, SESSIONS, VIEWPORTS } from "./entries.mjs";

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, "..", "..");
const CATALOG = resolve(REPO_ROOT, "ui-catalog");
const IMAGES = join(CATALOG, "images");
const BASELINES = join(CATALOG, "baselines");
const DIFFS = join(CATALOG, "diffs");
const DIFF_THRESHOLD = 0.001; // mismatched fraction above which a diff is flagged
// The conformance serve bootstraps this admin password (ADR-0012 §8) so the
// `console` driver can open the Admin Session and capture the Console screens.
// A publicly known capture-only value — never a production secret.
const CONSOLE_ADMIN_PASSWORD = "ui-catalog-admin";

const args = process.argv.slice(2);
const RECORD = args.includes("--record");
const DIFF_ONLY = args.includes("--diff-only");
if (RECORD && DIFF_ONLY) {
  throw new Error("--record cannot be combined with --diff-only");
}
const CSRF = "1"; // app.js posts a fixed CSRF header value (auth.go cookie defense)
const LEGACY_DRIVER_NOTE =
  "React catalog driver pending: this entry's legacy static-app selector driver is disabled so stale screenshots do not claim React coverage.";

// ── helpers ────────────────────────────────────────────────────────────────

async function freePort() {
  return new Promise((res, rej) => {
    const srv = net.createServer();
    srv.unref();
    srv.on("error", rej);
    srv.listen(0, "127.0.0.1", () => {
      const port = srv.address().port;
      srv.close(() => res(port));
    });
  });
}

async function buildServer() {
  const bin = join(__dirname, ".bin", "beamfall");
  await mkdir(dirname(bin), { recursive: true });
  await new Promise((res, rej) => {
    const p = spawn("go", ["build", "-o", bin, "./cmd/beamfall"], {
      cwd: REPO_ROOT,
      stdio: "inherit",
    });
    p.on("exit", (code) => (code === 0 ? res() : rej(new Error(`go build exited ${code}`))));
    p.on("error", rej);
  });
  return bin;
}

async function bootServer(bin, port) {
  const dataDir = join(__dirname, ".data");
  await rm(dataDir, { recursive: true, force: true });
  await mkdir(dataDir, { recursive: true });
  const proc = spawn(
    bin,
    [
      "serve",
      "--seed=conformance",
      "--allow-conformance-seed",
      `--addr=127.0.0.1:${port}`,
    ],
    {
      cwd: REPO_ROOT,
      env: {
        ...process.env,
        BEAMFALL_DATA_DIR: dataDir,
        BEAMFALL_ADMIN_PASSWORD: CONSOLE_ADMIN_PASSWORD,
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  let log = "";
  proc.stdout.on("data", (d) => (log += d));
  proc.stderr.on("data", (d) => (log += d));
  const base = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(`${base}/web/`);
      if (r.ok) return { proc, base };
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  proc.kill("SIGKILL");
  throw new Error(`server did not become ready in 15s. log:\n${log}`);
}

function cookiesFor(sessionName, port) {
  const creds = SESSIONS[sessionName];
  if (!creds) return [];
  // The server reads only the cookie value; we inject with secure:false so the
  // headless context sends them over plain http to 127.0.0.1.
  const base = { domain: "127.0.0.1", path: "/", httpOnly: true, secure: false, sameSite: "Strict" };
  return [
    { name: "beamfall_session", value: creds.session, ...base },
    { name: "beamfall_device", value: creds.device, ...base },
    { name: "beamfall_device_secret", value: creds.secret, ...base },
  ];
}

// Injected before app.js runs: kill animation/transition/caret so renders are
// byte-stable, force reduced motion, and freeze any time-based UI jitter.
const FREEZE_CSS = `
*, *::before, *::after {
  animation-duration: 0s !important;
  animation-delay: 0s !important;
  transition-duration: 0s !important;
  transition-delay: 0s !important;
  caret-color: transparent !important;
  scroll-behavior: auto !important;
}
/* Focus rings are a transient, run-dependent state on the web surface (the app
 * sets initial focus on load); suppress the :focus-visible outline/keyline so
 * renders are byte-stable. Focus-as-a-captured-state belongs to the tvOS /
 * AndroidTV surfaces (SPEC focus-state note), not web. */
*:focus, *:focus-visible {
  outline: none !important;
  box-shadow: none !important;
}`;

// CSS-aware leak/role-signal assertion. The DOM-text leak checks see `hidden=""`
// and treat an element as gone, but CSS can override the UA `[hidden] {
// display:none }` rule with an explicit `display` and keep the element painted
// (the .manage-crumb / .status-badge bug). That is invisible to text greps yet a
// real render leak: a locked session would show a false "Revealed" chip, a plain
// viewer a "Manage / MANAGER" crumb. Assert the contract directly: every element
// carrying the `hidden` attribute must compute to `display: none`. Returns the
// list of offenders (id/class/display) so the caller can fail the run.
async function hiddenButDisplayedOffenders(page) {
  return page.evaluate(() =>
    [...document.querySelectorAll("[hidden]")]
      .filter((el) => getComputedStyle(el).display !== "none")
      .map((el) => ({
        id: el.id || null,
        cls: el.className || null,
        tag: el.tagName.toLowerCase(),
        display: getComputedStyle(el).display,
      })),
  );
}

async function dumpHierarchy(page, id) {
  // Compact LLM artifact: visible app hierarchy plus hidden text summaries.
  // Full hidden DOM is too noisy for review, but hidden text stays greppable for
  // leak-proof entries.
  const { header, main, hidden } = await page.evaluate(() => {
    const compactHTML = (selector) => {
      const root = document.querySelector(selector);
      if (!root) return "";
      const clone = root.cloneNode(true);
      clone.querySelectorAll("[hidden]").forEach((el) => el.remove());
      return clone.outerHTML.replace(/\s+/g, " ").trim();
    };
    const label = (el) => {
      const id = el.id ? `#${el.id}` : "";
      const cls = typeof el.className === "string" && el.className.trim()
        ? `.${el.className.trim().replace(/\s+/g, ".")}`
        : "";
      return `${el.tagName.toLowerCase()}${id}${cls}`;
    };
    const summarize = (text) => text.replace(/\s+/g, " ").trim().slice(0, 200);
    return {
      header: compactHTML("header"),
      main: compactHTML("main") || compactHTML("body"),
      hidden: [...document.querySelectorAll("[hidden]")]
        .map((el) => {
          const text = summarize(el.textContent || "");
          const aria = summarize(el.getAttribute("aria-label") || "");
          const attrs = [aria && `aria=${JSON.stringify(aria)}`, text && `text=${JSON.stringify(text)}`]
            .filter(Boolean)
            .join(" ");
          return attrs ? `${label(el)} ${attrs}` : label(el);
        })
        .filter(Boolean),
    };
  });
  let ax;
  try {
    ax = await page.accessibility.snapshot({ interestingOnly: true });
  } catch {
    ax = null;
  }
  const text =
    `# ${id}\n\n## visible header\n\n${header}\n\n## visible main\n\n${main}\n\n` +
    `## hidden element summaries\n\n${hidden.join("\n")}\n\n` +
    `## accessibility snapshot\n\n${JSON.stringify(ax)}\n`;
  await writeFile(join(IMAGES, `${id}.txt`), text);
}

// ── drivers: each reaches a target state via real UI interaction ────────────
// Helper: wait for a view section to become visible (its `hidden` removed).
async function waitVisible(page, selector, timeout = 8000) {
  await page.waitForSelector(`${selector}:not([hidden])`, { timeout });
}

async function settle(page) {
  // Let pending hydration fetches resolve, then a short idle for paint.
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(150);
  // Drop the transient focus target so a focused control's keyline does not
  // drift between runs (FREEZE_CSS already suppresses the :focus-visible ring).
  await page.evaluate(() => document.activeElement && document.activeElement.blur && document.activeElement.blur());
  await page.waitForTimeout(30);
}

// The Manage view's schedule/job rows render server-computed nextDue/lastEnqueued
// timestamps against the REAL wall clock (the conformance seed pins only the
// read-path clock, not the scheduler), so they drift every run and would break
// byte-stable regression. Replace the volatile date/time text with a fixed
// placeholder before screenshot + DOM dump — the row structure and intent stay
// intact; the exact timestamp is not load-bearing for the catalog. Surgical: we
// only touch rendered text, never app.js.
async function neutralizeVolatileText(page) {
  await page.evaluate(() => {
    const DATE_RE = /\b\d{1,2}\/\d{1,2}\/\d{2,4},?\s+\d{1,2}:\d{2}(:\d{2})?\s*(AM|PM)?\b/gi;
    const walk = (node) => {
      for (const child of node.childNodes) {
        if (child.nodeType === Node.TEXT_NODE) {
          if (DATE_RE.test(child.nodeValue)) {
            child.nodeValue = child.nodeValue.replace(DATE_RE, "<deterministic-time>");
          }
        } else if (child.nodeType === Node.ELEMENT_NODE) {
          walk(child);
        }
      }
    };
    document.querySelectorAll(".manage-row-detail, .api-token-created, .pairing-result").forEach(walk);
  });
}

const DRIVERS = {
  async enroll(page) {
    await waitVisible(page, "#enroll-view");
    await settle(page);
  },
  async enrollInvalid(page) {
    await waitVisible(page, "#enroll-view");
    await page.fill("#pairing-code", "ZZZZ-ZZZZ");
    await page.click("#enroll-submit");
    await waitVisible(page, "#enroll-error");
    await settle(page);
  },
  async browse(page) {
    await waitVisible(page, "#browse-view");
    await settle(page);
  },
  async browseConnectionLost(page) {
    await waitVisible(page, "#browse-view");
    await settle(page);
    // Sever the API after the first good render, then provoke a refetch by
    // re-selecting the active nav entry (re-hydrates Sections → fetch fails →
    // the non-destructive connection-lost banner appears over last-good content).
    await page.route("**/api/graphql", (r) => r.abort());
    await page.route("**/api/manifest", (r) => r.abort());
    const navBtn = page.locator("#nav button").first();
    if (await navBtn.count()) await navBtn.click();
    await page.waitForSelector("#connection-banner:not([hidden])", { timeout: 8000 }).catch(() => {});
    await settle(page);
  },
  async profileSwitcher(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#profile-open");
    await waitVisible(page, "#profile-view");
    await settle(page);
  },
  async revealPinEntry(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#restrictions-open");
    await waitVisible(page, "#restrictions-view");
    await settle(page);
  },
  async revealPinDenied(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#restrictions-open");
    await waitVisible(page, "#restrictions-view");
    await page.fill("#reveal-pin", "0000");
    await page.click("#restrictions-submit");
    await waitVisible(page, "#restrictions-error");
    await settle(page);
  },
  async promote(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#promote-open");
    await waitVisible(page, "#promote-view");
    await settle(page);
  },
  async manage(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#manage-open");
    await waitVisible(page, "#manage-view");
    await settle(page);
    await neutralizeVolatileText(page);
  },
  // Console (ADR-0007 §4): open Manage, then unlock the Admin Session with the
  // bootstrap admin password (BEAMFALL_ADMIN_PASSWORD, set on the conformance
  // serve in bootServer). The admin-gated Console screens (API keys, Activity)
  // then become visible for capture.
  async console(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#manage-open");
    await waitVisible(page, "#manage-view");
    await page.waitForSelector("#console-section:not([hidden])", { timeout: 4000 });
    await page.fill("#admin-password", CONSOLE_ADMIN_PASSWORD);
    await page.click("#admin-unlock-submit");
    await page.waitForSelector("#console-screens:not([hidden])", { timeout: 4000 });
    await settle(page);
    await neutralizeVolatileText(page);
  },
  // Player chrome: open the player by clicking the first playable media card in
  // the revealed browse view, then reveal the transport chrome.
  async player(page) {
    await openPlayer(page);
    await showTransport(page);
    await settle(page);
  },
  async playerAudioMenu(page) {
    await openPlayer(page);
    await showTransport(page);
    await page.click("#player-audio-menu-button");
    await page.waitForSelector("#player-audio-menu:not([hidden])", { timeout: 4000 }).catch(() => {});
    await settle(page);
  },
  async playerSubtitleMenu(page) {
    await openPlayer(page);
    await showTransport(page);
    await page.click("#player-subtitle-menu-button");
    await page.waitForSelector("#player-subtitle-menu:not([hidden])", { timeout: 4000 }).catch(() => {});
    await settle(page);
  },
  async playerQualityMenu(page) {
    await openPlayer(page);
    await showTransport(page);
    await page.click("#player-quality-menu-button");
    await page.waitForSelector("#player-quality-menu:not([hidden])", { timeout: 4000 }).catch(() => {});
    await settle(page);
  },
  async playerInfoOverlay(page) {
    await openPlayer(page);
    await showTransport(page);
    await page.click("#player-info-button");
    await page.waitForSelector("#player-info-overlay:not([hidden])", { timeout: 4000 }).catch(() => {});
    await settle(page);
  },
  // Reveal-safe terminal state via the app's REAL code path: route the
  // playback-info request to a 410 (the reveal-terminal status the server
  // returns when content the session may not currently play is requested — a
  // relock/token-expiry/hidden outcome), then click the real media card. The
  // app's own `presentPlayerTerminal("hidden")` then renders its generic
  // reveal-safe message + Back, placing NO restricted metadata in the DOM.
  // We use the visible media-alpha card so a genuine card handler fires; the
  // 410 is what the locked session would receive for restricted bytes, so the
  // rendered terminal chrome is exactly the real one.
  async playerTerminalHidden(page) {
    await waitVisible(page, "#browse-view");
    await settle(page);
    let observedStatus = 410;
    await page.route("**/api/playback-info", async (route) => {
      await route.fulfill({
        status: 410,
        contentType: "application/json",
        body: JSON.stringify({ error: "gone" }),
      });
    });
    const card = page.locator(".item-card").first();
    await card.waitFor({ state: "visible", timeout: 8000 });
    await card.click();
    await waitVisible(page, "#player-view");
    await page.waitForSelector("#player-message:not([hidden])", { timeout: 6000 });
    await settle(page);
    return { terminalStatus: observedStatus };
  },
  async mydata(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#mydata-open");
    await waitVisible(page, "#mydata-view");
    await settle(page);
    await neutralizeVolatileText(page);
  },
  // My Data hard-DELETE confirm: navigate to mydata, intercept the GraphQL mutation
  // to return success (no real deletion), accept the native confirm() dialog, then
  // wait for the post-delete result state.
  async mydataDeleteConfirm(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#mydata-open");
    await waitVisible(page, "#mydata-view");
    page.on("dialog", (d) => d.accept());
    await page.route("**/api/graphql", async (route) => {
      const body = route.request().postData() || "";
      if (body.includes("DeleteMyEngagement")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ data: { deleteMyEngagement: { scope: "all" } } }),
        });
      } else {
        await route.continue();
      }
    });
    await page.click("#mydata-delete-all");
    await page.waitForSelector("#mydata-delete-result:not([hidden])", { timeout: 6000 });
    await settle(page);
  },
  // Migration Wizard empty state: unlock the Console, navigate to the Setup category,
  // and wait for the empty-importer message (no importers registered).
  async migrationWizardEmpty(page) {
    // The conformance seed's built-in filmtv plugin always registers a plex importer, so we
    // override the Importers query to return [] — the same page.route pattern used by
    // pairingMintViewer — to deterministically capture the empty-state UI element.
    await page.route("**/api/graphql", async (route, request) => {
      const body = request.postData() || "";
      if (body.includes("Importers")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ data: { importers: [] } }),
        });
      } else {
        await route.continue();
      }
    });
    await waitVisible(page, "#browse-view");
    await page.click("#manage-open");
    await waitVisible(page, "#manage-view");
    await page.waitForSelector("#console-section:not([hidden])", { timeout: 4000 });
    // openManage() calls refreshAdminSession() async and startJobsLive() which opens a
    // persistent SSE connection. The SSE connection prevents networkidle from firing, so
    // settle() is unreliable here. Poll directly: #console-screens becomes not-hidden when
    // applyAdminSession(true) fires. If not seen within 5s, admin is inactive — unlock.
    let adminAlreadyActive = false;
    try {
      await page.waitForSelector("#console-screens:not([hidden])", { timeout: 5000 });
      adminAlreadyActive = true;
    } catch {
      // admin session not yet reflected — proceed with unlock
    }
    if (!adminAlreadyActive) {
      await page.fill("#admin-password", CONSOLE_ADMIN_PASSWORD);
      await page.click("#admin-unlock-submit");
      await page.waitForSelector("#console-screens:not([hidden])", { timeout: 8000 });
    }
    await page.click("#console-nav button[data-cat='setup']");
    await page.waitForSelector("#console-cat-setup:not([hidden])", { timeout: 4000 });
    // migrationWizardEmpty is an empty <p> (no text, zero bounding box) — waitForSelector
    // state:'visible' fails on zero-size elements. waitForFunction polls the hidden flag directly.
    // NOTE: waitForFunction(fn, arg, options) — pass null for arg, options as third param.
    await page.waitForFunction(
      () => !document.getElementById("migration-wizard-empty")?.hidden,
      null,
      { timeout: 10000 },
    );
    await settle(page);
    await neutralizeVolatileText(page);
  },
  async pairingMintViewer(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#manage-open");
    await waitVisible(page, "#manage-view");
    await page.route("**/api/pairing-codes", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ code: "ABCD-1234", expiresAt: null }),
      });
    });
    await page.click("#mint-viewer-code");
    await page.waitForSelector("#pairing-result:not([hidden])", { timeout: 6000 });
    await settle(page);
    await neutralizeVolatileText(page);
  },
  async pairingMintManager(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#manage-open");
    await waitVisible(page, "#manage-view");
    await page.route("**/api/pairing-codes", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ code: "WXYZ-5678", expiresAt: null }),
      });
    });
    await page.click("#mint-manager-code");
    await page.waitForSelector("#pairing-result:not([hidden])", { timeout: 6000 });
    await settle(page);
    await neutralizeVolatileText(page);
  },
  async pairingMintError(page) {
    await waitVisible(page, "#browse-view");
    await page.click("#manage-open");
    await waitVisible(page, "#manage-view");
    await page.route("**/api/pairing-codes", async (route) => {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "server error" }),
      });
    });
    await page.click("#mint-viewer-code");
    await page.waitForSelector("#pairing-mint-error:not([hidden])", { timeout: 6000 });
    await settle(page);
  },
};

// Open the player from the revealed browse view by clicking the first playable
// media item card, waiting for transport to become available.
async function openPlayer(page) {
  await waitVisible(page, "#browse-view");
  await settle(page);
  const card = page.locator(".item-card").first();
  await card.waitFor({ state: "visible", timeout: 8000 });
  await card.click();
  await waitVisible(page, "#player-view");
  // Wait for the transport to mount (renderTransportStatic runs after
  // playback-info resolves); fall back to a short wait if the empty media
  // element never fires events.
  await page.waitForTimeout(400);
}

// Force the auto-hiding transport chrome visible (it hides on idle).
async function showTransport(page) {
  await page.evaluate(() => {
    const shell = document.getElementById("player-shell");
    if (shell) shell.classList.add("chrome-visible", "show-chrome");
    const t = document.getElementById("player-transport");
    if (t) t.style.opacity = "1";
  });
  // Nudge the shell so the app's own showChrome timer keeps it up during capture.
  await page.mouse.move(640, 450);
  await page.waitForTimeout(150);
}

// ── diff ────────────────────────────────────────────────────────────────────

async function diffAgainstBaseline(id) {
  const curPath = join(IMAGES, `${id}.png`);
  const basePath = join(BASELINES, `${id}.png`);
  const diffPath = join(DIFFS, `${id}.png`);
  if (!existsSync(basePath)) {
    return { id, status: "no-baseline" };
  }
  const cur = PNG.sync.read(await readFile(curPath));
  const base = PNG.sync.read(await readFile(basePath));
  if (cur.width !== base.width || cur.height !== base.height) {
    return { id, status: "size-mismatch", cur: `${cur.width}x${cur.height}`, base: `${base.width}x${base.height}` };
  }
  const diff = new PNG({ width: cur.width, height: cur.height });
  const mismatched = pixelmatch(cur.data, base.data, diff.data, cur.width, cur.height, { threshold: 0.1 });
  const fraction = mismatched / (cur.width * cur.height);
  if (fraction > DIFF_THRESHOLD) {
    await writeFile(diffPath, PNG.sync.write(diff));
    return { id, status: "regressed", mismatched, fraction };
  }
  // Sub-threshold: clear any stale diff.
  await rm(diffPath, { force: true });
  return { id, status: "match", fraction };
}

// ── main ─────────────────────────────────────────────────────────────────────

async function main() {
  await mkdir(IMAGES, { recursive: true });
  await mkdir(BASELINES, { recursive: true });
  await mkdir(DIFFS, { recursive: true });

  const manifest = [];
  const capturedIds = [];
  const hiddenLeaks = []; // { id, offenders[] } — hidden=true yet display!=none

  if (!DIFF_ONLY) {
    const bin = await buildServer();
    const port = await freePort();
    console.log(`==> booting conformance seed server on 127.0.0.1:${port}`);
    const { proc, base } = await bootServer(bin, port);
    let browser;
    try {
      browser = await chromium.launch({ headless: true });
      for (const entry of ENTRIES) {
        const vp = VIEWPORTS[entry.viewport] || VIEWPORTS.desktop;
        const manifestEntry = {
          id: entry.id,
          surface: "web",
          screen: entry.screen,
          state: entry.state,
          features: entry.features,
          viewport: { name: vp.name, w: vp.w, h: vp.h },
          theme: "dark",
          locale: "en",
          a11y: "default",
          // a11y_variants: declared variants required for release per ADR-0023 §9.
          // Capture of these variants is a CI/device residual; this field records the
          // requirement so release tooling can enumerate unmet variant gaps.
          a11y_variants: entry.a11yVariants || [],
          adr: entry.adr || [],
          fixture: entry.fixture || "conformance",
          intent: entry.intent,
          screenshot: `images/${entry.id}.png`,
          hierarchy: `images/${entry.id}.txt`,
          baseline: `baselines/${entry.id}.png`,
          status: entry.status || "captured",
        };
        if (entry.note) manifestEntry.note = entry.note;
        if (entry.leakProof) manifestEntry.leakProof = true;

        if (entry.drive && !entry.reactDriver && entry.status !== "device-only") {
          manifestEntry.status = "listed";
          manifestEntry.note = entry.note ? `${entry.note} ${LEGACY_DRIVER_NOTE}` : LEGACY_DRIVER_NOTE;
          manifest.push(manifestEntry);
          console.log(`--  listed: ${entry.id} (React driver pending)`);
          continue;
        }

        if ((entry.status === "listed" || entry.status === "device-only") || !entry.drive) {
          manifest.push(manifestEntry);
          console.log(`--  ${entry.status || "listed"}: ${entry.id}`);
          continue;
        }

        const context = await browser.newContext({
          viewport: { width: vp.w, height: vp.h },
          deviceScaleFactor: 1,
          reducedMotion: "reduce",
          colorScheme: "dark",
        });
        await context.addInitScript((css) => {
          const apply = () => {
            const style = document.createElement("style");
            style.textContent = css;
            document.documentElement.appendChild(style);
          };
          if (document.documentElement) apply();
          else document.addEventListener("DOMContentLoaded", apply);
        }, FREEZE_CSS);
        const cookies = cookiesFor(entry.session, port);
        if (cookies.length) await context.addCookies(cookies);
        const page = await context.newPage();
        try {
          await page.goto(`${base}/web/`, { waitUntil: "domcontentloaded" });
          const driver = DRIVERS[entry.drive];
          if (!driver) throw new Error(`no driver "${entry.drive}" for ${entry.id}`);
          const driveResult = await driver(page);
          await page.screenshot({ path: join(IMAGES, `${entry.id}.png`) });
          await dumpHierarchy(page, entry.id);
          // CSS-aware assertion: nothing carrying `hidden` may paint (catches the
          // .manage-crumb / .status-badge[hidden] override that text greps miss).
          const offenders = await hiddenButDisplayedOffenders(page);
          if (offenders.length) {
            hiddenLeaks.push({ id: entry.id, offenders });
            console.error(
              `!!  HIDDEN-BUT-DISPLAYED: ${entry.id}: ${offenders
                .map((o) => `${o.tag}#${o.id || "?"}.${o.cls || "?"} (display:${o.display})`)
                .join(", ")}`,
            );
          }
          if (driveResult && driveResult.terminalStatus !== undefined) {
            manifestEntry.fixtureDetail = { playbackInfoStatus: driveResult.terminalStatus };
          }
          capturedIds.push(entry.id);
          console.log(`==> captured: ${entry.id}`);
        } catch (err) {
          throw new Error(`capture failed for ${entry.id}: ${err.message || err}`);
        } finally {
          await context.close();
        }
        manifest.push(manifestEntry);
      }
    } finally {
      if (browser) await browser.close();
      proc.kill("SIGKILL");
    }
    await writeFile(join(CATALOG, "manifest.json"), JSON.stringify(manifest, null, 2) + "\n");
    console.log(`==> wrote manifest.json (${manifest.length} entries)`);

    if (capturedIds.length === 0) {
      console.error("\n==> FAIL: zero UI catalog entries captured; refusing a vacuous pass.");
      process.exit(1);
    }

    // Hard fail (even under --record): a `hidden` element that still paints is a
    // role/Reveal mis-signal, not a baseline to bless. Stop before re-blessing.
    if (hiddenLeaks.length) {
      console.error(
        `\n==> FAIL: ${hiddenLeaks.length} entrie(s) render an element marked hidden ` +
          `(CSS out-specifies the UA [hidden]{display:none} rule). ` +
          `Fix the offending style rule so [hidden] is authoritative.`,
      );
      process.exit(1);
    }
  }

  // ── baseline / diff ──
  if (RECORD) {
    for (const id of capturedIds) {
      await writeFile(join(BASELINES, `${id}.png`), await readFile(join(IMAGES, `${id}.png`)));
    }
    // Clear stale diffs after a re-bless.
    for (const f of await readdir(DIFFS).catch(() => [])) await rm(join(DIFFS, f), { force: true });
    console.log(`==> RECORD: re-blessed ${capturedIds.length || "all"} baselines`);
    return;
  }

  const ids = DIFF_ONLY ? await listPngs(IMAGES) : capturedIds;
  if (ids.length === 0) {
    console.error("\n==> FAIL: zero UI catalog images selected for diff.");
    process.exit(1);
  }
  const results = [];
  for (const id of ids) results.push(await diffAgainstBaseline(id));
  const regressed = results.filter((r) => r.status === "regressed" || r.status === "size-mismatch");
  const noBaseline = results.filter((r) => r.status === "no-baseline");
  console.log("\n==> regression report");
  for (const r of results) {
    const detail =
      r.status === "regressed"
        ? ` (${(r.fraction * 100).toFixed(3)}% pixels, diffs/${r.id}.png)`
        : r.status === "size-mismatch"
          ? ` (cur ${r.cur} vs base ${r.base})`
          : "";
    console.log(`    ${r.status.padEnd(14)} ${r.id}${detail}`);
  }
  if (noBaseline.length) {
    console.log(`\n==> ${noBaseline.length} entrie(s) have no baseline yet. Run \`npm run record\` to bless first renders.`);
  }
  if (regressed.length) {
    console.error(`\n==> FAIL: ${regressed.length} visual regression(s). Inspect ui-catalog/diffs/, then re-record intentionally with \`npm run record\`.`);
    process.exitCode = 1;
  } else {
    console.log(`\n==> OK: no regressions (${results.length - noBaseline.length} compared).`);
  }
}

async function listPngs(dir) {
  const files = await readdir(dir).catch(() => []);
  return files.filter((f) => f.endsWith(".png")).map((f) => f.replace(/\.png$/, ""));
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
