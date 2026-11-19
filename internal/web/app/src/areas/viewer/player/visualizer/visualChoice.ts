import { useSyncExternalStore } from "react";
import { visualById } from "./visuals";

// Which Oscillux visual the audio player shows (CORE-2368). Host selection policy, not a visual
// effect: "rotate" moves through every visual, "album" keeps one per album artwork, and a visual
// id pins that visual. The choice belongs to this browser, not the Profile: it is kept in
// localStorage, and a blocked or cleared store means "rotate".

export type VisualChoice = "rotate" | "album" | (string & {});

const CHOICE_KEY = "beamfall.visualChoice";
const DEFAULT_CHOICE: VisualChoice = "rotate";

let choice: VisualChoice | null = null;
const listeners = new Set<() => void>();

function valid(raw: unknown): VisualChoice {
  if (raw === "rotate" || raw === "album") return raw;
  if (typeof raw === "string" && visualById(raw)) return raw;
  return DEFAULT_CHOICE;
}

function readChoice(): VisualChoice {
  try {
    return valid(window.localStorage.getItem(CHOICE_KEY));
  } catch {
    return DEFAULT_CHOICE;
  }
}

export function getVisualChoice(): VisualChoice {
  choice ??= readChoice();
  return choice;
}

/** Sets and keeps the visual choice. */
export function setVisualChoice(next: VisualChoice): void {
  const value = valid(next);
  choice = value;
  try {
    if (value === DEFAULT_CHOICE) window.localStorage.removeItem(CHOICE_KEY);
    else window.localStorage.setItem(CHOICE_KEY, value);
  } catch {
    // The choice still holds for this page.
  }
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The visual choice, re-rendering when it changes. */
export function useVisualChoice(): VisualChoice {
  return useSyncExternalStore(subscribe, getVisualChoice, getVisualChoice);
}

/** Test seam: forget the cached choice so the next read comes from the store. */
export function resetVisualChoiceForTest(): void {
  choice = null;
}

/**
 * True where this browser can draw an Oscillux visual. A ResizeObserver is required: it withdraws
 * a frame drawn at a size the slot left before the paint, which the flash limiter relies on
 * (Codex round 19). Every browser with WebGL2 has one.
 */
export function canVisualize(): boolean {
  return (
    typeof WebGL2RenderingContext !== "undefined" &&
    typeof OfflineAudioContext !== "undefined" &&
    typeof ResizeObserver !== "undefined"
  );
}
