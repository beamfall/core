import { afterEach, describe, expect, it } from "vitest";
import { clampSync, formatSync, readSync, writeSync } from "./playbackSync";

afterEach(() => window.localStorage.clear());

describe("clampSync", () => {
  it("rounds to tenths and clamps into the range", () => {
    expect(clampSync(0.1 + 0.2, -5, 5)).toBe(0.3);
    expect(clampSync(7.24, -5, 5)).toBe(5);
    expect(clampSync(-9, -5, 5)).toBe(-5);
  });

  it("maps non-finite input and negative zero to zero", () => {
    expect(clampSync(Number.NaN, -5, 5)).toBe(0);
    expect(Object.is(clampSync(-0.01, -5, 5), 0)).toBe(true);
  });
});

describe("formatSync", () => {
  it("signs non-zero values with a real minus sign", () => {
    expect(formatSync(0.3)).toBe("+0.3 s");
    expect(formatSync(-1.2)).toBe("−1.2 s");
    expect(formatSync(0)).toBe("0.0 s");
    expect(formatSync(-0.04)).toBe("0.0 s");
  });
});

describe("readSync / writeSync", () => {
  it("persists per Profile and Media", () => {
    writeSync("p1", "m1", { subtitleOffset: 1.5 });
    writeSync("p1", "m1", { audioDelay: -0.2 });
    expect(readSync("p1", "m1")).toEqual({ subtitleOffset: 1.5, audioDelay: -0.2 });
    expect(readSync("p2", "m1")).toEqual({ subtitleOffset: 0, audioDelay: 0 });
    expect(readSync("p1", "m2")).toEqual({ subtitleOffset: 0, audioDelay: 0 });
  });

  it("removes an all-zero entry", () => {
    writeSync("p1", "m1", { subtitleOffset: 2 });
    writeSync("p1", "m1", { subtitleOffset: 0 });
    expect(window.localStorage.getItem("bf.playbackSync.p1.m1")).toBeNull();
  });

  it("does nothing without both ids", () => {
    writeSync("", "m1", { subtitleOffset: 2 });
    expect(window.localStorage.length).toBe(0);
    expect(readSync("", "m1").subtitleOffset).toBe(0);
  });

  it("clamps stored values and ignores garbage", () => {
    window.localStorage.setItem(
      "bf.playbackSync.p1.m1",
      JSON.stringify({ subtitleOffset: 999, audioDelay: -99 }),
    );
    expect(readSync("p1", "m1")).toEqual({ subtitleOffset: 60, audioDelay: -5 });
    window.localStorage.setItem("bf.playbackSync.p1.m1", "{not json");
    expect(readSync("p1", "m1")).toEqual({ subtitleOffset: 0, audioDelay: 0 });
  });
});
