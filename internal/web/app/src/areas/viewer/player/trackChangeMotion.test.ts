import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  ART_STYLES,
  getTrackChangeMotion,
  motionForChange,
  resetTrackChangeMotionForTest,
  setTrackChangeMotion,
  usePrefersReducedMotion,
  useTrackChangeMotion,
  WORD_STYLES,
} from "./trackChangeMotion";

const KEY = "beamfall.trackChangeMotion";

describe("track-change motion setting (CORE-2331, CORE-2332)", () => {
  beforeEach(() => {
    window.localStorage.clear();
    resetTrackChangeMotionForTest();
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("defaults to mix and keeps a choice in this browser", () => {
    expect(getTrackChangeMotion()).toEqual({ words: "mix", art: "mix" });
    setTrackChangeMotion({ words: "wipe" });
    setTrackChangeMotion({ art: "flip" });
    expect(JSON.parse(window.localStorage.getItem(KEY)!)).toEqual({ words: "wipe", art: "flip" });
    resetTrackChangeMotionForTest();
    expect(getTrackChangeMotion()).toEqual({ words: "wipe", art: "flip" });
    setTrackChangeMotion({ words: "mix", art: "mix" });
    expect(window.localStorage.getItem(KEY)).toBeNull();
  });

  it("reads an unknown or broken stored value as mix", () => {
    window.localStorage.setItem(KEY, JSON.stringify({ words: "explode", art: "slide" }));
    expect(getTrackChangeMotion()).toEqual({ words: "mix", art: "slide" });
    resetTrackChangeMotionForTest();
    window.localStorage.setItem(KEY, "{not json");
    expect(getTrackChangeMotion()).toEqual({ words: "mix", art: "mix" });
  });

  it("still holds a choice for the page when storage is blocked", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    setTrackChangeMotion({ words: "rise" });
    expect(getTrackChangeMotion().words).toBe("rise");
  });

  it("re-renders subscribers when the choice changes", () => {
    const { result } = renderHook(() => useTrackChangeMotion());
    act(() => setTrackChangeMotion({ art: "dissolve" }));
    expect(result.current.art).toBe("dissolve");
  });

  it("mixes per item: stable for one item, varied across items", () => {
    const mix = { words: "mix", art: "mix" } as const;
    const first = motionForChange("item-1", mix, false);
    expect(motionForChange("item-1", mix, false)).toEqual(first);
    const picks = new Set(
      Array.from({ length: 40 }, (_, i) =>
        JSON.stringify(motionForChange(`item-${i}`, mix, false)),
      ),
    );
    expect(picks.size).toBeGreaterThanOrEqual(3);
    for (const pick of picks) {
      const { words, art } = JSON.parse(pick);
      expect(WORD_STYLES).toContain(words);
      expect(ART_STYLES).toContain(art);
    }
  });

  it("plays a fixed choice as chosen, and only a crossfade under reduced motion", () => {
    expect(motionForChange("x", { words: "flap", art: "zoom" }, false)).toEqual({
      words: "flap",
      art: "zoom",
    });
    expect(motionForChange("x", { words: "flap", art: "mix" }, false).words).toBe("flap");
    expect(motionForChange("x", { words: "flap", art: "zoom" }, true)).toEqual({
      words: "fade",
      art: "fade",
    });
  });

  it("follows the reduced-motion preference as it changes", () => {
    let listener: () => void = () => {};
    const query = {
      matches: false,
      addEventListener: (_: string, l: () => void) => {
        listener = l;
      },
      removeEventListener: vi.fn(),
    };
    vi.stubGlobal(
      "matchMedia",
      vi.fn(() => query),
    );
    const { result } = renderHook(() => usePrefersReducedMotion());
    expect(result.current).toBe(false);
    query.matches = true;
    act(() => listener());
    expect(result.current).toBe(true);
  });
});
