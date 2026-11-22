import type { ClientPref, PlaybackMarker } from "@/lib/graphql/types";
import {
  containerSpeedKey,
  PLAYBACK_SPEED_MAX,
  PLAYBACK_SPEED_MIN,
  PREF_KEYS,
  parsePlaybackSpeed,
} from "../lib/preferences";

// Listening controls for the audio stage (CORE-2960): speed, removing long
// pauses and the sleep timer. Pure helpers; the hooks live in useListening.ts.

export const SPEED_PRESETS = [1, 1.2, 1.5, 2] as const;
export const SPEED_STEP = 0.05;
export { PLAYBACK_SPEED_MAX as SPEED_MAX, PLAYBACK_SPEED_MIN as SPEED_MIN };

/** A speed clamped to 0.5–3 and rounded to the slider's 0.05 step. */
export function clampSpeed(value: number): number {
  if (!Number.isFinite(value)) return 1;
  const clamped = Math.min(PLAYBACK_SPEED_MAX, Math.max(PLAYBACK_SPEED_MIN, value));
  return Math.round(clamped / SPEED_STEP) * SPEED_STEP;
}

/** The stored form of a speed: "1.25", "2". */
export function speedValue(value: number): string {
  return String(Math.round(clampSpeed(value) * 100) / 100);
}

export interface ResolvedSpeed {
  speed: number;
  /** Whether the container Entity's own speed decided it. */
  fromContainer: boolean;
}

/**
 * The speed for an item: its container Entity's override, else the Profile's
 * default, else 1×. A malformed or out-of-range value falls to the next level.
 */
export function resolveSpeed(prefs: readonly ClientPref[], containerID: string): ResolvedSpeed {
  const value = (key: string) =>
    key ? parsePlaybackSpeed(prefs.find((pref) => pref.key === key)?.value) : "";
  const container = value(containerSpeedKey(containerID));
  if (container) return { speed: Number(container), fromContainer: true };
  return { speed: Number(value(PREF_KEYS.playbackSpeedDefault) || 1), fromContainer: false };
}

/** A silent span of the item, in file seconds (the element's own clock). */
export interface SilentSpan {
  start: number;
  end: number;
}

/** Only pauses at least this long are removed. */
export const LONG_PAUSE_SECONDS = 0.8;
// Landing this close to a span's end plays it out rather than seeking.
const SPAN_END_SLACK = 0.05;
// Spans are stored in decimal seconds; 2.8 - 2 is 0.7999999999999998.
const SPAN_LENGTH_TOLERANCE = 1e-6;

/**
 * The long pauses of a silence map (`{version: 1, spans: [{startSeconds,
 * endSeconds}]}`), sorted and merged; [] for anything malformed.
 */
export function parseSilenceMap(body: unknown): SilentSpan[] {
  if (!body || typeof body !== "object") return [];
  const { version, spans } = body as { version?: unknown; spans?: unknown };
  if (version !== 1 || !Array.isArray(spans)) return [];
  const parsed: SilentSpan[] = [];
  for (const span of spans) {
    const start = (span as { startSeconds?: unknown })?.startSeconds;
    const end = (span as { endSeconds?: unknown })?.endSeconds;
    if (typeof start !== "number" || typeof end !== "number") continue;
    if (!Number.isFinite(start) || !Number.isFinite(end) || start < 0) continue;
    if (end - start >= LONG_PAUSE_SECONDS - SPAN_LENGTH_TOLERANCE) parsed.push({ start, end });
  }
  parsed.sort((a, b) => a.start - b.start);
  const merged: SilentSpan[] = [];
  for (const span of parsed) {
    const last = merged[merged.length - 1];
    if (last && span.start <= last.end) last.end = Math.max(last.end, span.end);
    else merged.push({ ...span });
  }
  return merged;
}

/** The long pause `time` is inside, with more than a moment of it left. */
export function longPauseAt(spans: readonly SilentSpan[], time: number): SilentSpan | undefined {
  return spans.find((s) => time >= s.start && time < s.end - SPAN_END_SLACK);
}

/** Where to jump when `time` is inside a long pause; undefined otherwise. */
export function pauseSkipTarget(spans: readonly SilentSpan[], time: number): number | undefined {
  return longPauseAt(spans, time)?.end;
}

/** The start of the next long pause after `time`, for scheduling the jump. */
export function nextPauseStart(spans: readonly SilentSpan[], time: number): number | undefined {
  return spans.find((s) => s.start > time)?.start;
}

/** Whole minutes and seconds, for "Saved 6m 12s". */
export function minutesAndSeconds(total: number): { minutes: number; seconds: number } {
  const whole = Math.max(0, Math.floor(total));
  return { minutes: Math.floor(whole / 60), seconds: whole % 60 };
}

// Chapters are the Markers outside the skip-segment kinds (intro, credits…).
function chapters(markers: readonly PlaybackMarker[] | undefined): PlaybackMarker[] {
  return (markers ?? [])
    .filter((m) => !m.segmentKind && Number.isFinite(m.startSeconds) && m.startSeconds >= 0)
    .sort((a, b) => a.startSeconds - b.startSeconds);
}

/** Whether the item has chapters, so "When this chapter ends" applies. */
export function hasChapters(markers: readonly PlaybackMarker[] | undefined): boolean {
  return chapters(markers).length > 0;
}

/**
 * Where the chapter playing at `time` ends: its own end when the Marker has
 * one, else the next chapter's start. null when it runs to the item's end.
 */
export function chapterEndAt(
  markers: readonly PlaybackMarker[] | undefined,
  time: number,
): number | null {
  const list = chapters(markers);
  const current = list.findLast((m) => m.startSeconds <= time);
  const ownEnd = current?.endSeconds;
  if (typeof ownEnd === "number" && Number.isFinite(ownEnd) && ownEnd > time) return ownEnd;
  return list.find((m) => m.startSeconds > time)?.startSeconds ?? null;
}

// A chapter ending this close to the item's end ends with the item.
const CHAPTER_AT_ITEM_END_SECONDS = 1;

/**
 * The timer for "When this chapter ends" at `time`: the chapter's end, or end
 * of item when the chapter runs to (or within a second of) the item's end, so
 * the queue does not go on to the next item.
 */
export function chapterSleep(
  markers: readonly PlaybackMarker[] | undefined,
  time: number,
  duration: number,
): SleepTimer {
  const end = chapterEndAt(markers, time);
  if (end === null) return { kind: "item" };
  if (duration > 0 && end >= duration - CHAPTER_AT_ITEM_END_SECONDS) return { kind: "item" };
  return { kind: "chapter", end };
}

export const SLEEP_MINUTES = [15, 30, 45, 60] as const;
/** The volume fades out over the timer's last seconds, then playback pauses. */
export const SLEEP_FADE_SECONDS = 10;
/**
 * End of item pauses this many listening seconds before the end, so the queue
 * does not advance at any speed.
 */
export const SLEEP_END_LEAD_SECONDS = 0.3;

export type SleepTimer =
  | { kind: "minutes"; minutes: number; until: number }
  | { kind: "item" }
  | { kind: "chapter"; end: number };

/**
 * Seconds of listening left on a timer: wall-clock time for a minutes timer,
 * else media time to the target at the playback rate.
 */
export function sleepRemaining(
  timer: SleepTimer,
  now: number,
  media: { currentTime: number; duration: number; playbackRate: number },
): number {
  if (timer.kind === "minutes") return Math.max(0, (timer.until - now) / 1000);
  const end = timer.kind === "item" ? media.duration : timer.end;
  if (!Number.isFinite(end)) return Number.POSITIVE_INFINITY;
  const left = (end - media.currentTime) / (media.playbackRate || 1);
  return Math.max(0, timer.kind === "item" ? left - SLEEP_END_LEAD_SECONDS : left);
}

/** The share of the listener's volume left as the timer runs out. */
export function sleepFade(remaining: number): number {
  return Math.min(1, Math.max(0, remaining / SLEEP_FADE_SECONDS));
}
