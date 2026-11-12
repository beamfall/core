import { ApiError, postJSON } from "@/lib/api";
import { probeCapabilityProfile } from "./capabilityProfile";
import { nextInPlayQueue, playQueueHandle, playQueueTitle } from "./playQueue";

// Playback-info request + its wire shape (ADR-0010 playbackInfo contract).
//
// The endpoint is POST /api/playback-info (NOT a GraphQL op — it negotiates a
// Capability Profile and mints a Stream Token, so it rides the REST surface).
// The request body is copied verbatim from the vanilla app
// (internal/web/static/app.js play()). The response is consumed field-by-field
// by the player; only the fields the web player reads are typed here — the
// server reports more, and unknown fields are ignored.

/** One selectable rendition on a transcode stream (ADR-0010 honest ABR). */
export interface PlaybackRendition {
  id?: string | number;
  height?: number;
  bandwidth?: number;
}

/** One playback route the server offers for the Media (ADR-0010 stream inventory). */
export interface PlaybackStream {
  /** "direct-play" | "direct-stream" | "transcode" — also surfaced as info.decision. */
  kind?: string;
  /** The bytes URL: a direct /api/media/... path, or an HLS .m3u8 for transcode. */
  url?: string;
  renditions?: PlaybackRendition[];
}

/** The canonical resume timeline (ADR-0010): seconds into the Media. */
export interface PlaybackTimeline {
  positionSeconds?: number;
}

/** Stream-Token-served Generated Assets attached to a playback (ADR-0033). */
export interface PlaybackAssets {
  /** Trickplay sprite-sheet manifest URL (resolved relative for sheet tiles). */
  trickplay?: string;
  /** Silent sampled MP4 preview clip URL for hover/focus preview surfaces. */
  previewClip?: string;
  /** ISO expiry of the asset Stream Token; an expired token suppresses trickplay. */
  expiresAt?: string;
  /** Optional bar/phrase timing hints composed with beatgrid.json by clients. */
  syncLanes?: string;
}

/** The "Up Next" issuance the server pre-mints for the player (ADR-0010 next-item). */
export interface PlaybackNext {
  mediaID: string;
  mediaClass?: string;
  title?: string;
  streams?: PlaybackStream[];
  timeline?: PlaybackTimeline;
  assets?: PlaybackAssets;
  trackPreferences?: unknown;
  next?: PlaybackNext | null;
}

/** The /api/playback-info response (ADR-0010 playbackInfo), narrowed to what the player reads. */
export interface PlaybackInfo {
  /** "audio" | "video": the item's playback class; absent when unavailable. */
  mediaClass?: string;
  /** The route the server chose; falls back to streams[0].kind when absent. */
  decision?: string;
  streams?: PlaybackStream[];
  timeline?: PlaybackTimeline;
  assets?: PlaybackAssets;
  /**
   * A machine token explaining the route: why it transcodes ("requires-transcode",
   * …) or why there is none ("media-file-unavailable", "media-unavailable").
   */
  reason?: string;
  next?: PlaybackNext | null;
  trackPreferences?: unknown;
}

/** The /api/preview-info response: Generated Asset URLs only, no playback streams. */
export interface PreviewInfo {
  mediaID?: string;
  assets?: PlaybackAssets;
}

/** The HLS stream of an inventory (the manual-rendition pin offered beside direct play). */
export function hlsStreamOf(streams: PlaybackStream[] | undefined): PlaybackStream | null {
  return (streams || []).find((stream) => stream.kind === "hls" && !!stream.url) || null;
}

/** A reveal-terminal HTTP status (token expired / relocked / hidden) — ADR-0004/0010. */
export function isRevealTerminalStatus(status: number): boolean {
  return status === 401 || status === 403 || status === 410;
}

/**
 * POST /api/playback-info for one Media. Resolves the info document, or throws
 * an {@link ApiError} carrying the HTTP status so the caller can distinguish a
 * reveal-terminal response (a relocked/hidden/expired Media) from a generic
 * failure — exactly the branch the vanilla play() takes on a non-ok response.
 * `nextMediaID` declares the queue's next item so the server pre-mints its
 * stream (info.next); an unservable next item is silently omitted.
 */
export async function fetchPlaybackInfo(
  mediaID: string,
  nextMediaID?: string,
): Promise<PlaybackInfo> {
  // Taken before the probe: a request that resumes after a newer Play all
  // started must not be read as leaving that queue.
  const queue = playQueueHandle();
  const capabilityProfile = await probeCapabilityProfile();
  // A Play all / Shuffle queue names its next item so the server pre-mints
  // Up Next for it (ADR-0029: the queue is client-owned). A catalog card's
  // queue (CORE-1143) names its own next item, which wins; the Play all queue
  // is still asked, so playback started outside it ends that queue.
  const queuedNext = await nextInPlayQueue(mediaID, queue);
  const next = nextMediaID || queuedNext;
  const info = await postJSON<PlaybackInfo>("/api/playback-info", {
    mediaID,
    capabilityProfile,
    ...(next ? { nextMediaID: next } : {}),
  });
  // The server issues a declared next item without a title; the Play all
  // queue kept the one its listing gave, so Up Next and the next heading can
  // name it. A card queue's next item is named by the player from that queue.
  if (info.next && !info.next.title && next === queuedNext && info.next.mediaID === queuedNext) {
    const title = playQueueTitle(queuedNext);
    if (title) info.next.title = title;
  }
  return info;
}

/** The words for a playback-info `reason` that leaves nothing to play. */
export function unavailableReasonMessage(
  reason: string | undefined,
  messages: { mediaFileMissing?: string; mediaNotAvailable?: string },
): string | undefined {
  switch (reason) {
    case "media-file-unavailable":
      return messages.mediaFileMissing;
    case "media-unavailable":
      return messages.mediaNotAvailable;
    default:
      return undefined;
  }
}

/**
 * POST /api/preview-info for one Media card. This deliberately avoids
 * playback negotiation so hover previews only receive media-assets URLs.
 */
export async function fetchPreviewInfo(mediaID: string): Promise<PreviewInfo> {
  return postJSON<PreviewInfo>("/api/preview-info", { mediaID });
}

export { ApiError };
