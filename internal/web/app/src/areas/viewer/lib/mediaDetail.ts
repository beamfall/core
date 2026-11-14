import { ApiError, graphql } from "@/lib/api";
import type { EntityFieldInfo, GenericMediaInfo, Section, SourceItem } from "@/lib/graphql/types";
import { viewerCacheEntries } from "@/lib/viewerCache";

// Data for the Media detail page (/media/:mediaId, CORE-1144).
//
// The server's media(id) carries no artwork and no playback position, so the
// page paints those only from the card that opened it. The card travels in
// navigation state bound to the identity that rendered it, as entity previews
// do (entityPreview.ts): a Back after relock or a Profile switch must not show
// another identity's item.

const MEDIA_QUERY =
  "query Media($id: String!) { media(id: $id) { id title summary year favorited } }";
const MEDIA_FIELDS_QUERY =
  "query MediaFields($mediaID: String!) { mediaFields(mediaID: $mediaID) { name type value label } }";

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

function isUnknownMedia(err: unknown): boolean {
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

// Mirrors sectionCacheKey (HydratedSection.tsx): "section:" + JSON of
// [section, libraryID, entityID, extraFilters].
const SECTION_CACHE_PREFIX = "section:";

export interface CachedSiblings {
  section: Section;
  items: SourceItem[];
}

/**
 * The other items of a hydrated section that holds this item, from the viewer
 * cache only (no request). The section the card came from wins; otherwise the
 * first cached section holding the item. Hero sections are skipped: they are a
 * promotion, not a set.
 */
export function findCachedSiblings(
  identity: string | null,
  mediaID: string,
  sectionID?: string,
): CachedSiblings | null {
  let first: CachedSiblings | null = null;
  for (const [key, value] of viewerCacheEntries(identity)) {
    if (!key.startsWith(SECTION_CACHE_PREFIX) || !Array.isArray(value)) continue;
    const items = value as SourceItem[];
    if (!items.some((item) => item?.id === mediaID)) continue;
    const section = parseSection(key.slice(SECTION_CACHE_PREFIX.length));
    if (!section || section.kind === "hero") continue;
    const others = items.filter((item) => item?.id !== mediaID);
    if (others.length === 0) continue;
    const match = { section, items: others };
    if (sectionID && section.id === sectionID) return match;
    first ??= match;
  }
  return first;
}

function parseSection(serialized: string): Section | null {
  try {
    const parts: unknown = JSON.parse(serialized);
    const section = Array.isArray(parts) ? parts[0] : null;
    if (!section || typeof section !== "object") return null;
    const candidate = section as Partial<Section>;
    return typeof candidate.id === "string" && typeof candidate.kind === "string"
      ? (candidate as Section)
      : null;
  } catch {
    return null;
  }
}
