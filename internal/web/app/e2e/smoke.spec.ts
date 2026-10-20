import { expect, test } from "@playwright/test";

test("finish parity rendering contract is pinned", async ({ page }, testInfo) => {
  expect(process.env.LANG).toBe("en_US.UTF-8");
  expect(process.env.LC_ALL).toBe("en_US.UTF-8");
  expect(process.env.TZ).toBe("UTC");
  expect(testInfo.config.workers).toBe(1);

  const rendering = await page.evaluate(() => ({
    viewport: [window.innerWidth, window.innerHeight],
    screen: [window.screen.width, window.screen.height],
    deviceScaleFactor: window.devicePixelRatio,
    locale: navigator.language,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  }));
  expect(rendering).toEqual({
    viewport: [1280, 720],
    screen: [1280, 720],
    deviceScaleFactor: 1,
    locale: "en-US",
    timezone: "UTC",
  });
});

// Runtime smoke: the embedded React app must MOUNT and render against the real
// server (not just compile). A fresh browser has no enrolled session, so the
// session spine 401s and routes to /enroll — landing there proves providers
// init, routing works, and the bundle executes without a fatal error.
test("app mounts and renders the enroll surface for an unenrolled browser", async ({ page }) => {
  const consoleErrors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() !== "error") return;
    const text = msg.text();
    // Expected pre-enroll noise: the session spine probes the API without a
    // session and gets 401/403; the browser logs the failed fetch as an
    // "error". That is not a render failure — ignore it.
    if (/Failed to load resource.*\b(401|403)\b/.test(text)) return;
    consoleErrors.push(text);
  });
  // A real React crash surfaces as an uncaught pageerror — always fatal.
  page.on("pageerror", (err) => consoleErrors.push(`pageerror: ${err.message}`));

  await page.goto("/web/");

  // The React root mounted with real content (not an empty shell).
  const root = page.locator("#root");
  await expect(root).not.toBeEmpty();

  // Unenrolled → the enroll surface (a pairing-code form) is shown.
  await expect(page.getByRole("heading").first()).toBeVisible();
  await expect(page.locator("form")).toBeVisible();

  await page.screenshot({ path: "e2e/.artifacts/smoke-enroll.png", fullPage: true });

  // No uncaught runtime errors. (Network 401s are expected and not console
  // errors; a fatal render bug would surface here.)
  expect(consoleErrors, consoleErrors.join("\n")).toEqual([]);
});
