import { ApiError, graphql } from "@/lib/api";
import type { EntityFieldInfo, GenericMediaInfo, SourceItem } from "@/lib/graphql/types";
import type { PlaybackAudioTrack, PlaybackCaptionTrack } from "./playback";

// Data for the Media detail page (/media/:mediaId, CORE-1144).
//
// The server's media(id) carries the item's artwork slots and credits but no
// playback position, so the page paints progress only from the card that
// opened it. The card travels in navigation state bound to the identity that
// rendered it, as entity previews do (entityPreview.ts): a Back after relock or a Profile switch must not show
// another identity's item.

const MEDIA_QUERY =
  "query Media($id: String!) { media(id: $id) { id title summary year durationSeconds mediaClass playsAlone placeholderIcon libraryID favorited artworkSlots sequence index headline classLabel videoWidth videoHeight credits { entityID title kind kindDisplayName kindPluralDisplayName archetype relation attributes artworkSlots } } }";
const MEDIA_FIELDS_QUERY =
  "query MediaFields($mediaID: String!) { mediaFields(mediaID: $mediaID) { name type value label } }";

const MEDIA_RELATIONS_QUERY =
  "query MediaRelations($mediaID: String!, $relation: String!) { mediaRelations(mediaID: $mediaID, relation: $relation) { locked media { id title year durationSeconds mediaClass artworkSlots } } }";

const MEDIA_TRACKS_QUERY =
  "query MediaTracks($mediaID: String!) { mediaTracks(mediaID: $mediaID) { versions { id label videoCodec hdr resolution audioTracks { streamIndex codec language channels default commentary visualImpaired } captionTracks { id format streamIndex language forced sdh visualImpaired commentary title } } } }";

const MEDIA_LYRICS_QUERY =
  "query MediaLyrics($mediaID: String!) { mediaLyrics(mediaID: $mediaID) { synced source lines { startMs text } } }";

/** The server answer for an id that is missing or hidden from this Profile (no oracle). */
const UNKNOWN_MEDIA = "unknown media";

export interface MediaDetail {
  media: GenericMediaInfo;
  fields: EntityFieldInfo[];
}

/** The detail-page cache key in the identity-scoped viewer cache. */
export const mediaDetailCacheKey = (id: string) => `media-detail:${id}`;

/** One Media item, or null when the server reports it unknown or not visible. */
export async function fetchMedia(
  id: string,
  signal?: AbortSignal,
): Promise<GenericMediaInfo | null> {
  try {
    const data = await graphql<{ media: GenericMediaInfo | null }>(MEDIA_QUERY, { id }, signal);
    return data.media ?? null;
  } catch (err) {
    if (isUnknownMedia(err)) return null;
    throw err;
  }
}

/** The item's plugin-declared fields, each with its display label when declared. */
export async function fetchMediaDetailFields(
  mediaID: string,
  signal?: AbortSignal,
): Promise<EntityFieldInfo[]> {
  const data = await graphql<{ mediaFields: EntityFieldInfo[] | null }>(
    MEDIA_FIELDS_QUERY,
    { mediaID },
    signal,
  );
  return data.mediaFields ?? [];
}

interface RelatedMedia {
  id: string;
  title: string;
  year?: number;
  durationSeconds?: number;
  mediaClass?: string;
  artworkSlots?: Record<string, string>;
}

/**
 * The Media related to this item by a stored relation, as cards (CORE-2439):
 * "extra-of" lists its Extras, "edition-of" its other Editions. The server
 * filters by this Profile's reveal level and orders by title. An item a locked
 * certification ceiling blocks stays listed but carries `locked`, so the row's
 * play queue steps over it.
 */
export async function fetchMediaRelations(
  mediaID: string,
  relation: "extra-of" | "edition-of",
  signal?: AbortSignal,
): Promise<SourceItem[]> {
  const data = await graphql<{
    mediaRelations: { media: RelatedMedia; locked?: boolean }[] | null;
  }>(MEDIA_RELATIONS_QUERY, { mediaID, relation }, signal);
  return (data.mediaRelations ?? [])
    .filter((row) => row.media?.id && row.media.id !== mediaID)
    .map(({ media, locked }) => ({
      kind: "media",
      id: media.id,
      title: media.title,
      year: media.year,
      durationSeconds: media.durationSeconds,
      mediaClass: media.mediaClass,
      artworkSlots: media.artworkSlots,
      ...(locked ? { locked: true } : {}),
    }));
}

/**
 * One Version of a Media with the tracks its primary file holds (CORE-2442).
 * Ids are the ones playbackInfo reports for the same file, so a pick made from
 * this list names the same Version and caption track at playback start.
 */
export interface MediaTrackVersion {
  id: string;
  label: string;
  videoCodec?: string;
  hdr?: string;
  /** "8K" | "4K" | "1080p" | "720p" | "SD"; absent when the frame size is unknown (CORE-2485). */
  resolution?: string;
  audioTracks: PlaybackAudioTrack[];
  captionTracks: PlaybackCaptionTrack[];
}

/** The item's Versions and their tracks, read from stored inventory without a playback session. */
export async function fetchMediaTracks(
  mediaID: string,
  signal?: AbortSignal,
): Promise<MediaTrackVersion[]> {
  const data = await graphql<{ mediaTracks: { versions: MediaTrackVersion[] | null } | null }>(
    MEDIA_TRACKS_QUERY,
    { mediaID },
    signal,
  );
  return (data.mediaTracks?.versions ?? []).map((version) => ({
    ...version,
    audioTracks: version.audioTracks ?? [],
    captionTracks: version.captionTracks ?? [],
  }));
}

/** One lyric line; `startMs` is set only when the lyrics are synced. */
export interface MediaLyricLine {
  startMs?: number | null;
  text: string;
}

/** An audio item's lyrics: synced lines carry ascending start times. */
export interface MediaLyrics {
  synced: boolean;
  source: string;
  lines: MediaLyricLine[];
}

/** The item's lyrics from its local sources (CORE-1638); no lines when it has none. */
export async function fetchMediaLyrics(
  mediaID: string,
  signal?: AbortSignal,
): Promise<MediaLyrics> {
  const data = await graphql<{ mediaLyrics: MediaLyrics | null }>(
    MEDIA_LYRICS_QUERY,
    { mediaID },
    signal,
  );
  const lyrics = data.mediaLyrics;
  return {
    synced: !!lyrics?.synced,
    source: lyrics?.source ?? "",
    lines: lyrics?.lines ?? [],
  };
}

export function isUnknownMedia(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    err.status === 200 &&
    err.message.toLowerCase().includes(UNKNOWN_MEDIA)
  );
}

interface MediaLinkState {
  mediaPreview?: {
    mediaID?: unknown;
    identity?: unknown;
    sectionID?: unknown;
    item?: unknown;
  };
}

export interface MediaPreview {
  item: SourceItem;
  /** The section the card was drawn in, when known: the source of the "more from" shelf. */
  sectionID?: string;
}

export interface FeedEntryPreview {
  mediaID: string;
  subscriptionID?: string;
  showTitle?: string;
  showPath?: string;
  availability: string;
  downloadState: string;
}

interface FeedEntryLinkState {
  feedEntryPreview?: {
    identity?: unknown;
    entry?: unknown;
  };
}

export function feedEntryLinkState(
  entry: FeedEntryPreview,
  identity: string | null | undefined,
): FeedEntryLinkState | undefined {
  if (!identity) return undefined;
  return { feedEntryPreview: { identity, entry } };
}

export function readFeedEntryPreview(
  state: unknown,
  identity: string | null | undefined,
  mediaID: string,
): FeedEntryPreview | null {
  if (!identity || !state || typeof state !== "object") return null;
  const raw = (state as FeedEntryLinkState).feedEntryPreview;
  if (!raw || raw.identity !== identity || !raw.entry || typeof raw.entry !== "object") return null;
  const entry = raw.entry as Partial<FeedEntryPreview>;
  if (entry.mediaID !== mediaID || typeof entry.availability !== "string") return null;
  return {
    mediaID,
    subscriptionID: typeof entry.subscriptionID === "string" ? entry.subscriptionID : undefined,
    showTitle: typeof entry.showTitle === "string" ? entry.showTitle : undefined,
    showPath: typeof entry.showPath === "string" ? entry.showPath : undefined,
    availability: entry.availability,
    downloadState: typeof entry.downloadState === "string" ? entry.downloadState : "none",
  };
}

/** Builds the navigation state for a link to /media/:id (see MediaDetailPage). */
export function mediaLinkState(
  item: SourceItem,
  identity: string | null,
  sectionID?: string,
): MediaLinkState | undefined {
  if (!identity) return undefined;
  return { mediaPreview: { mediaID: item.id, identity, sectionID, item } };
}

/** The card carried by navigation state, only when it matches this item and identity. */
export function readMediaPreview(
  state: unknown,
  identity: string | null,
  mediaID: string,
): MediaPreview | null {
  if (!identity || !state || typeof state !== "object") return null;
  const preview = (state as MediaLinkState).mediaPreview;
  if (!preview || preview.identity !== identity || preview.mediaID !== mediaID) return null;
  const item = preview.item as Partial<SourceItem> | null | undefined;
  if (!item || typeof item !== "object" || item.id !== mediaID) return null;
  if (typeof item.title !== "string" || !item.title) return null;
  return {
    item: item as SourceItem,
    sectionID:
      typeof preview.sectionID === "string" && preview.sectionID ? preview.sectionID : undefined,
  };
}

export interface ResumeProgress {
  positionSeconds: number;
  durationSeconds: number;
  /** Whole percent played, kept within 1..99 so a started item never reads 0 or 100. */
  percent: number;
}

// A card may carry the viewer's position for the item (an optional engagement
// object on newer servers). Read it defensively: absent, partial, completed or
// out-of-range values all mean "no resume point known".
export function resumeProgress(item: SourceItem | null | undefined): ResumeProgress | null {
  const engagement = (item as { engagement?: unknown } | null | undefined)?.engagement;
  if (!engagement || typeof engagement !== "object") return null;
  const { positionSeconds, durationSeconds, completed } = engagement as Record<string, unknown>;
  if (completed === true) return null;
  if (typeof positionSeconds !== "number" || typeof durationSeconds !== "number") return null;
  if (!(positionSeconds > 0) || !(durationSeconds > 0) || positionSeconds >= durationSeconds) {
    return null;
  }
  const percent = Math.min(99, Math.max(1, Math.round((positionSeconds / durationSeconds) * 100)));
  return { positionSeconds, durationSeconds, percent };
}
