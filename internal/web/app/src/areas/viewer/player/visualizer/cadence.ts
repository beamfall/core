// Tells a browser's presentation cap from render overload before frame intervals reach the
// Oscillux governor (CORE-2483). The governor targets 60 Hz and reads a slow average as overload,
// but browsers cap requestAnimationFrame at a steady 30 Hz in power-saving modes (Safari Low Power
// Mode, Chrome Energy Saver) and in background iframes, however light the render. A steady cadence
// at that cap is scaled to the target, so only frames slower than the cap, or an unsteady
// cadence, count against the render. WebGL timer queries would measure GPU time directly, but
// EXT_disjoint_timer_query_webgl2 is not exposed by default in current browsers.

/** Intervals the gate watches before it passes any to the governor. */
export const CADENCE_WINDOW = 30;
/** The capped cadence browsers apply, in milliseconds. */
const CAP_MS = 1000 / 30;
/** A median within this fraction of CAP_MS is at the cap. */
const CAP_TOLERANCE = 0.08;
/** An interval within this fraction of the median is steady ... */
const STEADY_TOLERANCE = 0.15;
/** ... and the cadence is steady when this share of the window is. */
const STEADY_SHARE = 0.9;
/** Intervals at or above this are pauses, as in the governor's default. */
const PAUSE_MS = 250;

export class CadenceGate {
  private readonly window: number[] = [];

  constructor(private readonly targetMs = 1000 / 60) {}

  /** Forgets the cadence, for example after a size change or a switch. */
  reset(): void {
    this.window.length = 0;
  }

  /**
   * The interval to record with the governor for this frame, or null to skip it while the gate
   * is still learning the cadence. Pauses and invalid intervals pass through unchanged for the
   * governor to ignore.
   */
  sample(frameMs: number): number | null {
    if (!Number.isFinite(frameMs) || frameMs <= 0 || frameMs >= PAUSE_MS) return frameMs;
    this.window.push(frameMs);
    if (this.window.length > CADENCE_WINDOW) this.window.shift();
    if (this.window.length < CADENCE_WINDOW) return null;
    const sorted = [...this.window].sort((a, b) => a - b);
    const median = sorted[sorted.length >> 1];
    if (Math.abs(median - CAP_MS) > CAP_TOLERANCE * CAP_MS) return frameMs;
    const steady = this.window.filter(
      (x) => Math.abs(x - median) <= STEADY_TOLERANCE * median,
    ).length;
    if (steady < STEADY_SHARE * this.window.length) return frameMs;
    return (frameMs * this.targetMs) / median;
  }
}
