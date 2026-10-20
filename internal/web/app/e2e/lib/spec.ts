import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, type Page } from "@playwright/test";

// Read the harness state global-setup recorded (base URL + whether the Console
// admin lane is available on this server).
export interface HarnessState {
  baseURL: string;
  adminAvailable: boolean;
}

export function harnessAuthDir(): string {
  return join(import.meta.dirname, "..", ".auth", String(process.env.BEAMFALL_E2E_PORT ?? 8830));
}

export function harnessState(): HarnessState {
  const file = join(harnessAuthDir(), "state.json");
  return JSON.parse(readFileSync(file, "utf8")) as HarnessState;
}

// Attach a console/pageerror collector with the same filtering posture as
// smoke.spec.ts: a failed API fetch the browser logs as a 401/403 "error" is
// expected pre-auth noise, not a render failure; a real React crash surfaces as
// an uncaught pageerror and is always fatal. Returns the live error array; a
// passing spec asserts it is empty at the end.
export function collectFatalErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() !== "error") return;
    const text = msg.text();
    // Expected pre-auth / reveal-gated noise: probes that 401/403 before a
    // session resolves, or restricted surfaces the unrevealed session cannot
    // read. The browser logs the failed fetch as an "error" — not a crash.
    if (/Failed to load resource.*\b(401|403)\b/.test(text)) return;
    errors.push(text);
  });
  page.on("pageerror", (err) => errors.push(`pageerror: ${err.message}`));
  return errors;
}

export async function applyLargeText(page: Page): Promise<void> {
  await page.addStyleTag({
    content: ":root { font-size: 20px !important; }",
  });
}

export async function expectLargeTextApplied(page: Page): Promise<void> {
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).fontSize))
    .toBe("20px");
}

export async function expectNoHorizontalDocumentOverflow(page: Page): Promise<void> {
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
}

export async function expectReducedMotionActive(page: Page): Promise<void> {
  await expect
    .poll(() => page.evaluate(() => matchMedia("(prefers-reduced-motion: reduce)").matches))
    .toBe(true);
}

export async function expectReducedMotionDurations(page: Page): Promise<void> {
  const maxDurationMs = await page.evaluate(() => {
    function durationMs(value: string): number {
      return Math.max(
        0,
        ...value.split(",").map((part) => {
          const trimmed = part.trim();
          if (!trimmed || trimmed === "0s") return 0;
          if (trimmed.endsWith("ms")) return Number.parseFloat(trimmed);
          if (trimmed.endsWith("s")) return Number.parseFloat(trimmed) * 1000;
          return 0;
        }),
      );
    }

    return Math.max(
      0,
      ...Array.from(document.querySelectorAll("#root, #root *")).map((el) => {
        const style = getComputedStyle(el);
        return Math.max(durationMs(style.animationDuration), durationMs(style.transitionDuration));
      }),
    );
  });
  expect(maxDurationMs).toBeLessThanOrEqual(0.002);
}

export async function expectForcedColorsActive(page: Page): Promise<void> {
  await expect
    .poll(() => page.evaluate(() => matchMedia("(forced-colors: active)").matches))
    .toBe(true);
}

export async function expectBodyTextContrastsBackground(page: Page): Promise<void> {
  const colors = await page.evaluate(() => {
    const style = getComputedStyle(document.body);
    return { color: style.color, backgroundColor: style.backgroundColor };
  });
  expect(colors.color).not.toBe(colors.backgroundColor);
}
