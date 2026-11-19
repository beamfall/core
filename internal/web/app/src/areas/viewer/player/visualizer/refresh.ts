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
// The bound is the shortest interval between two draws since the loop first ran, and it is never
// forgotten: a larger bound credits more, and a page that slows down on the same display (120 to
// 30 draws a second at 120 Hz) would raise a bound taken over its recent draws to four refreshes
// and be credited two for a frame shown one (Codex round 28). So a display that slows down (a
// window moved from a 240 Hz monitor to a 60 Hz one) keeps the shorter bound and is credited
// less than it could be, until the visualizer is mounted again.

// The shortest refresh period trusted (a 250 Hz display): two callbacks closer than that are a
// browser catching up, not a display refreshing.
const MIN_REFRESH_MS = 4;
// The longest refresh period of any display a browser presents on (24 Hz): a host drawing slower
// than its display, as a throttled one does, cannot measure the refresh, and a paint can be late
// by one refresh, so a frame is assumed shown for no longer than this (Codex round 20).
const MAX_REFRESH_MS = 1000 / 24;

export class RefreshBound {
  private bound = MAX_REFRESH_MS;

  /** Records the interval between two draws, in milliseconds. */
  note(interval: number): void {
    this.bound = Math.min(this.bound, Math.max(MIN_REFRESH_MS, interval));
  }

  /** The time credited to a frame that the next draw followed after `interval` milliseconds. */
  shown(interval: number): number {
    return Math.min(this.bound, Math.max(interval / 2, interval - this.bound));
  }
}
