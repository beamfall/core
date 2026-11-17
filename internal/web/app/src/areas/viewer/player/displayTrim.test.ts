import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  DISPLAY_TRIM_STEP_MS,
  getDisplayTrimMS,
  MAX_DISPLAY_TRIM_MS,
  resetDisplayTrimForTest,
  setDisplayTrimMS,
  useDisplayTrimMS,
} from "./displayTrim";

const KEY = "beamfall.followerDisplayTrimMS";

beforeEach(() => {
  window.localStorage.clear();
  resetDisplayTrimForTest();
});

afterEach(() => {
  vi.restoreAllMocks();
  window.localStorage.clear();
  resetDisplayTrimForTest();
});

describe("display delay trim (CORE-2245)", () => {
  it("is zero until this screen sets one", () => {
    expect(getDisplayTrimMS()).toBe(0);
    expect(window.localStorage.getItem(KEY)).toBeNull();
  });

  it("keeps the trim in this browser", () => {
    setDisplayTrimMS(3 * DISPLAY_TRIM_STEP_MS);
    expect(window.localStorage.getItem(KEY)).toBe("30");
    // A reload reads it back.
    resetDisplayTrimForTest();
    expect(getDisplayTrimMS()).toBe(30);

    setDisplayTrimMS(-20);
    resetDisplayTrimForTest();
    expect(getDisplayTrimMS()).toBe(-20);
  });

  it("removes the stored trim on reset", () => {
    setDisplayTrimMS(40);
    setDisplayTrimMS(0);
    expect(window.localStorage.getItem(KEY)).toBeNull();
    expect(getDisplayTrimMS()).toBe(0);
  });

  it("clamps and rounds what it is given", () => {
    setDisplayTrimMS(9000);
    expect(getDisplayTrimMS()).toBe(MAX_DISPLAY_TRIM_MS);
    setDisplayTrimMS(-9000);
    expect(getDisplayTrimMS()).toBe(-MAX_DISPLAY_TRIM_MS);
    setDisplayTrimMS(12.6);
    expect(getDisplayTrimMS()).toBe(13);
    setDisplayTrimMS(Number.NaN);
    expect(getDisplayTrimMS()).toBe(0);
  });

  it.each([
    ["not a number", 0],
    ["1e9", MAX_DISPLAY_TRIM_MS],
    ["-1e9", -MAX_DISPLAY_TRIM_MS],
    ["", 0],
  ])("reads a stored %j as %d", (stored, want) => {
    window.localStorage.setItem(KEY, stored);
    expect(getDisplayTrimMS()).toBe(want);
  });

  it("works for the page when storage is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });

    expect(getDisplayTrimMS()).toBe(0);
    expect(() => setDisplayTrimMS(50)).not.toThrow();
    expect(getDisplayTrimMS()).toBe(50);
    expect(() => setDisplayTrimMS(0)).not.toThrow();
    expect(getDisplayTrimMS()).toBe(0);
  });

  it("re-renders its readers when the trim changes", () => {
    const view = renderHook(() => useDisplayTrimMS());
    expect(view.result.current).toBe(0);
    act(() => setDisplayTrimMS(-70));
    expect(view.result.current).toBe(-70);
    view.unmount();
    // A reader that is gone is not told.
    expect(() => setDisplayTrimMS(10)).not.toThrow();
  });
});
