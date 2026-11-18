import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, type Page, test } from "./lib/test";

// CORE-1709: a file with three audio streams and five subtitle streams played
// on the HLS route. The server leg (track resolution, the audio-copy rung and
// the ffmpeg -map for the chosen stream) is covered by the Go tests in
// internal/playback and internal/ffmpeg; this spec proves the web player lists
// every stream the server reports, sends the viewer's choice back to
// /api/playback-info and shows text subtitles over the HLS stream. The stream
// itself is the synthetic browser-compat fMP4 fixture (colour bars and a tone).
const fixtures = join(import.meta.dirname, "fixtures");
const playlist = readFileSync(join(fixtures, "browser-compat.m3u8"), "utf8");
const initSegment = readFileSync(join(fixtures, "browser-compat-init.mp4"));
const mediaSegment = readFileSync(join(fixtures, "browser-compat0.m4s"));

const AUDIO = [
  { streamIndex: 1, codec: "aac", language: "eng", channels: 2, default: true },
  { streamIndex: 2, codec: "ac3", language: "fra", channels: 6 },
  { streamIndex: 3, codec: "aac", language: "eng", channels: 2, commentary: true },
];

const CAPTIONS = [
  {
    id: "muxed-main-4",
    url: "/api/e2e/captions/4.vtt",
    format: "subrip",
    streamIndex: 4,
    language: "eng",
  },
  {
    id: "muxed-main-5",
    url: "/api/e2e/captions/5.vtt",
    format: "ass",
    streamIndex: 5,
    language: "spa",
  },
  { id: "muxed-main-6", format: "dvd_subtitle", streamIndex: 6, language: "fra" },
  {
    id: "sidecar-main-0",
    url: "/api/e2e/captions/s0.vtt",
    format: "srt",
    language: "deu",
    forced: true,
  },
  {
    id: "sidecar-main-1",
    url: "/api/e2e/captions/s1.vtt",
    format: "srt",
    language: "eng",
    sdh: true,
  },
];

type TrackRequest = { selectedAudioIndex?: number; selectedSubtitleIndex?: number };

async function installTrackRoutes(page: Page): Promise<TrackRequest[]> {
  const requests: TrackRequest[] = [];
  await page.route("**/api/playback-info", async (route) => {
    const body = (route.request().postDataJSON() ?? {}) as TrackRequest;
    requests.push({
      selectedAudioIndex: body.selectedAudioIndex,
      selectedSubtitleIndex: body.selectedSubtitleIndex,
    });
    const chosen = body.selectedAudioIndex ?? 1;
    await route.fulfill({
      json: {
        decision: "transcode",
        streams: [{ kind: "hls", url: "/api/e2e/browser-compat.m3u8" }],
        audioTracks: AUDIO.map((track) => ({ ...track, selected: track.streamIndex === chosen })),
        captionTracks: CAPTIONS,
        subtitlesUnavailableReason: "no-client-renderable-subtitle-track",
      },
    });
  });
  await page.route("**/api/e2e/browser-compat.m3u8", (route) =>
    route.fulfill({ contentType: "application/vnd.apple.mpegurl", body: playlist }),
  );
  await page.route("**/api/e2e/browser-compat-init.mp4", (route) =>
    route.fulfill({ contentType: "video/mp4", body: initSegment }),
  );
  await page.route("**/api/e2e/browser-compat0.m4s", (route) =>
    route.fulfill({ contentType: "video/iso.segment", body: mediaSegment }),
  );
  await page.route("**/api/e2e/captions/*.vtt", (route) =>
    route.fulfill({
      contentType: "text/vtt",
      body: "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nTest caption\n",
    }),
  );
  return requests;
}

async function openSettings(page: Page): Promise<void> {
  const button = page.getByRole("button", { name: "Settings", exact: true });
  if ((await button.getAttribute("aria-expanded")) !== "true") await button.click();
  await expect(button).toHaveAttribute("aria-expanded", "true");
}

async function openMenu(page: Page, name: string) {
  await openSettings(page);
  const trigger = page
    .getByRole("combobox", { name, exact: true })
    .or(page.getByRole("button", { name, exact: true }))
    .first();
  await trigger.click();
  const listbox = page.getByRole("listbox", { name });
  await expect(listbox).toBeVisible();
  return listbox;
}

function shownCaption(page: Page): Promise<string | null> {
  return page.evaluate(() => {
    const video = document.querySelector("video");
    if (!video) return null;
    for (const element of video.querySelectorAll<HTMLTrackElement>("track[data-bf-caption]")) {
      if (element.track.mode === "showing") return element.dataset.bfCaption ?? null;
    }
    return null;
  });
}

test("HLS route offers every audio and subtitle stream of a multi-track file", async ({ page }) => {
  const requests = await installTrackRoutes(page);
  await page.goto("/web/watch/media-alpha");
  await expect(page.locator("video")).toHaveCount(1);
  await expect.poll(() => requests.length).toBeGreaterThan(0);

  const audio = await openMenu(page, "Audio");
  const audioNames = [
    "English · AAC · Stereo",
    "French · Dolby Digital · 5.1",
    "English · AAC · Stereo · Commentary",
  ];
  for (const name of audioNames) {
    await expect(audio.getByRole("option", { name, exact: true })).toBeVisible();
  }
  await expect(audio.getByRole("option")).toHaveCount(3);
  await expect(audio.getByRole("option", { name: audioNames[0], exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );

  const before = requests.length;
  await audio.getByRole("option", { name: audioNames[1], exact: true }).click();
  await expect.poll(() => requests.slice(before).map((r) => r.selectedAudioIndex)).toContain(2);
  const audioAfter = await openMenu(page, "Audio");
  await expect(
    audioAfter.getByRole("option", { name: audioNames[1], exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Escape");

  const subtitles = await openMenu(page, "Subtitles");
  const subtitleNames = [
    "Subtitles off",
    "English",
    "English · SDH",
    "French · Burned in",
    "German · Forced",
    "Spanish",
  ];
  for (const name of subtitleNames) {
    await expect(subtitles.getByRole("option", { name, exact: true })).toBeVisible();
  }
  const unavailable = subtitles.getByRole("option", {
    name: /Some subtitles can.t be shown in this browser/,
  });
  await expect(unavailable).toHaveAttribute("aria-disabled", "true");

  await subtitles.getByRole("option", { name: "German · Forced", exact: true }).click();
  await expect.poll(() => shownCaption(page)).toBe("sidecar-main-0");

  // A bitmap stream has no text rendition: choosing it renegotiates a burn-in.
  const beforeBurn = requests.length;
  const subtitlesAgain = await openMenu(page, "Subtitles");
  await subtitlesAgain.getByRole("option", { name: "French · Burned in", exact: true }).click();
  await expect
    .poll(() => requests.slice(beforeBurn).map((r) => r.selectedSubtitleIndex))
    .toContain(6);
  // The audio choice survives the burn-in renegotiation.
  expect(requests.at(-1)?.selectedAudioIndex).toBe(2);
});
