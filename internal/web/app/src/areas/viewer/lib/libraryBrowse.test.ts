import { afterEach, describe, expect, it, vi } from "vitest";
import { libraryViewClass, readLibraryViewMode, writeLibraryViewMode } from "./libraryBrowse";

afterEach(() => {
  vi.restoreAllMocks();
  window.localStorage.clear();
});

describe("library view mode (CORE-2386)", () => {
  it("remembers the layout per library", () => {
    writeLibraryViewMode("tv", "thumbnail");
    writeLibraryViewMode("films", "list");
    expect(readLibraryViewMode("tv")).toBe("thumbnail");
    expect(readLibraryViewMode("films")).toBe("list");
    expect(readLibraryViewMode("music")).toBe("poster");
  });

  it("reads the earlier grid choice, or anything unknown, as posters", () => {
    window.localStorage.setItem("beamfall.libraryView.tv", "grid");
    expect(readLibraryViewMode("tv")).toBe("poster");
    window.localStorage.setItem("beamfall.libraryView.tv", "mosaic");
    expect(readLibraryViewMode("tv")).toBe("poster");
  });

  it("falls back to posters when storage is blocked", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => writeLibraryViewMode("tv", "list")).not.toThrow();
    expect(readLibraryViewMode("tv")).toBe("poster");
  });

  it("draws each layout with its own list class", () => {
    expect(libraryViewClass("poster")).toBe("bf-grid");
    expect(libraryViewClass("thumbnail")).toBe("bf-grid bf-grid-wide");
    expect(libraryViewClass("list")).toBe("bf-stack bf-rows");
  });
});
