// Subtitle offset and audio delay values (CORE-1671), shared by the subtitle
// panel and the audio sync panel. Both are signed seconds with mpv's meaning:
// a positive subtitle offset shows cues later, a positive audio delay plays the
// sound later. Values step in tenths and persist on this device per Profile and
// Media, so a fix made once holds the next time the same item plays.
//
// The server keeps its own per-Profile nudge for sidecar tracks (ADR-0194 §4),
// but the playbackInfo wire does not carry the value yet (CORE-0782), so the
// player cannot show it and applying both would shift twice. Until that wire
// lands the player persists locally and leaves the server nudge alone.

export const SUBTITLE_OFFSET_LIMIT = 60;
export const AUDIO_DELAY_LIMIT = 5;
export const SYNC_FINE_STEP = 0.1;
export const SYNC_COARSE_STEP = 0.5;

export interface SyncValues {
  subtitleOffset: number;
  audioDelay: number;
}

const STORAGE_PREFIX = "bf.playbackSync.";

/** Rounds to tenths and clamps into [min, max]. */
export function clampSync(value: number, min: number, max: number): number {
  if (!Number.isFinite(value)) return 0;
  const rounded = Math.round(value * 10) / 10;
  // Normalise -0 so the display never reads "−0.0 s".
  return Math.min(max, Math.max(min, rounded)) || 0;
}

/** "+0.3 s", "−1.2 s" or "0.0 s" (a real minus sign, not a hyphen). */
export function formatSync(value: number): string {
  const magnitude = Math.abs(value).toFixed(1);
  if (Number(magnitude) === 0) return "0.0 s";
  return `${value > 0 ? "+" : "−"}${magnitude} s`;
}

function storageKey(profileID: string, mediaID: string): string | null {
  if (!profileID || !mediaID) return null;
  return `${STORAGE_PREFIX}${profileID}.${mediaID}`;
}

export function readSync(profileID: string, mediaID: string): SyncValues {
  const zero = { subtitleOffset: 0, audioDelay: 0 };
  const key = storageKey(profileID, mediaID);
  if (!key) return zero;
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return zero;
    const parsed = JSON.parse(raw) as Partial<SyncValues>;
    return {
      subtitleOffset: clampSync(
        Number(parsed.subtitleOffset ?? 0),
        -SUBTITLE_OFFSET_LIMIT,
        SUBTITLE_OFFSET_LIMIT,
      ),
      audioDelay: clampSync(Number(parsed.audioDelay ?? 0), -AUDIO_DELAY_LIMIT, AUDIO_DELAY_LIMIT),
    };
  } catch {
    return zero;
  }
}

/** Stores one value; an all-zero entry is removed rather than kept. */
export function writeSync(profileID: string, mediaID: string, patch: Partial<SyncValues>): void {
  const key = storageKey(profileID, mediaID);
  if (!key) return;
  const next = { ...readSync(profileID, mediaID), ...patch };
  try {
    if (!next.subtitleOffset && !next.audioDelay) {
      window.localStorage.removeItem(key);
    } else {
      window.localStorage.setItem(key, JSON.stringify(next));
    }
  } catch {
    // Remembering the value is optional; the live offset still applies.
  }
}

/**
 * The styled ASS renderer (JASSUB) draws on a canvas, not from native cues, so
 * it cannot be retimed by shifting cue times. The subtitle hook publishes the
 * live offset on the <video> element instead, and the renderer follows it.
 */
export const SUBTITLE_OFFSET_EVENT = "bf:subtitle-offset";
const SUBTITLE_OFFSET_ATTRIBUTE = "data-bf-subtitle-offset";

export function publishSubtitleOffset(video: HTMLVideoElement, offset: number): void {
  if (offset === publishedSubtitleOffset(video)) return;
  if (offset) {
    video.setAttribute(SUBTITLE_OFFSET_ATTRIBUTE, String(offset));
  } else {
    video.removeAttribute(SUBTITLE_OFFSET_ATTRIBUTE);
  }
  video.dispatchEvent(new Event(SUBTITLE_OFFSET_EVENT));
}

export function publishedSubtitleOffset(video: HTMLVideoElement): number {
  return clampSync(
    Number(video.getAttribute(SUBTITLE_OFFSET_ATTRIBUTE) ?? 0),
    -SUBTITLE_OFFSET_LIMIT,
    SUBTITLE_OFFSET_LIMIT,
  );
}
