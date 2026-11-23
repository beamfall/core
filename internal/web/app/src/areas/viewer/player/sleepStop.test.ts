import { afterEach, describe, expect, it, vi } from "vitest";
import { loadSleepMark, noteSkip, noteSleepStop, sleepHoldsAutoplay } from "./sleepStop";

describe("sleep stop marks (CORE-3365)", () => {
  afterEach(() => {
    loadSleepMark();
    vi.useRealTimers();
  });

  it("holds back a skip's load when the timer stopped before that load began", () => {
    noteSkip();
    noteSleepStop();
    expect(sleepHoldsAutoplay(loadSleepMark())).toBe(true);
  });

  it("lets a load with no skip pending play", () => {
    noteSleepStop();
    expect(sleepHoldsAutoplay(loadSleepMark())).toBe(false);
  });

  it("lets a skip's mark go once taken, and once it has waited too long", () => {
    vi.useFakeTimers();
    noteSkip();
    noteSleepStop();
    loadSleepMark();
    expect(sleepHoldsAutoplay(loadSleepMark())).toBe(false);
    noteSkip();
    noteSleepStop();
    vi.advanceTimersByTime(3_000);
    expect(sleepHoldsAutoplay(loadSleepMark())).toBe(false);
  });
});
