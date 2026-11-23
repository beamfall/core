import { afterEach, describe, expect, it } from "vitest";
import { loadSleepMark, noteSkip, noteSleepStop, sleepHoldsAutoplay } from "./sleepStop";

describe("sleep stop marks (CORE-3365)", () => {
  afterEach(() => {
    loadSleepMark();
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

  it("holds back a skip's load and lets the mark go once taken", () => {
    noteSkip();
    noteSleepStop();
    expect(sleepHoldsAutoplay(loadSleepMark())).toBe(true);
    expect(sleepHoldsAutoplay(loadSleepMark())).toBe(false);
  });

  it("lets a newer skip replace a pending mark", () => {
    noteSkip();
    noteSleepStop();
    noteSkip();
    expect(sleepHoldsAutoplay(loadSleepMark())).toBe(false);
  });
});
