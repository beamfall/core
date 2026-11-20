import type { Page } from "@playwright/test";
import { mediaPath } from "../src/areas/viewer/lib/mediaAddress";
import {
  removeAudioLibrary,
  type SeededAudioLibrary,
  seedAudioLibrary,
  TEST_ALBUM,
} from "./lib/audio-library";
import { collectFatalErrors, harnessState } from "./lib/spec";
import { expect, test } from "./lib/test";

// The audio stage cross-fades its title: the outgoing layer stays mounted (data-state="out")
// for a moment after a track change, so match only the layer that is coming in.
const AUDIO_TITLE = '.bf-audio-meta-layer[data-state="in"] .bf-audio-title';

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

/**
 * A track's watch address can use a bare ID, a standalone token, or a
 * readable slug ending in the same token (CORE-2329).
 */
function watchAddress(id: string): RegExp {
  const token = id.replace(/^media-sha256-/, "").replace(/^media-sample1-/, "sample1[.]");
  return new RegExp(`/web/watch/(?:${id}|${token}|[^?#]*-${token})$`);
}

/** Plays Track One from its album page's track list (CORE-1635), which starts playback directly. */
async function playAlbumFromTrackOne(page: Page): Promise<void> {
  await page.goto(`/web/entity/${library.albumEntityID}`);
  await page
    .getByRole("button", { name: /Track One/ })
    .first()
    .click();
  await expect(page).toHaveURL(watchAddress(library.tracks[0].id));
}

test("plays an album and advances to its next track (CORE-1640)", async ({ page }) => {
  test.setTimeout(60_000);
  const errors = collectFatalErrors(page);
  const [first, second] = library.tracks;
  await playAlbumFromTrackOne(page);

  await expect(page.locator(AUDIO_TITLE)).toHaveText(first.title);
  await expectPlaying(page);
  const started = (await mediaState(page))?.currentTime ?? 0;
  await expectPlaying(page, started);
  await expect
    .poll(() => sessionMetadata(page))
    .toEqual({ title: first.title, artist: TEST_ALBUM.artist, album: TEST_ALBUM.album });

  // The track ends after TEST_ALBUM.seconds; the queue moves on without a click.
  expect(`/web${mediaPath("watch", second.id)}`).toMatch(watchAddress(second.id));
  const sampledID = "media-sample1-0123456789abcdef0";
  expect(`/web${mediaPath("watch", sampledID)}`).toMatch(watchAddress(sampledID));
  await expect(page).toHaveURL(watchAddress(second.id), { timeout: 15_000 });
  await expect(page.locator(AUDIO_TITLE)).toHaveText(second.title);
  await expectPlaying(page);
  await expect
    .poll(() => sessionMetadata(page))
    .toEqual({ title: second.title, artist: TEST_ALBUM.artist, album: TEST_ALBUM.album });
  expect(errors).toEqual([]);
});

test("elapsed clock advances (CORE-1491)", async ({ page }) => {
  test.setTimeout(60_000);
  await playAlbumFromTrackOne(page);
  await expect(page.locator(AUDIO_TITLE)).toHaveText(library.tracks[0].title);
  await expectPlaying(page);
  await expect(page.locator(".bf-time.is-start")).not.toHaveText("0:00", { timeout: 10_000 });
});

// CORE-1490: Recently Added mixes the seeded tracks with the seed's video.
// Playing a track from that list must queue only the audio items, so the audio
// stage counts the tracks alone. Rows carry their mediaClass (CORE-1492), so the
// player narrows the queue by class; before that it read "1 of 4". The Home
// shelf folds the album's new tracks into one card (CORE-2524), so the tracks
// are played from the shelf's See all page, which lists every item.
test("a track from a mixed shelf queues only audio items (CORE-1490)", async ({ page }) => {
  test.setTimeout(60_000);
  const titles = library.tracks.map((track) => track.title);
  const trackCard = new RegExp(titles.join("|"));
  await page.goto("/web/");
  // Recently Added by name: earlier specs also fill "Your Most-Played" with the
  // video and a track, and that shelf queues a single audio item.
  await page
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "Recently Added" }) })
    .getByRole("button", { name: "See all" })
    .click();
  await expect(page).toHaveURL(/\/seeall\//);
  // The URL changes before the See all route replaces Home, so wait for the
  // page's own h1 (Home's shelf titles are h2); otherwise "Sample Media" can
  // match a Home card too.
  await expect(
    page.getByRole("heading", { name: "Recently Added", level: 1, exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: /Sample Media/ })).toBeVisible();
  const card = page.getByRole("link", { name: trackCard }).first();
  const label = (await card.getAttribute("aria-label")) ?? (await card.innerText());
  const title = titles.find((candidate) => label.includes(candidate));
  await card.click();
  await page.locator(".bf-detail-play").click();
  await expect(page.locator(AUDIO_TITLE)).toHaveText(title ?? "");
  await expectPlaying(page);
  await expect(page.locator(".bf-audio-position")).toHaveText(new RegExp(` of ${titles.length}$`), {
    timeout: 5_000,
  });
});
