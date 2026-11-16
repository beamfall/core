import { routeSyntheticAlbum, type SyntheticTrack } from "./lib/synthetic-audio";
import { expect, test } from "./lib/test";

// The audio stage's Oscillux visualizer (CORE-1309), in a real browser behind
// the real server's CSP: the wasm core compiles, the visual draws through the
// present pass and the flash limiter, and the picture moves while the track
// plays. Headless Chromium draws WebGL on SwiftShader unless it is given the
// GPU, and the visualizer refuses a software context (see
// audio-visualizer-software-player.spec.ts), so this file runs on the GPU.
// The player projects' testMatch (/player\.spec\.ts/) picks this file up.
// Focused run:
//
//   npx playwright test e2e/audio-visualizer-player.spec.ts --project=player-desktop

const TRACK: SyntheticTrack = {
  id: "media-visualizer-one",
  title: "Visualizer One",
  frequency: 220,
  seconds: 20,
};

test.use({ serviceWorkers: "block", launchOptions: { args: ["--enable-gpu"] } });

test("an audio item plays with an Oscillux visual in the stage", async ({ page }) => {
  test.setTimeout(60_000);
  const problems: string[] = [];
  page.on("console", (message) => {
    if (message.text().startsWith("visualizer:")) problems.push(message.text());
  });
  page.on("pageerror", (error) => problems.push(error.message));
  await routeSyntheticAlbum(page, [TRACK]);
  await page.goto(`/web/watch/${TRACK.id}`);
  await expect(page.getByRole("heading", { name: TRACK.title, level: 2 })).toBeVisible();
  const video = page.locator("video");
  await expect.poll(() => video.evaluate((v: HTMLVideoElement) => v.duration)).toBeGreaterThan(0);
  // Start it the way a listener does, whether or not the browser autoplayed it.
  if (await video.evaluate((v: HTMLVideoElement) => v.paused)) {
    await page.getByRole("button", { name: "Play", exact: true }).click();
  }
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: 15_000 })
    .toBeGreaterThan(1);

  const canvas = page.locator("[data-visualizer-slot] canvas");
  await expect(canvas).toBeVisible();
  // The canvas's backing store follows its box (at least the limiter's 64 x 36).
  await expect
    .poll(() => canvas.evaluate((c: HTMLCanvasElement) => c.width >= 64 && c.height >= 36))
    .toBe(true);
  // Two frames half a second apart differ: the visual is drawing and moving.
  const first = await canvas.screenshot();
  await page.waitForTimeout(500);
  const second = await canvas.screenshot();
  expect(first.equals(second)).toBe(false);
  await test.info().attach("visual", { body: second, contentType: "image/png" });
  expect(problems).toEqual([]);
});
