import { useSyncExternalStore } from "react";

// This screen's display delay trim (CORE-2245). A panel takes time to show a
// frame, and a follower screen has to leave the picture in step with the lead
// screen's sound, so it shows each frame this many ms early. The trim belongs
// to the screen, not the party or the Profile: it is kept in this browser
// only, and a blocked or cleared store means no trim.

const DISPLAY_TRIM_KEY = "beamfall.followerDisplayTrimMS";

/** One step of the trim control. */
export const DISPLAY_TRIM_STEP_MS = 10;
/** The furthest the trim goes either way. */
export const MAX_DISPLAY_TRIM_MS = 500;

let trimMS: number | null = null;
const listeners = new Set<() => void>();

function clampTrim(value: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.min(MAX_DISPLAY_TRIM_MS, Math.max(-MAX_DISPLAY_TRIM_MS, Math.round(value)));
}

function readTrim(): number {
  try {
    return clampTrim(Number(window.localStorage.getItem(DISPLAY_TRIM_KEY) ?? 0));
  } catch {
    return 0;
  }
}

/** How many ms early this screen shows a followed picture; negative is late. */
export function getDisplayTrimMS(): number {
  trimMS ??= readTrim();
  return trimMS;
}

/** Sets and keeps the trim, clamped to its range. */
export function setDisplayTrimMS(value: number): void {
  const next = clampTrim(value);
  trimMS = next;
  try {
    if (next === 0) window.localStorage.removeItem(DISPLAY_TRIM_KEY);
    else window.localStorage.setItem(DISPLAY_TRIM_KEY, String(next));
  } catch {
    // The trim still holds for this page.
  }
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The trim, re-rendering when it changes. */
export function useDisplayTrimMS(): number {
  return useSyncExternalStore(subscribe, getDisplayTrimMS, getDisplayTrimMS);
}

/** Test seam: forget the cached trim so the next read comes from the store. */
export function resetDisplayTrimForTest(): void {
  trimMS = null;
}
