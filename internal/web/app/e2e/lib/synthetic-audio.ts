import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, renameSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Page, Route } from "@playwright/test";

// A synthetic audio album for player specs (CORE-1633, CORE-1634): sine tones
// rendered by ffmpeg, served through page routes with the playback-info and
// Media reads the viewer makes, so no real recording and no server-side seed
// is needed. Shared on purpose: any audio spec can stub an album with it.

export interface SyntheticTrack {
  id: string;
  title: string;
  /** Tone pitch in Hz; distinct per track so a spec can tell them apart. */
  frequency: number;
  seconds: number;
  /** Silence before and after the tone, declared as the stream's gapless trim (CORE-1626). */
  gapless?: { delaySamples: number; paddingSamples: number };
}

export const TEST_ALBUM = { id: "entity-test-album", title: "Test Album" };
export const TEST_ARTIST = { id: "entity-test-artist", title: "Test Artist" };

export const TEST_TRACKS: SyntheticTrack[] = [
  { id: "media-track-one", title: "Track One", frequency: 440, seconds: 30 },
  { id: "media-track-two", title: "Track Two", frequency: 660, seconds: 30 },
];

const cacheDir = join(tmpdir(), "beamfall-e2e-synthetic-audio");
const wavMemoryCache = new Map<string, Buffer>();

/**
 * A mono 44.1 kHz WAV of the track's tone (wrapped in its declared silence),
 * rendered once per machine and cached in memory per Playwright worker.
 */
export function syntheticToneWAV(
  track: Pick<SyntheticTrack, "frequency" | "seconds" | "gapless">,
): Buffer {
  const silence = track.gapless
    ? `-pad${track.gapless.delaySamples}-${track.gapless.paddingSamples}`
    : "";
  const file = join(cacheDir, `sine-${track.frequency}hz-${track.seconds}s${silence}.wav`);
  const cached = wavMemoryCache.get(file);
  if (cached) return cached;
  mkdirSync(cacheDir, { recursive: true });
  const filter = track.gapless
    ? [
        "-af",
        `adelay=delays=${track.gapless.delaySamples}S:all=1,apad=pad_len=${track.gapless.paddingSamples}`,
      ]
    : [];
  if (!existsSync(file)) {
    const tmp = `${file}.${process.pid}.${Math.random().toString(36).slice(2)}.tmp`;
    try {
      execFileSync("ffmpeg", [
        "-hide_banner",
        "-loglevel",
        "error",
        "-y",
        "-f",
        "lavfi",
        "-i",
        `sine=frequency=${track.frequency}:sample_rate=44100:duration=${track.seconds}`,
        ...filter,
        "-ac",
        "1",
        "-c:a",
        "pcm_s16le",
        "-f",
        "wav",
        tmp,
      ]);
      // Publish atomically so concurrent cold-cache workers never read or
      // memoize partially written WAV bytes.
      renameSync(tmp, file);
    } finally {
      rmSync(tmp, { force: true });
    }
  }
  const bytes = readFileSync(file);
  wavMemoryCache.set(file, bytes);
  return bytes;
}

/** The URL a track's stream is served at. */
export function syntheticStreamURL(track: SyntheticTrack): string {
  return `/api/e2e/synthetic/${track.id}.wav`;
}

function fulfilBytes(route: Route, body: Buffer, contentType: string) {
  const range = route.request().headers().range;
  const cacheControl = "public, max-age=3600";
  if (!range) {
    return route.fulfill({
      body,
      contentType,
      headers: { "Accept-Ranges": "bytes", "Cache-Control": cacheControl },
    });
  }
  const [startText, endText] = range.replace("bytes=", "").split("-");
  const start = Number(startText);
  const end = endText ? Math.min(Number(endText), body.length - 1) : body.length - 1;
  return route.fulfill({
    status: 206,
    body: body.subarray(start, end + 1),
    headers: {
      "Accept-Ranges": "bytes",
      "Cache-Control": cacheControl,
      "Content-Range": `bytes ${start}-${end}/${body.length}`,
      "Content-Type": contentType,
    },
  });
}

function streamsOf(track: SyntheticTrack) {
  const gapless = track.gapless && {
    sampleRate: 44100,
    encoderDelaySamples: track.gapless.delaySamples,
    encoderPaddingSamples: track.gapless.paddingSamples,
  };
  return [{ kind: "direct-play", url: syntheticStreamURL(track), ...(gapless ? { gapless } : {}) }];
}

/**
 * Serve `tracks` as one album: each track's Media read credits the album and
 * the artist, its playback-info is an audio direct play, and the server issues
 * the following track as `next` (with its stream, as the server pre-mints it).
 */
export async function routeSyntheticAlbum(
  page: Page,
  tracks: SyntheticTrack[] = TEST_TRACKS,
): Promise<void> {
  const byID = new Map(tracks.map((track) => [track.id, track]));
  const credits = [
    {
      entityID: TEST_ALBUM.id,
      title: TEST_ALBUM.title,
      kind: "album",
      archetype: "container",
      relation: "part-of",
    },
    {
      entityID: TEST_ARTIST.id,
      title: TEST_ARTIST.title,
      kind: "artist",
      archetype: "person",
      relation: "by",
    },
  ];

  await page.route("**/api/graphql", async (route) => {
    const body = route.request().postDataJSON() as {
      query?: string;
      variables?: { id?: string };
    } | null;
    const track = byID.get(body?.variables?.id ?? "");
    if (!body?.query?.startsWith("query Media(") || !track) return route.fallback();
    await route.fulfill({
      json: { data: { media: { id: track.id, title: track.title, artworkSlots: {}, credits } } },
    });
  });

  await page.route("**/api/playback-info", async (route) => {
    const body = route.request().postDataJSON() as { mediaID?: string } | null;
    const index = tracks.findIndex((track) => track.id === body?.mediaID);
    if (index < 0) return route.fallback();
    const following = tracks[index + 1];
    await route.fulfill({
      json: {
        mediaClass: "audio",
        decision: "direct-play",
        streams: streamsOf(tracks[index]),
        next: following
          ? { mediaID: following.id, mediaClass: "audio", streams: streamsOf(following) }
          : null,
      },
    });
  });

  await page.route("**/api/e2e/synthetic/*.wav", (route) => {
    const id = new URL(route.request().url()).pathname
      .split("/")
      .pop()
      ?.replace(/\.wav$/, "");
    const track = byID.get(id ?? "");
    if (!track) return route.fulfill({ status: 404 });
    return fulfilBytes(route, syntheticToneWAV(track), "audio/wav");
  });
}

// Refuses media playback until the first pointer press, so a browser that
// allows autoplay cannot start before the spec's gesture on Play (or race with it).
export function blockAutoplay() {
  const play = HTMLMediaElement.prototype.play;
  let activated = false;
  document.addEventListener(
    "pointerdown",
    () => {
      activated = true;
    },
    true,
  );
  HTMLMediaElement.prototype.play = function (this: HTMLMediaElement) {
    if (activated) return play.call(this);
    return Promise.reject(new DOMException("No gesture yet", "NotAllowedError"));
  };
}
