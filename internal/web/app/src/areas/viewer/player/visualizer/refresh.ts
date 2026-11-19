// The time the flash limiter is credited with for each frame (limiter README, `limiter1`
// deltaMs): a lower bound on the time the last frame was on screen, never more.
//
// A page cannot read paint times or the display's refresh period R. The last frame may have been
// painted one refresh late and this one on time, so it was shown for max(R, interval - R). The
// intervals between draws bound R from above only: animation frames arrive no faster than the
// display refreshes, but they can run at every other refresh however steady they are (Safari
// draws at 60 Hz on a 120 Hz display by default, and power-saving modes cap at 30 Hz). So the
// credit is the least of that over every R up to the bound, max(interval / 2, interval - bound),
// and no more than the bound (Codex rounds 19 to 21). At a steady cadence that is half of each
// interval. A steady cadence does not show the refresh period, so it earns no more than that.
//
// The bound is the shortest interval among the last REFRESH_WINDOW draws, not since the loop
// started: a display that slows down (240 Hz to 60 Hz, a window moved to another monitor) would
// otherwise keep the old minimum and credit a quarter of each interval (Codex round 27).
// Forgetting is safe: any bound at or above R gives a credit at or below the time shown.

/** Draw intervals the bound is taken over. */
export const REFRESH_WINDOW = 32;
// The shortest refresh period trusted (a 250 Hz display): two callbacks closer than that are a
// browser catching up, not a display refreshing.
const MIN_REFRESH_MS = 4;
// The longest refresh period of any display a browser presents on (24 Hz): a host drawing slower
// than its display, as a throttled one does, cannot measure the refresh, and a paint can be late
// by one refresh, so a frame is assumed shown for no longer than this (Codex round 20).
const MAX_REFRESH_MS = 1000 / 24;

export class RefreshBound {
  // A ring of the last intervals, so a frame allocates nothing.
  private readonly recent = new Float64Array(REFRESH_WINDOW).fill(MAX_REFRESH_MS);
  private at = 0;

  /** Forgets the cadence, when the draw loop starts again. */
  reset(): void {
    this.recent.fill(MAX_REFRESH_MS);
    this.at = 0;
  }

  /** Records the interval between two draws, in milliseconds. */
  note(interval: number): void {
    this.recent[this.at] = Math.min(MAX_REFRESH_MS, Math.max(MIN_REFRESH_MS, interval));
    this.at = (this.at + 1) % REFRESH_WINDOW;
  }

  /** The time credited to a frame that the next draw followed after `interval` milliseconds. */
  shown(interval: number): number {
    let bound = MAX_REFRESH_MS;
    for (let i = 0; i < REFRESH_WINDOW; i++) if (this.recent[i] < bound) bound = this.recent[i];
    return Math.min(bound, Math.max(interval / 2, interval - bound));
  }
}
