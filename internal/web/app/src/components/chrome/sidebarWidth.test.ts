import { afterEach, describe, expect, it, vi } from "vitest";
import {
  clampSidebarWidth,
  readSidebarWidth,
  SIDEBAR_WIDTH_DEFAULT,
  SIDEBAR_WIDTH_MAX,
  SIDEBAR_WIDTH_MIN,
  SIDEBAR_WIDTH_PREF,
  sidebarWidthForKey,
  writeSidebarWidth,
} from "./sidebarWidth";

afterEach(() => {
  vi.restoreAllMocks();
  window.localStorage.clear();
});

describe("clampSidebarWidth", () => {
  it("holds widths within the limits and rounds to whole pixels", () => {
    expect(clampSidebarWidth(10)).toBe(SIDEBAR_WIDTH_MIN);
    expect(clampSidebarWidth(900)).toBe(SIDEBAR_WIDTH_MAX);
    expect(clampSidebarWidth(250.6)).toBe(251);
    expect(clampSidebarWidth(SIDEBAR_WIDTH_MIN)).toBe(SIDEBAR_WIDTH_MIN);
    expect(clampSidebarWidth(SIDEBAR_WIDTH_MAX)).toBe(SIDEBAR_WIDTH_MAX);
  });

  it("reads anything non-finite as the default", () => {
    expect(clampSidebarWidth(Number.NaN)).toBe(SIDEBAR_WIDTH_DEFAULT);
    expect(clampSidebarWidth(Number.POSITIVE_INFINITY)).toBe(SIDEBAR_WIDTH_DEFAULT);
  });

  it("keeps the default narrower than the old fixed 236px and inside the limits", () => {
    expect(SIDEBAR_WIDTH_DEFAULT).toBeLessThan(236);
    expect(clampSidebarWidth(SIDEBAR_WIDTH_DEFAULT)).toBe(SIDEBAR_WIDTH_DEFAULT);
  });
});

describe("sidebarWidthForKey", () => {
  it("steps, jumps to the limits and resets", () => {
    expect(sidebarWidthForKey("ArrowRight", 200)).toBe(216);
    expect(sidebarWidthForKey("ArrowLeft", 200)).toBe(184);
    expect(sidebarWidthForKey("ArrowLeft", SIDEBAR_WIDTH_MIN)).toBe(SIDEBAR_WIDTH_MIN);
    expect(sidebarWidthForKey("ArrowRight", SIDEBAR_WIDTH_MAX)).toBe(SIDEBAR_WIDTH_MAX);
    expect(sidebarWidthForKey("Home", 300)).toBe(SIDEBAR_WIDTH_MIN);
    expect(sidebarWidthForKey("End", 200)).toBe(SIDEBAR_WIDTH_MAX);
    expect(sidebarWidthForKey("Enter", 300)).toBe(SIDEBAR_WIDTH_DEFAULT);
    expect(sidebarWidthForKey("a", 300)).toBeNull();
  });
});

describe("sidebar width persistence", () => {
  it("round-trips a chosen width", () => {
    writeSidebarWidth(280);
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_PREF)).toBe("280");
    expect(readSidebarWidth()).toBe(280);
  });

  it("stores a clamped width and clamps a stored one", () => {
    writeSidebarWidth(9000);
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_PREF)).toBe(String(SIDEBAR_WIDTH_MAX));
    window.localStorage.setItem(SIDEBAR_WIDTH_PREF, "900");
    expect(readSidebarWidth()).toBe(SIDEBAR_WIDTH_MAX);
    window.localStorage.setItem(SIDEBAR_WIDTH_PREF, "12");
    expect(readSidebarWidth()).toBe(SIDEBAR_WIDTH_MIN);
  });

  it("falls back to the default for missing or invalid values", () => {
    expect(readSidebarWidth()).toBe(SIDEBAR_WIDTH_DEFAULT);
    for (const bad of ["", "  ", "wide", "NaN"]) {
      window.localStorage.setItem(SIDEBAR_WIDTH_PREF, bad);
      expect(readSidebarWidth()).toBe(SIDEBAR_WIDTH_DEFAULT);
    }
  });

  it("clears the entry when the width returns to the default", () => {
    writeSidebarWidth(280);
    writeSidebarWidth(SIDEBAR_WIDTH_DEFAULT);
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_PREF)).toBeNull();
  });

  it("survives a storage that throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    expect(readSidebarWidth()).toBe(SIDEBAR_WIDTH_DEFAULT);
    expect(() => writeSidebarWidth(280)).not.toThrow();
  });
});
