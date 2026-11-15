import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync } from "node:fs";
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
}

export const TEST_ALBUM = { id: "entity-test-album", title: "Test Album" };
export const TEST_ARTIST = { id: "entity-test-artist", title: "Test Artist" };

export const TEST_TRACKS: SyntheticTrack[] = [
  { id: "media-track-one", title: "Track One", frequency: 440, seconds: 30 },
  { id: "media-track-two", title: "Track Two", frequency: 660, seconds: 30 },
];

const cacheDir = join(tmpdir(), "beamfall-e2e-synthetic-audio");

/** A mono 44.1 kHz WAV of the track's tone, rendered once per machine and cached. */
export function syntheticToneWAV(track: Pick<SyntheticTrack, "frequency" | "seconds">): Buffer {
  mkdirSync(cacheDir, { recursive: true });
  const file = join(cacheDir, `sine-${track.frequency}hz-${track.seconds}s.wav`);
  if (!existsSync(file)) {
    execFileSync("ffmpeg", [
      "-hide_banner",
      "-loglevel",
      "error",
      "-y",
      "-f",
      "lavfi",
      "-i",
      `sine=frequency=${track.frequency}:sample_rate=44100:duration=${track.seconds}`,
      "-ac",
      "1",
      "-c:a",
      "pcm_s16le",
      file,
    ]);
  }
  return readFileSync(file);
}

/** The URL a track's stream is served at. */
export function syntheticStreamURL(track: SyntheticTrack): string {
  return `/api/e2e/synthetic/${track.id}.wav`;
}

function fulfilBytes(route: Route, body: Buffer, contentType: string) {
  const range = route.request().headers().range;
  if (!range) return route.fulfill({ body, contentType, headers: { "Accept-Ranges": "bytes" } });
  const [startText, endText] = range.replace("bytes=", "").split("-");
  const start = Number(startText);
  const end = endText ? Math.min(Number(endText), body.length - 1) : body.length - 1;
  return route.fulfill({
    status: 206,
    body: body.subarray(start, end + 1),
    headers: {
      "Accept-Ranges": "bytes",
      "Content-Range": `bytes ${start}-${end}/${body.length}`,
      "Content-Type": contentType,
    },
  });
}

function streamsOf(track: SyntheticTrack) {
  return [{ kind: "direct-play", url: syntheticStreamURL(track) }];
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
