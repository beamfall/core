// How one slide gives way to the next (CORE-2981). Oscillux is the only source
// of visual effects in Beamfall (ADR-0228), so the web slideshow never draws a
// crossfade or a pan of its own: Calm runs Oscillux's photo-slideshow pass
// (CORE-2980, slideshowDriver.ts), and every other mode hard-cuts.
import { DEFAULT_INTERVAL_SECONDS } from "./slideshow";
import { createSlideshowDriver, webgl2Available } from "./slideshowDriver";

/** The motion modes the setup sheet offers. */
export type SlideshowMotion = "calm" | "still" | "visualiser";

export const SLIDESHOW_MOTIONS: readonly SlideshowMotion[] = ["calm", "still", "visualiser"];

/**
 * What drives a slide change. Null means a hard cut: the next slide replaces
 * the last in one frame.
 */
export interface SlideMotionDriver {
  /** Moves from the outgoing slide element to the incoming one. */
  advance: (from: HTMLElement | null, to: HTMLElement) => void;
  /**
   * Resolves once the slide element is actually on screen (after the black a
   * calm start holds), so its interval counts from then; at once when the
   * driver does not draw it.
   */
  whenPresented: (element: HTMLElement) => Promise<void>;
  /** Whether the page is advancing: a paused page holds the slide still. */
  setRunning: (running: boolean) => void;
  dispose: () => void;
}

/** What a driver reads from the page at each change. */
export interface SlideMotionOptions {
  /** The slide interval in milliseconds. */
  intervalMs: () => number;
}

function reducedMotion(): boolean {
  return window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;
}

/**
 * The one seam where Oscillux motion plugs in. Oscillux v0.2.16 (vendored)
 * ships the calm photo-slideshow pass, which drives "calm" through WebGL2.
 * Without WebGL2 every mode is a hard cut, and a calm driver that loses its
 * context or cannot run the pass hard-cuts from then on. "visualiser" waits
 * for the visualiser host (CORE-2980).
 */
export function oscilluxSlideMotion(
  motion: SlideshowMotion,
  options: SlideMotionOptions = { intervalMs: () => DEFAULT_INTERVAL_SECONDS * 1000 },
): SlideMotionDriver | null {
  if (motion !== "calm" || !webgl2Available()) return null;
  return createSlideshowDriver({ intervalMs: options.intervalMs, reducedMotion });
}

/**
 * Whether a motion mode can run with the pinned Oscillux package. Calm always
 * can: without WebGL2 it changes slides with a hard cut, as Still does.
 */
export function slideshowMotionAvailable(motion: SlideshowMotion): boolean {
  return motion === "still" || motion === "calm";
}

/** Calm: Oscillux's slideshow pass where WebGL2 runs it, hard cuts elsewhere. */
export function defaultSlideshowMotion(): SlideshowMotion {
  return "calm";
}
