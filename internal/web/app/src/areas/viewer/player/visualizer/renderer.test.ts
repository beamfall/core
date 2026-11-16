import { describe, expect, it } from "vitest";
import { QUALITY_LADDER, renderQuality, switchAction } from "./renderer";

describe("switchAction (CORE-2219)", () => {
  it("blends from one visual to another", () => {
    expect(switchAction("aurora", false, "nebula", false)).toBe("blend");
  });

  it("keeps the visual when it does not change", () => {
    expect(switchAction("aurora", false, "aurora", false)).toBe("same");
  });

  it("starts the first visual at once", () => {
    expect(switchAction(null, false, "aurora", false)).toBe("instant");
  });

  it("changes at once under reduced motion, even mid-switch", () => {
    expect(switchAction("aurora", false, "nebula", true)).toBe("instant");
    expect(switchAction("aurora", true, "nebula", true)).toBe("instant");
  });

  it("queues a request made while a switch runs", () => {
    expect(switchAction("aurora", true, "nebula", false)).toBe("queue");
    expect(switchAction("aurora", true, "aurora", false)).toBe("queue");
  });
});

describe("renderQuality (CORE-2215)", () => {
  it("follows the Oscillux reference host's ladder", () => {
    expect(QUALITY_LADDER.map((s) => s.scale)).toEqual([0.5, 0.67, 0.83, 1]);
    expect(QUALITY_LADDER.map((s) => s.reduce)).toEqual([1, 0.6, 0.25, 0]);
  });

  it("renders at the canvas size and full detail at the top level", () => {
    expect(renderQuality(3840, 2160, 3)).toEqual({ width: 3840, height: 2160, reduce: 0 });
  });

  it("scales the render size down with the level", () => {
    expect(renderQuality(3840, 2160, 0)).toEqual({ width: 1920, height: 1080, reduce: 1 });
    expect(renderQuality(3840, 2160, 1)).toEqual({ width: 2573, height: 1447, reduce: 0.6 });
  });

  it("clamps the level and never renders below one pixel", () => {
    expect(renderQuality(100, 100, 9).width).toBe(100);
    expect(renderQuality(1, 1, -1)).toEqual({ width: 1, height: 1, reduce: 1 });
  });
});
