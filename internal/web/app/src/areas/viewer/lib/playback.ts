import { ApiError, postJSON } from "@/lib/api";
import { probeCapabilityProfile } from "./capabilityProfile";
import { leavePlayQueue, nextInPlayQueue, playQueueHandle, playQueueTitle } from "./playQueue";

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
  /** Marker thumbnail directory URL (ends in `/`); files are `marker-<ms>.jpg`. */
  markerThumbnailBase?: string;
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

/**
 * A pitch shift and tempo change for an audio Media (CORE-1658): whole
 * semitones, and a speed factor where 1 is as recorded. Sent on the request;
 * echoed on the response only when the server applied it.
 */
export interface PlaybackAdjustment {
  pitchShiftSemitones?: number;
  tempo?: number;
}

/**
 * One Version of a Media with two or more (ADR-0009, CORE-1676). `id` is
 * opaque and stable, sent back as `selectedVersion`; `label` "" is the default
 * Version, which the client names.
 */
export interface PlaybackVersion {
  id: string;
  label: string;
  videoCodec?: string;
  hdr?: string;
  selected: boolean;
}

/**
 * One audio stream of the negotiated file (CORE-1709). `streamIndex` is sent
 * back as `selectedAudioIndex` to hear another track; exactly one is
 * `selected`: the track the delivered streams play.
 */
export interface PlaybackAudioTrack {
  streamIndex: number;
  codec: string;
  language?: string;
  channels?: number;
  default?: boolean;
  commentary?: boolean;
  visualImpaired?: boolean;
  selected?: boolean;
}

/**
 * One caption track of the negotiated file: a sidecar (`url` only) or a
 * subtitle stream muxed into the file (`streamIndex`). A muxed text stream
 * also carries a WebVTT `url`; a muxed picture-based stream has none and is
 * shown by burning it into the video (`selectedSubtitleIndex`).
 */
export interface PlaybackCaptionTrack {
  id: string;
  url?: string;
  format: string;
  streamIndex?: number;
  language?: string;
  forced?: boolean;
  sdh?: boolean;
  visualImpaired?: boolean;
  commentary?: boolean;
}

/** The server's subtitle pick at playback start: a language, kind and role ref. */
export interface PlaybackSelectedSubtitle {
  track: {
    language?: string;
    /** "muxed" | "sidecar". */
    kind: string;
    roles?: { forced?: boolean; sdh?: boolean; visualImpaired?: boolean; commentary?: boolean };
  };
  reason?: string;
}

/** The tracks the viewer picked that the server has to deliver (CORE-1709). */
export interface PlaybackTrackChoice {
  /** The audio stream to play, by absolute stream index. */
  selectedAudioIndex?: number;
  /** The picture-based subtitle stream to burn into the video. */
  selectedSubtitleIndex?: number;
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
  /** The adjustment the stream carries; absent when none was applied. */
  adjustment?: PlaybackAdjustment;
  /** The Media's Versions, listed only when it has two or more. */
  versions?: PlaybackVersion[];
  /** Every audio stream of the file; absent when it has none. */
  audioTracks?: PlaybackAudioTrack[];
  /** Muxed and sidecar caption tracks; absent when there are none. */
  captionTracks?: PlaybackCaptionTrack[];
  /** The server's subtitle pick for the start of playback. */
  selectedSubtitleTrack?: PlaybackSelectedSubtitle;
  /** Why subtitles the viewer would want cannot be shown here, when they cannot. */
  subtitlesUnavailableReason?: string;
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
 * `nextMediaID` declares a catalog card queue's next item so the server
 * pre-mints its stream (info.next); an unservable next item is silently
 * omitted. "" means a card queue drives playback and has no next item;
 * undefined means no card queue, so a Play all queue may name the next item.
 * `adjustment` asks for a key or tempo change; the default sends none.
 * `selectedVersion` names the Version the viewer picked; the server ignores
 * an id it does not know and chooses itself. `tracks` names the audio stream
 * and the burned-in subtitle stream the viewer picked (CORE-1709).
 */
export async function fetchPlaybackInfo(
  mediaID: string,
  nextMediaID?: string,
  adjustment?: PlaybackAdjustment,
  selectedVersion?: string,
  tracks?: PlaybackTrackChoice,
): Promise<PlaybackInfo> {
  // Taken before the probe: a request that resumes after a newer Play all
  // started must not be read as leaving that queue.
  const queue = playQueueHandle();
  const capabilityProfile = await probeCapabilityProfile();
  // A Play all / Shuffle queue names its next item so the server pre-mints
  // Up Next for it (ADR-0029: the queue is client-owned). Playback a catalog
  // card's queue drives (CORE-1143) has left the Play all queue, which ends
  // here even when the card queue has nothing after this item.
  let queuedNext = "";
  if (nextMediaID === undefined) queuedNext = await nextInPlayQueue(mediaID, queue);
  else leavePlayQueue(queue);
  const next = nextMediaID || queuedNext;
  const info = await postJSON<PlaybackInfo>("/api/playback-info", {
    mediaID,
    capabilityProfile,
    ...(next ? { nextMediaID: next } : {}),
    ...(isDefaultAdjustment(adjustment)
      ? {}
      : {
          adjustment: {
            pitchShiftSemitones: adjustment?.pitchShiftSemitones ?? 0,
            tempo: adjustment?.tempo ?? 1,
          },
        }),
    ...(selectedVersion ? { selectedVersion } : {}),
    ...(tracks?.selectedAudioIndex !== undefined
      ? { selectedAudioIndex: tracks.selectedAudioIndex }
      : {}),
    ...(tracks?.selectedSubtitleIndex !== undefined
      ? { selectedSubtitleIndex: tracks.selectedSubtitleIndex }
      : {}),
  });
  // The server issues a declared next item without a title; the Play all
  // queue kept the one its listing gave, so Up Next and the next heading can
  // name it. A card queue's next item is named by the player from that queue.
  if (info.next && !info.next.title && queuedNext && info.next.mediaID === queuedNext) {
    const title = playQueueTitle(queuedNext);
    if (title) info.next.title = title;
  }
  return info;
}

/** What a playback message offers: another attempt, or a way back out. */
export type PlaybackMessageAction = "retry" | "back";

/** The copy for each `reason` a playback-info `unavailable` decision can carry. */
export interface UnavailableReasonMessages {
  mediaFileMissing?: string;
  mediaNotAvailable?: string;
  transcodeDisallowed?: string;
  streamLimitReached?: string;
  transcodeCapacityReached?: string;
  dolbyVisionUnavailable?: string;
  hdrToneMapUnavailable?: string;
  hdrToneMapDisabled?: string;
}

// Every reason internal/playback leaves on an `unavailable` decision
// (decide, transcodeRefusal, VideoSignal.FullTranscodeRefusal). A limit the
// viewer can wait out offers Try again; a gate or a missing capability that
// another attempt cannot change offers Back.
export const UNAVAILABLE_REASONS: Record<
  string,
  { message: keyof UnavailableReasonMessages; action: PlaybackMessageAction }
> = {
  "media-file-unavailable": { message: "mediaFileMissing", action: "retry" },
  "media-unavailable": { message: "mediaNotAvailable", action: "back" },
  "transcode-disallowed": { message: "transcodeDisallowed", action: "back" },
  "stream-limit-reached": { message: "streamLimitReached", action: "retry" },
  "transcode-capacity-reached": { message: "transcodeCapacityReached", action: "retry" },
  "dolby-vision-p5-transcode-unavailable": { message: "dolbyVisionUnavailable", action: "back" },
  "hdr-tone-map-unverified": { message: "hdrToneMapUnavailable", action: "back" },
  "hdr-tone-map-disabled": { message: "hdrToneMapDisabled", action: "back" },
};

/**
 * The words and action for a playback-info `reason` that leaves nothing to
 * play. An unknown reason gets no message (the caller's generic copy) and
 * Try again.
 */
export function unavailableReason(
  reason: string | undefined,
  messages: UnavailableReasonMessages,
): { message?: string; action: PlaybackMessageAction } {
  const entry =
    reason && Object.hasOwn(UNAVAILABLE_REASONS, reason) ? UNAVAILABLE_REASONS[reason] : undefined;
  if (!entry) return { action: "retry" };
  return { message: messages[entry.message], action: entry.action };
}

/** The words for a playback-info `reason` that leaves nothing to play. */
export function unavailableReasonMessage(
  reason: string | undefined,
  messages: UnavailableReasonMessages,
): string | undefined {
  return unavailableReason(reason, messages).message;
}

/**
 * The server's own wording for a refused playback-info request: the JSON
 * error body is `{"error": "..."}` (writeErrorCause). Empty when the payload
 * carries none.
 */
export function serverErrorReason(err: unknown): string {
  if (!(err instanceof ApiError) || !err.payload || typeof err.payload !== "object") return "";
  const detail = Reflect.get(err.payload, "error");
  return typeof detail === "string" ? detail.trim() : "";
}

/**
 * POST /api/preview-info for one Media card. This deliberately avoids
 * playback negotiation so hover previews only receive media-assets URLs.
 */
export async function fetchPreviewInfo(mediaID: string): Promise<PreviewInfo> {
  return postJSON<PreviewInfo>("/api/preview-info", { mediaID });
}

export { ApiError };

/** The playback-info `reason` the server leaves on a composed-picture decision (ADR-0057). */
export const COMPOSED_PICTURE_REASON = "composed-picture";

/**
 * Whether an audio item is served as a composed picture (CD+G graphics or lyrics
 * as H.264 video). Mirrors the Apple and Android kits: the decision reason, or,
 * where it is absent (the next-item decision), any stream rendition with a
 * picture height, since a bare audio stream advertises none.
 */
export function isComposedPictureServed(
  info: Pick<PlaybackInfo, "reason" | "streams"> | null | undefined,
): boolean {
  if (info?.reason?.trim() === COMPOSED_PICTURE_REASON) return true;
  return !!info?.streams?.some((s) => s.renditions?.some((r) => (r.height ?? 0) > 0));
}

/** Whether the player surface has a picture: any non-audio item, or a composed-picture audio one. */
export function hasPicture(info: PlaybackInfo | null | undefined): boolean {
  return info?.mediaClass !== "audio" || isComposedPictureServed(info);
}

/** Whether an adjustment changes nothing: no pitch shift and the recorded tempo. */
export function isDefaultAdjustment(adjustment: PlaybackAdjustment | null | undefined): boolean {
  return (adjustment?.pitchShiftSemitones ?? 0) === 0 && appliedTempoOf(adjustment) === 1;
}

/** The tempo factor of an adjustment: 1 when absent or 0 (the wire default). */
function appliedTempoOf(adjustment: PlaybackAdjustment | null | undefined): number {
  const tempo = adjustment?.tempo ?? 0;
  return Number.isFinite(tempo) && tempo > 0 ? tempo : 1;
}

/**
 * Song seconds per stream second: the tempo the server applied (CORE-1658).
 * An adjusted stream lasts the song duration divided by the tempo, so a
 * stream position times this is the song position; 1 when nothing was applied.
 */
export function songSecondsPerStreamSecond(info: PlaybackInfo | null | undefined): number {
  return appliedTempoOf(info?.adjustment);
}

/** Whether two adjustments ask for the same key and tempo. */
export function sameAdjustment(
  a: PlaybackAdjustment | null | undefined,
  b: PlaybackAdjustment | null | undefined,
): boolean {
  return (
    (a?.pitchShiftSemitones ?? 0) === (b?.pitchShiftSemitones ?? 0) &&
    Math.abs(appliedTempoOf(a) - appliedTempoOf(b)) < 0.001
  );
}

/**
 * Whether the stream plays the requested adjustment: the default needs no
 * echo; anything else needs the server's `adjustment` echo to match, since
 * the server omits it whenever it played the item unadjusted.
 */
export function adjustmentApplied(
  info: PlaybackInfo | null | undefined,
  requested: PlaybackAdjustment,
): boolean {
  if (isDefaultAdjustment(requested)) return true;
  return !!info?.adjustment && sameAdjustment(info.adjustment, requested);
}
