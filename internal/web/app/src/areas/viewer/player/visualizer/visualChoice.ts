import { useSyncExternalStore } from "react";
import { steppedVisual, visualById } from "./visuals";

// Which Oscillux visual the audio player shows (CORE-2368). Host selection policy, not a visual
// effect: "rotate" moves through every visual, "album" keeps one per album artwork, a visual id
// pins that visual, and "off" shows none (CORE-2814). The choice belongs to this browser, not the
// Profile: it is kept in localStorage, and a blocked or cleared store means "rotate".

export type VisualChoice = "rotate" | "album" | "off" | (string & {});

const CHOICE_KEY = "beamfall.visualChoice";
const DEFAULT_CHOICE: VisualChoice = "rotate";

let choice: VisualChoice | null = null;
// The choice "off" replaced, which turning the visualizer back on restores. It lasts the page.
let beforeOff: VisualChoice | null = null;
// The id of the visual on screen, or about to be, as the Visualizer last reported it. It outlives
// the Visualizer, so turning it back on can return to the visual shown last.
let shown: string | null = null;
const listeners = new Set<() => void>();

function notify(): void {
  for (const listener of listeners) listener();
}

function valid(raw: unknown): VisualChoice {
  if (raw === "rotate" || raw === "album" || raw === "off") return raw;
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
  const current = getVisualChoice();
  if (value === "off" && current !== "off") beforeOff = current;
  choice = value;
  try {
    if (value === DEFAULT_CHOICE) window.localStorage.removeItem(CHOICE_KEY);
    else window.localStorage.setItem(CHOICE_KEY, value);
  } catch {
    // The choice still holds for this page.
  }
  notify();
}

/** Turns the visualizer off, or back on to the choice it had before. */
export function toggleVisualizer(): void {
  setVisualChoice(getVisualChoice() === "off" ? (beforeOff ?? DEFAULT_CHOICE) : "off");
}

/**
 * Pins the visual after (1) or before (-1) the one on screen, in catalog order (CORE-2814). From
 * "off" it turns the visualizer back on with the visual shown last, or the first one.
 */
export function stepVisual(step: 1 | -1, reducedMotion = prefersReducedMotion()): void {
  const current = getVisualChoice();
  const last = visualById(shown ?? "") ?? visualById(current);
  if (current === "off") {
    const back =
      last && (!reducedMotion || last.calm) ? last : steppedVisual(null, 1, reducedMotion);
    setVisualChoice(back.id);
    return;
  }
  setVisualChoice(steppedVisual(last?.id ?? null, step, reducedMotion).id);
}

function prefersReducedMotion(): boolean {
  return !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}

/** The Visualizer reports the visual it shows, or is about to. */
export function setShownVisual(id: string): void {
  if (shown === id) return;
  shown = id;
  notify();
}

function getShownVisual(): string | null {
  return shown;
}

/** The id of the visual on screen, or shown last; null before any. */
export function useShownVisual(): string | null {
  return useSyncExternalStore(subscribe, getShownVisual, getShownVisual);
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The visual choice, re-rendering when it changes. */
export function useVisualChoice(): VisualChoice {
  return useSyncExternalStore(subscribe, getVisualChoice, getVisualChoice);
}

/** Test seam: forget the cached choice, so the next read comes from the store, and the shown visual. */
export function resetVisualChoiceForTest(): void {
  choice = null;
  beforeOff = null;
  shown = null;
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
