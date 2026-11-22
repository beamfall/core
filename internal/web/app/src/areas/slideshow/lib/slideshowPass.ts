// Host-side inputs for Oscillux's calm photo-slideshow pass (vendor/oscillux/glsl300/slideshow,
// CORE-2980). The pass's README is the contract: the host owns layout choice, timing and the
// eased progress; every pixel of the picture is the pass's. Nothing here draws.

/** The pass's transition styles, by id (README, "Style"). */
export const SLIDESHOW_STYLES = ["dissolve", "lightSweep", "iris"] as const;
export type SlideshowStyle = (typeof SLIDESHOW_STYLES)[number];

/** The pass's layouts, by id (README, "Layout"). */
export const SLIDESHOW_LAYOUTS = ["cover", "fill", "pair"] as const;
export type SlideshowLayout = (typeof SLIDESHOW_LAYOUTS)[number];

/** SlideshowBlock: eleven std140 vec4s, 176 bytes. */
export const SLIDESHOW_BLOCK_FLOATS = 44;

/** The README's suggested durations, in milliseconds. */
export const STYLE_DURATION_MS: Readonly<Record<SlideshowStyle, number>> = {
  dissolve: 1500,
  lightSweep: 1800,
  iris: 1600,
};

/** Reduce Motion: a plain dissolve over 1.0 s, with motion 0 and amplitude 0. */
export const REDUCED_DURATION_MS = 1000;

/** The README's floor between transition starts; a change sooner than this is a hard cut. */
export const MIN_TRANSITION_GAP_MS = 5000;

/** A photo's aspect beyond this ratio of the frame's (either way) gets the blurred fill. */
export const FILL_ASPECT_RATIO = 1.4;

/** One slide's fields (README, "Parameters"). A rect is photo uv; all zeros means unknown. */
export interface SlideFields {
  k?: number;
  layout?: SlideshowLayout;
  direction?: number;
  aspect0?: number;
  aspect1?: number;
  beats?: number;
  rect0?: readonly number[];
  rect1?: readonly number[];
}

/** One draw's fields; `b` defaults to `a`, as between transitions. */
export interface SlideshowFields {
  p?: number;
  style?: SlideshowStyle;
  aspect: number;
  motion?: number;
  amplitude?: number;
  beatPhase?: number;
  width: number;
  height: number;
  a: SlideFields;
  b?: SlideFields;
}

const UNKNOWN_RECT = [0, 0, 0, 0] as const;

function slideWords(out: Float32Array, at: number, s: SlideFields): void {
  const aspect0 = s.aspect0 ?? 1;
  out.set(
    [
      s.k ?? 0,
      SLIDESHOW_LAYOUTS.indexOf(s.layout ?? "cover"),
      s.direction ?? 0,
      aspect0,
      s.aspect1 ?? aspect0,
      s.beats ?? 1,
      0,
      0,
      ...(s.rect0 ?? UNKNOWN_RECT),
      ...(s.rect1 ?? UNKNOWN_RECT),
    ],
    at,
  );
}

/**
 * Packs the SlideshowBlock into `out` (44 floats), field for field as Oscillux's reference
 * packer, `slideshowBlock` in tools/shader/slideshow.mjs.
 */
export function packSlideshowBlock(
  f: SlideshowFields,
  out = new Float32Array(SLIDESHOW_BLOCK_FLOATS),
): Float32Array {
  out.set([
    f.p ?? 0,
    SLIDESHOW_STYLES.indexOf(f.style ?? "dissolve"),
    f.aspect,
    f.motion ?? 1,
    f.amplitude ?? 0,
    f.beatPhase ?? 0,
    0,
    0,
    f.width,
    f.height,
    0,
    0,
  ]);
  slideWords(out, 12, f.a);
  slideWords(out, 28, f.b ?? f.a);
  return out;
}

/**
 * The README's layout choice: two photos (the page pairs portraits only on a landscape frame)
 * side by side, else the blurred fill for a photo whose aspect differs much from the frame's,
 * else cover.
 */
export function chooseLayout(
  photoAspects: readonly number[],
  frameAspect: number,
): SlideshowLayout {
  if (photoAspects.length > 1) return "pair";
  const ratio = (photoAspects[0] ?? frameAspect) / frameAspect;
  return Math.max(ratio, 1 / ratio) > FILL_ASPECT_RATIO ? "fill" : "cover";
}

/** The transition for the nth change: the three styles in turn, or the reduced dissolve. */
export function transitionFor(
  n: number,
  reducedMotion: boolean,
): { style: SlideshowStyle; durationMs: number } {
  if (reducedMotion) return { style: "dissolve", durationMs: REDUCED_DURATION_MS };
  const style = SLIDESHOW_STYLES[n % SLIDESHOW_STYLES.length] ?? "dissolve";
  return { style, durationMs: STYLE_DURATION_MS[style] };
}

/** The progress the pass reads: elapsed over duration, eased once with smoothstep (README, "Timing"). */
export function transitionProgress(elapsedMs: number, durationMs: number): number {
  const t = Math.min(1, Math.max(0, elapsedMs / durationMs));
  return t * t * (3 - 2 * t);
}

/** A slide's Ken Burns progress: linear over its interval plus its incoming transition. */
export function slideProgress(elapsedMs: number, spanMs: number): number {
  return Math.min(1, Math.max(0, elapsedMs / spanMs));
}
