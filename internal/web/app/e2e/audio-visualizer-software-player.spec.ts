import { routeSyntheticAlbum, type SyntheticTrack } from "./lib/synthetic-audio";
import { expect, test } from "./lib/test";

// The audio stage's Oscillux visualizer (CORE-1309) on a software WebGL2
// context, which headless Chromium gives by default (SwiftShader). Its
// full-frame passes would run on the CPU and stall the page, so the
// visualizer leaves its slot empty and playback goes on as before. Focused run:
//
//   npx playwright test e2e/audio-visualizer-software-player.spec.ts --project=player-desktop

const TRACK: SyntheticTrack = {
  id: "media-visualizer-software",
  title: "Visualizer Software",
  frequency: 330,
  seconds: 20,
};

test.use({ serviceWorkers: "block" });

test("an audio item plays with an empty stage on software WebGL", async ({ page }) => {
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
  if (await video.evaluate((v: HTMLVideoElement) => v.paused)) {
    await page.getByRole("button", { name: "Play", exact: true }).click();
  }
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime), { timeout: 15_000 })
    .toBeGreaterThan(1);
  await expect(page.locator("[data-visualizer-slot]")).toBeAttached();
  await expect(page.locator("[data-visualizer-slot] canvas")).toHaveCount(0);
  expect(problems).toEqual([]);
});
