import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, utimesSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ADMIN_PASSWORD, graphqlWithSeedManager } from "./harness";

// A small synthetic audio library for browser specs that play audio Media
// (CORE-1640). The tracks are generated sine tones, tagged so the Music plugin
// files them as one album by one artist, and scanned into a real Music-plugin
// Library on the self-booted harness server. Synthetic names only.

export interface AudioAlbumSpec {
  artist: string;
  album: string;
  /** Track titles in track-number order. */
  tracks: readonly string[];
  /** Length of each track in seconds; short so a spec can play through. */
  seconds: number;
}

export const TEST_ALBUM: AudioAlbumSpec = {
  artist: "Test Artist",
  album: "Test Album",
  tracks: ["Track One", "Track Two", "Track Three"],
  seconds: 2,
};

export interface SeededAudioTrack {
  id: string;
  title: string;
}

export interface SeededAudioLibrary {
  libraryID: string;
  root: string;
  /** The scanned tracks, in track-number order. */
  tracks: SeededAudioTrack[];
  /** The album Entity (archetype "container") the Music plugin filed the tracks under. */
  albumEntityID: string;
}

/** Writes the album's tracks as tagged MP3 sine tones under `root`. Needs ffmpeg on PATH. */
export function writeAudioAlbum(root: string, spec: AudioAlbumSpec): void {
  const dir = join(root, spec.artist, spec.album);
  mkdirSync(dir, { recursive: true });
  spec.tracks.forEach((title, index) => {
    const number = index + 1;
    const file = join(dir, `${String(number).padStart(2, "0")} ${title}.mp3`);
    const result = spawnSync(
      "ffmpeg",
      [
        "-hide_banner",
        "-loglevel",
        "error",
        "-y",
        "-f",
        "lavfi",
        "-i",
        `sine=frequency=${330 + 110 * index}:duration=${spec.seconds}`,
        "-ac",
        "1",
        "-c:a",
        "libmp3lame",
        "-b:a",
        "64k",
        "-id3v2_version",
        "3",
        "-metadata",
        `title=${title}`,
        "-metadata",
        `artist=${spec.artist}`,
        "-metadata",
        `album_artist=${spec.artist}`,
        "-metadata",
        `album=${spec.album}`,
        "-metadata",
        `track=${number}/${spec.tracks.length}`,
        file,
      ],
      { encoding: "utf8" },
    );
    if (result.error || result.status !== 0) {
      throw new Error(
        `ffmpeg could not write ${file} (is ffmpeg installed?): ${
          result.error?.message ?? result.stderr
        }`,
      );
    }
    // The scanner skips files modified in the last few seconds as still being
    // written (scan.stabilityWindow); a finished fixture is older than that.
    const settled = new Date(Date.now() - 60 * 60_000);
    utimesSync(file, settled, settled);
  });
}

interface SourceItemRow {
  id: string;
  title: string;
}

async function libraryMedia(baseURL: string, libraryID: string): Promise<SourceItemRow[]> {
  const data = (await graphqlWithSeedManager(
    baseURL,
    'query L($libraryID: String) { sourceItems(kind: "all-items", libraryID: $libraryID, limit: 50) { id title } }',
    { libraryID },
  )) as { sourceItems?: SourceItemRow[] };
  return data.sourceItems ?? [];
}

async function albumEntityOf(baseURL: string, trackID: string): Promise<string | undefined> {
  const data = (await graphqlWithSeedManager(
    baseURL,
    "query M($id: String!) { media(id: $id) { credits { entityID archetype } } }",
    { id: trackID },
  )) as { media?: { credits?: { entityID: string; archetype: string }[] } };
  return data.media?.credits?.find((credit) => credit.archetype === "container")?.entityID;
}

async function openSeedAdminSession(baseURL: string): Promise<void> {
  // Library changes need an Admin Session; the bootstrap one global-setup
  // opened may have lapsed by the time a late project runs.
  await graphqlWithSeedManager(
    baseURL,
    "mutation V($password: String!) { verifyAdminSession(password: $password) { active } }",
    { password: ADMIN_PASSWORD },
  );
}

/**
 * Generates `spec` as audio files, creates a Music-plugin Library over them,
 * scans it and waits until every track is listed under its tagged title and
 * filed under an album Entity.
 * Only for the self-booted harness: the server reads the files from this host.
 */
export async function seedAudioLibrary(
  baseURL: string,
  spec: AudioAlbumSpec = TEST_ALBUM,
  timeoutMs = 60_000,
): Promise<SeededAudioLibrary> {
  const root = mkdtempSync(join(tmpdir(), "beamfall-e2e-audio-"));
  try {
    writeAudioAlbum(root, spec);
    await openSeedAdminSession(baseURL);
    const created = (await graphqlWithSeedManager(
      baseURL,
      "mutation C($title: String!, $pluginID: String, $roots: [String!]) { createLibrary(title: $title, pluginID: $pluginID, roots: $roots) { id } }",
      { title: `${spec.album} Library`, pluginID: "music", roots: [root] },
    )) as { createLibrary?: { id?: string } };
    const libraryID = created.createLibrary?.id;
    if (!libraryID) throw new Error(`createLibrary returned ${JSON.stringify(created)}`);
    await graphqlWithSeedManager(
      baseURL,
      "mutation S($libraryID: String!, $root: String!) { scanLibrary(libraryID: $libraryID, root: $root) { id status } }",
      { libraryID, root },
    );

    const deadline = Date.now() + timeoutMs;
    let rows: SourceItemRow[] = [];
    while (Date.now() < deadline) {
      rows = await libraryMedia(baseURL, libraryID);
      const found = spec.tracks.map((title) => rows.find((row) => row.title === title));
      if (found.every((row): row is SourceItemRow => row !== undefined)) {
        const albumEntityID = await albumEntityOf(baseURL, found[0].id);
        if (albumEntityID) {
          const tracks = found.map(({ id, title }) => ({ id, title }));
          return { libraryID, root, tracks, albumEntityID };
        }
      }
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
    const jobs = await graphqlWithSeedManager(
      baseURL,
      "query Jobs { jobs { id kind status error } }",
      {},
    ).catch((err: unknown) => String(err));
    throw new Error(
      `audio library ${libraryID} never listed ${spec.tracks.join(", ")}; saw ${JSON.stringify(rows)}; jobs ${JSON.stringify(jobs)}`,
    );
  } catch (err) {
    rmSync(root, { recursive: true, force: true });
    throw err;
  }
}

/** Soft-deletes the seeded Library (it leaves every store query) and removes its files. */
export async function removeAudioLibrary(
  baseURL: string,
  library: SeededAudioLibrary,
): Promise<void> {
  try {
    await openSeedAdminSession(baseURL);
    await graphqlWithSeedManager(
      baseURL,
      "mutation D($libraryID: String!) { deleteLibrary(libraryID: $libraryID) }",
      { libraryID: library.libraryID },
    );
  } finally {
    rmSync(library.root, { recursive: true, force: true });
  }
}
