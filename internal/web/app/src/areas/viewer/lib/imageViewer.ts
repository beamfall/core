import { graphql, postJSON } from "@/lib/api";
import { probeCapabilityProfile } from "./capabilityProfile";
import type { PlaybackInfo } from "./playback";
import type { PlayQueueItem } from "./watchQueue";

// Data helpers for the web image viewer (/view/:address, CORE-0542).

const SET_FAVORITE_MUTATION =
  "mutation SetFavorite($mediaID: String!, $favorite: Boolean!) { setFavorite(mediaID: $mediaID, favorite: $favorite) { id favorited } }";

/** Sets the acting Profile's favourite for one Media and returns the stored state. */
export async function setMediaFavorite(mediaID: string, favorite: boolean): Promise<boolean> {
  const data = await graphql<{ setFavorite: { id: string; favorited: boolean } }>(
    SET_FAVORITE_MUTATION,
    { mediaID, favorite },
  );
  return data.setFavorite.favorited;
}

/**
 * The URL of an item's bytes for the viewer: the direct stream playback-info
 * issues (a Stream Token-gated URL, valid for an hour), or "" when the server
 * has none to serve. An image takes its original from the direct-play route;
 * a clip (CORE-2977) also takes a direct stream, but never an HLS transcode,
 * which the viewer's plain video element does not play. This deliberately
 * skips fetchPlaybackInfo, which also steers the Play all queue a docked
 * player may be running.
 */
export async function fetchDirectStream(mediaID: string, mediaClass = "image"): Promise<string> {
  const info = await postJSON<PlaybackInfo>("/api/playback-info", {
    mediaID,
    capabilityProfile: await probeCapabilityProfile(),
  });
  if (info.mediaClass && info.mediaClass !== mediaClass) return "";
  const kinds = isClipClass(mediaClass) ? ["direct-play", "direct-stream"] : ["direct-play"];
  const stream = info.streams?.find(
    (candidate) =>
      !!candidate.url &&
      kinds.includes(candidate.kind ?? "") &&
      !candidate.url.split("?")[0].endsWith(".m3u8"),
  );
  return stream?.url ?? "";
}

/** True for a Media the server classed as an image. */
export function isImageClass(mediaClass: string | undefined): boolean {
  return mediaClass === "image";
}

/** True for a Media the server classed as video: in a photo queue, a camera-roll clip. */
export function isClipClass(mediaClass: string | undefined): boolean {
  return mediaClass === "video";
}

/**
 * The items the viewer steps through: images and clips of the queue it was
 * opened with, minus any trashed in this visit. An item of another known class
 * (a track, say) is stepped over; an item whose class is unknown is kept,
 * since a queue listed from a Photos source may not carry classes and the
 * viewer redirects or steps over a non-viewable item on arrival. The current
 * item (keep) stays whatever its class, so an item that turns out not to be
 * viewable still has neighbours to step on to.
 */
export function viewerItems(
  queue: readonly PlayQueueItem[] | undefined,
  removed?: ReadonlySet<string>,
  keep?: string,
): PlayQueueItem[] {
  return (queue ?? []).filter(
    (item) =>
      !removed?.has(item.id) &&
      (item.id === keep ||
        !item.mediaClass ||
        isImageClass(item.mediaClass) ||
        isClipClass(item.mediaClass)),
  );
}

/** The viewer's neighbours of id: the items either side, its 1-based position and the count. */
export function viewerNeighbours(
  queue: readonly PlayQueueItem[] | undefined,
  id: string,
  removed?: ReadonlySet<string>,
): {
  items: PlayQueueItem[];
  index: number;
  previous?: PlayQueueItem;
  next?: PlayQueueItem;
  position: number;
  total: number;
} {
  const items = viewerItems(queue, removed, id);
  const index = items.findIndex((item) => item.id === id);
  if (index < 0) return { items, index, position: 0, total: 0 };
  return {
    items,
    index,
    previous: items[index - 1],
    next: items[index + 1],
    position: index + 1,
    total: items.length,
  };
}

/** How many neighbours the filmstrip shows on each side of the current item. */
export const FILMSTRIP_RADIUS = 7;

/** The slice of items the filmstrip draws around index (the current item). */
export function filmstripWindow<T>(
  items: readonly T[],
  index: number,
  radius = FILMSTRIP_RADIUS,
): T[] {
  if (index < 0 || items.length < 2) return [];
  return items.slice(Math.max(0, index - radius), index + radius + 1);
}

/**
 * Where the viewer goes after trashing items[index]: the next item, else the
 * previous one, else nowhere (the viewer closes).
 */
export function trashStepTarget(
  items: readonly PlayQueueItem[],
  index: number,
): { target: PlayQueueItem; direction: "previous" | "next" } | null {
  if (index < 0) return null;
  if (items[index + 1]) return { target: items[index + 1], direction: "next" };
  if (index > 0 && items[index - 1]) return { target: items[index - 1], direction: "previous" };
  return null;
}

const TRASH_MEDIA_MUTATION =
  "mutation TrashMedia($mediaID: String!) { trashMedia(mediaID: $mediaID) { id } }";

/** Moves one Media to the server trash through the audited trashMedia mutation (manager role). */
export async function trashMedia(mediaID: string): Promise<void> {
  await graphql<{ trashMedia: unknown }>(TRASH_MEDIA_MUTATION, { mediaID });
}

const VIEWER_THUMB_QUERY =
  "query ViewerThumb($id: String!) { media(id: $id) { id mediaClass durationSeconds artworkSlots } }";

/** What the filmstrip draws for one item. */
export interface ViewerThumb {
  /** The poster (else thumb) slot URL, or "" for none. */
  artwork: string;
  mediaClass?: string;
  durationSeconds?: number;
}

/** One filmstrip item's artwork and class, or null when it is unknown or hidden. */
export async function fetchViewerThumb(
  id: string,
  signal?: AbortSignal,
): Promise<ViewerThumb | null> {
  const data = await graphql<{
    media: {
      mediaClass?: string;
      durationSeconds?: number;
      artworkSlots?: Record<string, string>;
    } | null;
  }>(VIEWER_THUMB_QUERY, { id }, signal).catch((err) => {
    if (signal?.aborted) throw err;
    return { media: null };
  });
  const media = data.media;
  if (!media) return null;
  const slots = media.artworkSlots ?? {};
  return {
    artwork: slots.poster || slots.thumb || "",
    mediaClass: media.mediaClass || undefined,
    durationSeconds: media.durationSeconds || undefined,
  };
}

/** Zoom steps the viewer moves through; the first is the fitted image. */
export const ZOOM_STEPS = [1, 2, 4] as const;

/** The next zoom step after scale, wrapping back to fit after the largest. */
export function nextZoom(scale: number): number {
  const next = ZOOM_STEPS.find((step) => step > scale + 0.001);
  return next ?? 1;
}

/** Clamps a wheel- or pinch-driven scale to the viewer's range. */
export function clampZoom(scale: number): number {
  return Math.min(ZOOM_STEPS[ZOOM_STEPS.length - 1], Math.max(1, scale));
}
