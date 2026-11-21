import { routeSyntheticAlbum, TEST_TRACKS } from "./lib/synthetic-audio";
import { expect, type Locator, type Page, test } from "./lib/test";

// Audio playback across the viewer (CORE-1633). The player projects' testMatch
// (/player\.spec\.ts/) picks this file up, so it runs on desktop and mobile
// Chromium with the enrolled viewer session.

const position = (video: Locator) =>
  video.evaluate((element: HTMLVideoElement) => element.currentTime);
const paused = (video: Locator) => video.evaluate((element: HTMLVideoElement) => element.paused);

async function startTrackOne(page: Page): Promise<Locator> {
  await routeSyntheticAlbum(page);
  await page.goto(`/web/watch/${TEST_TRACKS[0].id}`);
  const video = page.locator("video");
  await expect(page.getByRole("heading", { name: "Track One", level: 2 })).toBeVisible();
  await expect.poll(() => video.evaluate((element: HTMLVideoElement) => element.duration)).toBe(30);
  if (await paused(video)) await page.getByRole("button", { name: "Play", exact: true }).click();
  await expect.poll(() => position(video)).toBeGreaterThan(0.2);
  return video;
}

test("audio keeps playing in the mini player while browsing to search and back", async ({
  page,
}) => {
  const video = await startTrackOne(page);

  // Back to Browse, then to search, all in-app: playback must never stop.
  // The controls fade after 3 s of playback and a loaded host can get there
  // first; focusing Back reveals them, as it would for a keyboard user.
  const backButton = page.getByRole("button", { name: "Back", exact: true });
  await backButton.focus();
  await backButton.click();
  await expect(page).toHaveURL(/\/web\/?$/);
  const search = page
    .getByRole("searchbox", { name: "Search" })
    .or(page.getByRole("combobox", { name: "Search" }));
  await search.first().fill("Track");
  await search.first().press("Enter");
  await expect(page).toHaveURL(/\/web\/search/);

  const mini = page.getByRole("region", { name: "Now playing" });
  await expect(mini).toBeVisible();
  await expect(mini.getByText("Track One")).toBeVisible();
  await expect(mini.getByText("Test Artist")).toBeVisible();
  await expect(mini.getByRole("progressbar", { name: "Playback position" })).toBeVisible();
  await expect(mini.getByRole("button", { name: "Pause" })).toBeVisible();
  await expect(mini.getByRole("button", { name: "Next" })).toBeEnabled();
  expect(await paused(video)).toBe(false);
  const onSearch = await position(video);
  await expect.poll(() => position(video)).toBeGreaterThan(onSearch + 0.5);

  // Keyboard: the Open player control is a focusable native button.
  const open = mini.getByRole("button", { name: "Open player" });
  await open.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(new RegExp(`/web/watch/${TEST_TRACKS[0].id}`));
  await expect(mini).toBeHidden();
  await expect(page.getByRole("heading", { name: "Track One", level: 2 })).toBeVisible();
  expect(await paused(video)).toBe(false);
  const back = await position(video);
  expect(back).toBeGreaterThan(onSearch);
  await expect.poll(() => position(video)).toBeGreaterThan(back + 0.3);
});
