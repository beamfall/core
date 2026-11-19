import { describe, expect, it } from "vitest";
import { REFRESH_WINDOW, RefreshBound } from "./refresh";

// Draws `count` frames `interval` ms apart and returns the credit of the last.
function run(refresh: RefreshBound, interval: number, count: number): number {
  let shown = 0;
  for (let i = 0; i < count; i++) {
    refresh.note(interval);
    shown = refresh.shown(interval);
  }
  return shown;
}

describe("RefreshBound", () => {
  // Codex round 21: a steady cadence can be every other refresh of a faster display, so it never
  // earns more than half of each interval, however long it has been steady.
  it.each([60, 120])("credits half of each interval at a steady %i Hz", (hz) => {
    const refresh = new RefreshBound();
    expect(run(refresh, 1000 / hz, 600)).toBeCloseTo(500 / hz);
  });

  it("never credits more than a frame painted one refresh late was shown", () => {
    // Every cadence a display of period R can give a page drawing at each n-th refresh, with a
    // late paint then an on-time one: the frame was shown for max(R, interval - R).
    for (const hz of [24, 30, 48, 60, 75, 90, 120, 144, 165, 240]) {
      const R = 1000 / hz;
      for (const n of [1, 2, 3, 4, 6]) {
        const refresh = new RefreshBound();
        const interval = n * R;
        const shown = Math.max(R, interval - R);
        for (let i = 0; i < 3 * REFRESH_WINDOW; i++) {
          refresh.note(interval);
          expect(refresh.shown(interval)).toBeLessThanOrEqual(shown + 1e-9);
        }
      }
    }
  });

  // Codex round 27: a page that drew at 240 Hz and now draws at 60 Hz kept the 4.17 ms minimum
  // and credited a quarter of each interval for as long as it ran.
  it("measures a slower cadence again within the window", () => {
    const refresh = new RefreshBound();
    run(refresh, 1000 / 240, 500);
    expect(run(refresh, 1000 / 60, 1)).toBeCloseTo(1000 / 240);
    expect(run(refresh, 1000 / 60, REFRESH_WINDOW - 2)).toBeCloseTo(1000 / 240);
    expect(run(refresh, 1000 / 60, 1)).toBeCloseTo(500 / 60);
  });

  it("takes a faster cadence at its first interval", () => {
    const refresh = new RefreshBound();
    run(refresh, 1000 / 60, 100);
    expect(run(refresh, 1000 / 240, 1)).toBeCloseTo(500 / 240);
  });

  it("holds a short interval for the window, then lets it go", () => {
    const refresh = new RefreshBound();
    run(refresh, 1000 / 60, 100);
    // A jittery frame: a late callback, then one that catches up.
    refresh.note(28);
    refresh.note(5);
    expect(refresh.shown(1000 / 60)).toBeCloseTo(5);
    expect(run(refresh, 1000 / 60, REFRESH_WINDOW - 1)).toBeCloseTo(5);
    expect(run(refresh, 1000 / 60, 1)).toBeCloseTo(500 / 60);
  });

  it("keeps the bound between 4 ms and a 24 Hz refresh", () => {
    const refresh = new RefreshBound();
    // A throttled page: one frame a second is credited one 24 Hz refresh.
    expect(run(refresh, 1000, 2 * REFRESH_WINDOW)).toBeCloseTo(1000 / 24);
    // A burst of callbacks closer than any display refreshes.
    expect(run(refresh, 1, 1)).toBeCloseTo(0.5);
    expect(run(refresh, 1000 / 60, 1)).toBeCloseTo(4);
  });

  it("forgets the cadence on reset", () => {
    const refresh = new RefreshBound();
    run(refresh, 1000 / 240, 100);
    refresh.reset();
    expect(run(refresh, 1000 / 60, 1)).toBeCloseTo(500 / 60);
  });
});
