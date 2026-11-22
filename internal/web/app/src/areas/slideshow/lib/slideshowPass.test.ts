import { describe, expect, it } from "vitest";
import {
  chooseLayout,
  packSlideshowBlock,
  SLIDESHOW_BLOCK_FLOATS,
  slideProgress,
  transitionFor,
  transitionProgress,
} from "./slideshowPass";

// Expected blocks are the output of Oscillux v0.2.15's reference packer, slideshowBlock in
// tools/shader/slideshow.mjs, for the same named fields.
describe("packSlideshowBlock", () => {
  it("packs a transition field for field as slideshow.mjs", () => {
    const block = packSlideshowBlock({
      p: 0.25,
      style: "lightSweep",
      aspect: 16 / 9,
      motion: 1,
      width: 1920,
      height: 1080,
      a: { k: 0.5, layout: "fill", direction: 1, aspect0: 2 / 3, beats: 1 },
      b: {
        k: 0.1,
        layout: "pair",
        direction: 0,
        aspect0: 0.75,
        aspect1: 0.8,
        rect0: [0.1, 0.2, 0.3, 0.4],
        rect1: [0.5, 0.5, 0.6, 0.7],
      },
    });
    expect(block.byteLength).toBe(176);
    // biome-ignore format: one std140 vec4 per row
    const expected = [
      0.25, 1, 16 / 9, 1, // pass0
      0, 0, 0, 0, // pass1
      1920, 1080, 0, 0, // size
      0.5, 1, 1, 2 / 3, 2 / 3, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // slideA, extraA, rectA0, rectA1
      0.1, 2, 0, 0.75, 0.8, 1, 0, 0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.5, 0.6, 0.7, // slideB .. rectB1
    ];
    expect(Array.from(block)).toEqual(Array.from(new Float32Array(expected)));
  });

  it("repeats the showing slide as b between transitions, with unknown rects", () => {
    const block = packSlideshowBlock({
      style: "iris",
      aspect: 0.5,
      motion: 0,
      width: 540,
      height: 1080,
      a: { k: 0.9, layout: "cover", aspect0: 1.5 },
    });
    expect(Array.from(block)).toEqual(
      Array.from(
        new Float32Array([
          0, 2, 0.5, 0, 0, 0, 0, 0, 540, 1080, 0, 0, 0.9, 0, 0, 1.5, 1.5, 1, 0, 0, 0, 0, 0, 0, 0, 0,
          0, 0, 0.9, 0, 0, 1.5, 1.5, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
        ]),
      ),
    );
  });

  it("writes into a reused block", () => {
    const out = new Float32Array(SLIDESHOW_BLOCK_FLOATS).fill(9);
    expect(packSlideshowBlock({ aspect: 1, width: 1, height: 1, a: {} }, out)).toBe(out);
    expect(out[1]).toBe(0);
    expect(out[13]).toBe(0);
  });
});

describe("host timing and layout", () => {
  it("pairs two photos, fills a far-off aspect and covers the rest", () => {
    expect(chooseLayout([0.66, 0.75], 16 / 9)).toBe("pair");
    expect(chooseLayout([3 / 2], 16 / 9)).toBe("cover");
    expect(chooseLayout([2 / 3], 16 / 9)).toBe("fill");
    expect(chooseLayout([16 / 9], 9 / 16)).toBe("fill");
  });

  it("rotates the three styles at their durations, and dissolves under Reduce Motion", () => {
    expect([0, 1, 2, 3].map((n) => transitionFor(n, false))).toEqual([
      { style: "dissolve", durationMs: 1500 },
      { style: "lightSweep", durationMs: 1800 },
      { style: "iris", durationMs: 1600 },
      { style: "dissolve", durationMs: 1500 },
    ]);
    expect(transitionFor(1, true)).toEqual({ style: "dissolve", durationMs: 1000 });
  });

  it("eases p once with smoothstep and runs k linearly, both clamped", () => {
    expect(transitionProgress(-5, 1000)).toBe(0);
    expect(transitionProgress(250, 1000)).toBeCloseTo(0.15625);
    expect(transitionProgress(500, 1000)).toBe(0.5);
    expect(transitionProgress(2000, 1000)).toBe(1);
    expect(slideProgress(3000, 12000)).toBe(0.25);
    expect(slideProgress(20000, 12000)).toBe(1);
  });
});
