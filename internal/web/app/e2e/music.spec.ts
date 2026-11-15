import type { Page } from "@playwright/test";
import {
  removeAudioLibrary,
  type SeededAudioLibrary,
  seedAudioLibrary,
  TEST_ALBUM,
} from "./lib/audio-library";
import { collectFatalErrors, harnessState } from "./lib/spec";
import { expect, test } from "./lib/test";

// MUSIC (CORE-1640): real audio playback in the browser. A Music-plugin Library of
// generated, tagged tracks is scanned into the self-booted server, then the viewer
// plays them with no network mocks: the media element runs, the audio stage and
// Media Session describe the track, and the queue advances on its own.

test.describe.configure({ mode: "serial" });

let library: SeededAudioLibrary;

test.beforeAll(async () => {
  test.setTimeout(120_000);
  library = await seedAudioLibrary(harnessState().baseURL, TEST_ALBUM, 60_000);
});

test.afterAll(async () => {
  if (library) await removeAudioLibrary(harnessState().baseURL, library);
});

interface MediaState {
  paused: boolean;
  currentTime: number;
}

async function mediaState(page: Page): Promise<MediaState | null> {
  return page.evaluate(() => {
    const element = document.querySelector<HTMLMediaElement>("video, audio");
    return element ? { paused: element.paused, currentTime: element.currentTime } : null;
  });
}

async function sessionMetadata(page: Page) {
  return page.evaluate(() => {
    const metadata = navigator.mediaSession?.metadata;
    return metadata
      ? { title: metadata.title, artist: metadata.artist, album: metadata.album }
      : null;
  });
}

/** Asserts the media element is running and its clock moves past `after` seconds. */
async function expectPlaying(page: Page, after = 0): Promise<void> {
  await expect.poll(async () => (await mediaState(page))?.paused, { timeout: 10_000 }).toBe(false);
  await expect
    .poll(async () => (await mediaState(page))?.currentTime ?? 0, { timeout: 10_000 })
    .toBeGreaterThan(after);
}

/** Opens Track One from its album page and presses Play on its detail page. */
async function playAlbumFromTrackOne(page: Page): Promise<void> {
  await page.goto(`/web/entity/${library.albumEntityID}`);
  await page
    .getByRole("button", { name: /Track One/ })
    .first()
    .click();
  await expect(page).toHaveURL(new RegExp(`/web/media/${library.tracks[0].id}$`));
  await page.locator(".bf-detail-play").click();
  await expect(page).toHaveURL(new RegExp(`/web/watch/${library.tracks[0].id}$`));
}

test("plays an album and advances to its next track (CORE-1640)", async ({ page }) => {
  test.setTimeout(60_000);
  const errors = collectFatalErrors(page);
  const [first, second] = library.tracks;
  await playAlbumFromTrackOne(page);

  await expect(page.locator(".bf-audio-title")).toHaveText(first.title);
  await expectPlaying(page);
  const started = (await mediaState(page))?.currentTime ?? 0;
  await expectPlaying(page, started);
  await expect
    .poll(() => sessionMetadata(page))
    .toEqual({ title: first.title, artist: TEST_ALBUM.artist, album: TEST_ALBUM.album });

  // The track ends after TEST_ALBUM.seconds; the queue moves on without a click.
  await expect(page).toHaveURL(new RegExp(`/web/watch/${second.id}$`), { timeout: 15_000 });
  await expect(page.locator(".bf-audio-title")).toHaveText(second.title);
  await expectPlaying(page);
  await expect
    .poll(() => sessionMetadata(page))
    .toEqual({ title: second.title, artist: TEST_ALBUM.artist, album: TEST_ALBUM.album });
  expect(errors).toEqual([]);
});

test("elapsed clock advances (CORE-1491)", async ({ page }) => {
  test.setTimeout(60_000);
  await playAlbumFromTrackOne(page);
  await expect(page.locator(".bf-audio-title")).toHaveText(library.tracks[0].title);
  await expectPlaying(page);
  await expect(page.locator(".bf-time.is-start")).not.toHaveText("0:00", { timeout: 10_000 });
});

// CORE-1490: Home > Recently Added mixes the seeded tracks with the seed's video.
// Playing a track from that shelf must queue only the audio items, so the audio
// stage counts the tracks alone. Marked as an expected failure until the web
// player narrows a shelf queue by class: it reads "1 of 4" today. When the fix
// lands this test turns red; remove test.fail() then.
test("a track from a mixed shelf queues only audio items (CORE-1490)", async ({ page }) => {
  test.fail(true, "CORE-1490: the Recently Added queue still includes the video");
  test.setTimeout(60_000);
  const titles = library.tracks.map((track) => track.title);
  const trackCard = new RegExp(titles.join("|"));
  await page.goto("/web/");
  const shelf = page
    .locator("section")
    .filter({ has: page.getByRole("button", { name: /Sample Media/ }) })
    .filter({ has: page.getByRole("button", { name: trackCard }) })
    .last();
  const card = shelf.getByRole("button", { name: trackCard }).first();
  const label = (await card.getAttribute("aria-label")) ?? (await card.innerText());
  const title = titles.find((candidate) => label.includes(candidate));
  await card.click();
  await page.locator(".bf-detail-play").click();
  await expect(page.locator(".bf-audio-title")).toHaveText(title ?? "");
  await expectPlaying(page);
  await expect(page.locator(".bf-audio-position")).toHaveText(new RegExp(` of ${titles.length}$`), {
    timeout: 5_000,
  });
});
