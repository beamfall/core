// How one slide gives way to the next (CORE-2981). Oscillux is the only source
// of visual effects in Beamfall (ADR-0228), so the web slideshow never draws a
// crossfade or a pan of its own: it hard-cuts until the pinned Oscillux package
// ships its calm photo-slideshow mode (CORE-2980), which plugs in here.

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
  dispose: () => void;
}

/**
 * The one seam where Oscillux motion plugs in. Oscillux v0.2.10 (vendored)
 * has a beat-quantised visualiser switch scheduler and the visualiser's own
 * switch shader, but no calm photo mode, so every mode is a hard cut today.
 * CORE-2980 returns a driver from the pinned package here for "calm", and the
 * visualiser host for "visualiser".
 */
export function oscilluxSlideMotion(_motion: SlideshowMotion): SlideMotionDriver | null {
  return null;
}

/** Whether a motion mode can run with the pinned Oscillux package. */
export function slideshowMotionAvailable(motion: SlideshowMotion): boolean {
  return motion === "still" || oscilluxSlideMotion(motion) !== null;
}

/** The first available of Calm then Still: Still until CORE-2980 lands. */
export function defaultSlideshowMotion(): SlideshowMotion {
  return slideshowMotionAvailable("calm") ? "calm" : "still";
}
