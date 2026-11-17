import { useEffect, useState, useSyncExternalStore } from "react";

// Track-change motion on the audio card (CORE-2331 words, CORE-2332 artwork).
// Host UI chrome, not a visual effect: the styles move the card's own words
// and picture with transform, opacity and filter only. The choice belongs to
// this browser, not the Profile: it is kept in localStorage, and a blocked or
// cleared store means "mix".

export const WORD_STYLES = ["rise", "flap", "wipe"] as const;
export const ART_STYLES = ["zoom", "flip", "slide", "dissolve"] as const;
export type WordStyle = (typeof WORD_STYLES)[number];
export type ArtStyle = (typeof ART_STYLES)[number];

export interface TrackChangeMotion {
  words: WordStyle | "mix";
  art: ArtStyle | "mix";
}

/** The styles one change plays; "fade" is the reduced-motion crossfade. */
export interface ChangeMotion {
  words: WordStyle | "fade";
  art: ArtStyle | "fade";
}

// "Mix" pairs the word and artwork styles so they read as one move.
const MIX: readonly ChangeMotion[] = [
  { words: "rise", art: "dissolve" },
  { words: "flap", art: "flip" },
  { words: "wipe", art: "slide" },
  { words: "rise", art: "zoom" },
];

const MOTION_KEY = "beamfall.trackChangeMotion";
const DEFAULT_MOTION: TrackChangeMotion = { words: "mix", art: "mix" };

let motion: TrackChangeMotion | null = null;
const listeners = new Set<() => void>();

function valid(raw: unknown): TrackChangeMotion {
  const value = (raw ?? {}) as Partial<TrackChangeMotion>;
  return {
    words: (WORD_STYLES as readonly string[]).includes(value.words ?? "")
      ? (value.words as WordStyle)
      : "mix",
    art: (ART_STYLES as readonly string[]).includes(value.art ?? "")
      ? (value.art as ArtStyle)
      : "mix",
  };
}

function readMotion(): TrackChangeMotion {
  try {
    const raw = window.localStorage.getItem(MOTION_KEY);
    return raw ? valid(JSON.parse(raw)) : DEFAULT_MOTION;
  } catch {
    return DEFAULT_MOTION;
  }
}

export function getTrackChangeMotion(): TrackChangeMotion {
  motion ??= readMotion();
  return motion;
}

/** Sets and keeps part of the motion choice. */
export function setTrackChangeMotion(change: Partial<TrackChangeMotion>): void {
  const next = valid({ ...getTrackChangeMotion(), ...change });
  motion = next;
  try {
    if (next.words === "mix" && next.art === "mix") window.localStorage.removeItem(MOTION_KEY);
    else window.localStorage.setItem(MOTION_KEY, JSON.stringify(next));
  } catch {
    // The choice still holds for this page.
  }
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The motion choice, re-rendering when it changes. */
export function useTrackChangeMotion(): TrackChangeMotion {
  return useSyncExternalStore(subscribe, getTrackChangeMotion, getTrackChangeMotion);
}

/** Test seam: forget the cached choice so the next read comes from the store. */
export function resetTrackChangeMotionForTest(): void {
  motion = null;
}

// A stable small hash, so one item always gets the same mixed style and a
// re-render never re-rolls it.
function hash(text: string): number {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) h = Math.imul(h ^ text.charCodeAt(i), 16777619);
  return h >>> 0;
}

/**
 * The styles the change to `key` plays: the chosen ones, a pair picked by the
 * item for "mix", or a plain crossfade when the viewer prefers reduced motion.
 */
export function motionForChange(
  key: string,
  choice: TrackChangeMotion,
  reducedMotion: boolean,
): ChangeMotion {
  if (reducedMotion) return { words: "fade", art: "fade" };
  const pick = MIX[hash(key) % MIX.length];
  return {
    words: choice.words === "mix" ? pick.words : choice.words,
    art: choice.art === "mix" ? pick.art : choice.art,
  };
}

const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

/** Whether the viewer prefers reduced motion, following changes. */
export function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = useState(
    () => typeof window !== "undefined" && !!window.matchMedia?.(REDUCED_MOTION_QUERY).matches,
  );
  useEffect(() => {
    const query = window.matchMedia?.(REDUCED_MOTION_QUERY);
    if (!query) return;
    const update = () => setReduced(query.matches);
    update();
    query.addEventListener?.("change", update);
    return () => query.removeEventListener?.("change", update);
  }, []);
  return reduced;
}
