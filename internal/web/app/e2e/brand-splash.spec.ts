import type { CDPSession, Page } from "@playwright/test";
import { expect, test } from "./lib/test";

// Cold-load logo intro (CORE-2556): the splash plays the horizontal lockup when the viewport is
// at least as wide as it is tall and the stacked lockup otherwise, switches layout mid-intro
// without restarting, shows the static lockup under reduced motion, and hands off to the first
// screen. The splash leaves as soon as the app has rendered (CORE-2928), so each test holds the
// bundle back to inspect it, and Chromium's animation domain slows the intro so the checks run
// well inside it.

const SLOW_PLAYBACK = 0.1;

type SplashState = {
  visible: string[];
  animations: Record<string, number>;
  startTimes: Record<string, Array<number | null>>;
};

async function slowIntro(page: Page): Promise<CDPSession> {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Animation.enable");
  await cdp.send("Animation.setPlaybackRate", { playbackRate: SLOW_PLAYBACK });
  return cdp;
}

function splashState(page: Page): Promise<SplashState> {
  return page.evaluate(() => {
    const hosts = Array.from(
      document.querySelectorAll<HTMLElement>("#bf-splash .bf-splash__layout"),
    );
    const state: SplashState = { visible: [], animations: {}, startTimes: {} };
    for (const host of hosts) {
      const layout = host.dataset.layout ?? "";
      if (getComputedStyle(host).visibility === "visible") state.visible.push(layout);
      const animations = host.shadowRoot?.getAnimations() ?? [];
      state.animations[layout] = animations.length;
      state.startTimes[layout] = [
        ...new Set(animations.map((a) => (a.startTime === null ? null : Number(a.startTime)))),
      ];
    }
    return state;
  });
}

const held = new WeakSet<Page>();

// Hold the bundle back so the splash can be inspected before the handoff, and wait until the
// ready sentinel has released both intros and every animation has a start time.
async function openHeld(page: Page): Promise<void> {
  if (!held.has(page)) {
    held.add(page);
    await page.route("**/web/assets/**", (route) => setTimeout(() => void route.continue(), 1_500));
  }
  await page.goto("/web/", { waitUntil: "commit" });
  await page.locator("#bf-splash .bf-splash__ready").waitFor({ state: "attached" });
  await page.waitForFunction(() =>
    Array.from(document.querySelectorAll<HTMLElement>("#bf-splash .bf-splash__layout"))
      .flatMap((host) => host.shadowRoot?.getAnimations() ?? [])
      .every((animation) => animation.startTime !== null),
  );
}

async function expectHandOff(page: Page): Promise<void> {
  await expect(page.locator("#bf-splash")).toHaveCount(0, { timeout: 10_000 });
  await expect(page.locator("#root")).not.toBeEmpty();
}

test("landscape plays the horizontal lockup intro, then hands off", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await slowIntro(page);
  await openHeld(page);
  const state = await splashState(page);
  expect(state.visible).toEqual(["horizontal"]);
  expect(state.animations.horizontal).toBeGreaterThan(0);
  await expectHandOff(page);
});

test("portrait plays the stacked lockup intro", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await slowIntro(page);
  await openHeld(page);
  const state = await splashState(page);
  expect(state.visible).toEqual(["stacked"]);
  expect(state.animations.stacked).toBeGreaterThan(0);
  await expectHandOff(page);
});

test("rotating mid-intro switches layout at the same point in the intro", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await slowIntro(page);
  await openHeld(page);
  const before = await splashState(page);
  expect(before.visible).toEqual(["horizontal"]);

  await page.setViewportSize({ width: 720, height: 1280 });
  const after = await splashState(page);
  expect(after.visible).toEqual(["stacked"]);
  // Both intros run on one clock from the same start, and the switch restarts neither.
  expect(after.startTimes.horizontal).toHaveLength(1);
  expect(after.startTimes.horizontal[0]).not.toBeNull();
  expect(after.startTimes.stacked).toEqual(after.startTimes.horizontal);
  expect(after.startTimes).toEqual(before.startTimes);
  await expectHandOff(page);
});

test("reduced motion shows the static lockup in either layout, then hands off", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  for (const [width, height, layout] of [
    [1280, 720, "horizontal"],
    [390, 844, "stacked"],
  ] as const) {
    await page.setViewportSize({ width, height });
    await openHeld(page);
    const state = await splashState(page);
    expect(state.visible).toEqual([layout]);
    expect(state.animations).toEqual({ horizontal: 0, stacked: 0 });
    await expectHandOff(page);
  }
});
