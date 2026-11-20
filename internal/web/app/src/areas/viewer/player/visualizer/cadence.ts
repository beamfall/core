// Tells a browser's presentation cap from render overload before frame intervals reach the
// Oscillux governor (CORE-2483). The governor targets 60 Hz and reads a slow average as overload,
// but browsers cap requestAnimationFrame at a steady 30 Hz in power-saving modes (Safari Low Power
// Mode, Chrome Energy Saver) and in background iframes, however light the render. A steady cadence
// at that cap is scaled to the target, so only frames slower than the cap, or an unsteady
// cadence, count against the render. WebGL timer queries would measure GPU time directly, but
// EXT_disjoint_timer_query_webgl2 is not exposed by default in current browsers.
//
// The cap band is narrow (about ±1.2 ms) so a steady overload a little slower than the cap, 35 ms
// on a variable-refresh or 120 Hz display, still counts. An overload that lands exactly on 30 Hz,
// as a render missing every other 60 Hz vsync does, is indistinguishable from the cap by timing
// alone and is not demoted; it already draws at the capped browser's rate.
//
// A page the browser throttles without hiding it (a pane in the background of a desktop app, an
// occluded window) gets callbacks 30 to 250 ms apart, under the governor's pause gap, while the
// GPU sits idle. The renderer fences each frame and checks the fence GPU_BUDGET_MS after the
// frame: a slow interval after a frame the GPU finished within that budget is the browser's
// scheduling, not the render, and records as the target interval (CORE-2720), so the level
// holds or, with the GPU idle, recovers. Measured in Chromium under ANGLE Metal, a 200 ms
// throttle found every fence signalled and a GPU overload at 30 to 100 ms found none signalled.
// The check runs at the budget, not at the next callback, so a browser that holds callbacks back
// until the GPU is done still counts its overload. A check that runs late, behind a busy main
// thread, bounds the GPU's time only by when it ran, so the interval records as that bound.

/** Intervals the gate watches before it passes any to the governor. */
export const CADENCE_WINDOW = 30;
/** The capped cadence browsers apply, in milliseconds. */
const CAP_MS = 1000 / 30;
/** A median within this fraction of CAP_MS is at the cap. */
const CAP_TOLERANCE = 0.035;
/** An interval within this fraction of the median is steady ... */
const STEADY_TOLERANCE = 0.15;
/** ... and the cadence is steady when this share of the window is. */
const STEADY_SHARE = 0.9;
/** Intervals at or above this are pauses, as in the governor's default. */
const PAUSE_MS = 250;
/** An interval above the target by this factor is slow, as in the governor's default. */
const SLOW_FACTOR = 1.25;
/** The time after a frame within which a GPU that keeps up has finished it. */
export const GPU_BUDGET_MS = (1000 / 60) * SLOW_FACTOR;

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
   * governor to ignore. gpuMs, when known, is a bound on how long the GPU took over the previous
   * frame. A slow interval after a frame the GPU kept within GPU_BUDGET_MS records as the target
   * interval and stays out of the cadence window; a looser bound still goes through cap detection
   * and then caps what is recorded.
   */
  sample(frameMs: number, gpuMs: number | null = null): number | null {
    if (!Number.isFinite(frameMs) || frameMs <= 0 || frameMs >= PAUSE_MS) return frameMs;
    const slow = frameMs > this.targetMs * SLOW_FACTOR;
    if (slow && gpuMs !== null && gpuMs <= GPU_BUDGET_MS) return this.targetMs;
    const sample = this.capped(frameMs);
    return sample !== null && slow && gpuMs !== null ? Math.min(sample, gpuMs) : sample;
  }

  // The interval scaled to the target when the cadence is steady at the browser cap.
  private capped(frameMs: number): number | null {
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
