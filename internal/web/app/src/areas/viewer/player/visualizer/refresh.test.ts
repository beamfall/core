import { describe, expect, it } from "vitest";
import { RefreshBound } from "./refresh";

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
        for (let i = 0; i < 100; i++) {
          refresh.note(interval);
          expect(refresh.shown(interval)).toBeLessThanOrEqual(shown + 1e-9);
        }
      }
    }
  });

  // Codex round 28: a bound taken over the last 32 draws forgot the display. At 120 Hz a page
  // that slowed to 30 draws a second had a bound of four refreshes after 32 of them, and a frame
  // painted three refreshes late and followed on time, shown 8.33 ms, was credited 16.67 ms.
  it("keeps the shortest interval however long the cadence has been slower", () => {
    const refresh = new RefreshBound();
    run(refresh, 1000 / 120, 100);
    expect(run(refresh, 1000 / 30, 1000)).toBeCloseTo(1000 / 120);
  });

  it("takes a faster cadence at its first interval", () => {
    const refresh = new RefreshBound();
    run(refresh, 1000 / 60, 100);
    expect(run(refresh, 1000 / 240, 1)).toBeCloseTo(500 / 240);
  });

  it("keeps a short interval", () => {
    const refresh = new RefreshBound();
    run(refresh, 1000 / 60, 100);
    // A jittery frame: a late callback, then one that catches up.
    refresh.note(28);
    refresh.note(5);
    expect(run(refresh, 1000 / 60, 1000)).toBeCloseTo(5);
  });

  // Codex round 29: the credit as it is where it is not a lower bound, and where it is low.
  it("credits half the interval of a page never faster, and the bound of one that slowed", () => {
    // 30 draws a second since the mount. On a 120 Hz display a frame painted three refreshes
    // late was shown 8.33 ms: no interval has shown the page its display.
    expect(run(new RefreshBound(), 1000 / 30, 100)).toBeCloseTo(500 / 30);
    // One 1/240 s interval, then 30 draws a second: an eighth of each interval.
    const refresh = new RefreshBound();
    run(refresh, 1000 / 240, 1);
    expect(run(refresh, 1000 / 30, 100)).toBeCloseTo(1000 / 240);
  });

  it("keeps the bound between 4 ms and a 24 Hz refresh", () => {
    const refresh = new RefreshBound();
    // A throttled page: one frame a second is credited one 24 Hz refresh.
    expect(run(refresh, 1000, 100)).toBeCloseTo(1000 / 24);
    // A burst of callbacks closer than any display refreshes.
    expect(run(refresh, 1, 1)).toBeCloseTo(0.5);
    expect(run(refresh, 1000 / 60, 1)).toBeCloseTo(4);
  });
});
