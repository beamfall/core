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
 * The URL of an image's original bytes: the direct stream playback-info
 * issues for image-class Media (a Stream Token-gated URL, valid for an hour),
 * or "" when the server has none to serve. This deliberately skips
 * fetchPlaybackInfo, which also steers the Play all queue a docked player may
 * be running.
 */
export async function fetchImageOriginal(mediaID: string): Promise<string> {
  const info = await postJSON<PlaybackInfo>("/api/playback-info", {
    mediaID,
    capabilityProfile: await probeCapabilityProfile(),
  });
  if (info.mediaClass && !isImageClass(info.mediaClass)) return "";
  return info.streams?.find((stream) => stream.kind === "direct-play")?.url ?? "";
}

/** True for a Media the server classed as an image. */
export function isImageClass(mediaClass: string | undefined): boolean {
  return mediaClass === "image";
}

/**
 * The image neighbours of id in the queue the viewer was opened with. Items of
 * another known class (a camera-roll clip, say) are stepped over; an item
 * whose class is unknown is kept, since a queue listed from a Photos source
 * may not carry classes and the viewer redirects a non-image on arrival.
 */
export function imageNeighbours(
  queue: readonly PlayQueueItem[] | undefined,
  id: string,
): { previous?: PlayQueueItem; next?: PlayQueueItem; position: number; total: number } {
  const images = (queue ?? []).filter((item) => !item.mediaClass || isImageClass(item.mediaClass));
  const index = images.findIndex((item) => item.id === id);
  if (index < 0) return { position: 0, total: 0 };
  return {
    previous: images[index - 1],
    next: images[index + 1],
    position: index + 1,
    total: images.length,
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
