import { afterEach, describe, expect, it } from "vitest";
import { readLibraryPivot, writeLibraryPivot } from "./libraryPivot";

describe("remembered Library pivot", () => {
  afterEach(() => window.localStorage.clear());

  it("reads a pivot remembered under the node's old doubled ID (CORE-2343)", () => {
    window.localStorage.setItem("beamfall.libraryPivot.library-library-films", "/all");
    expect(readLibraryPivot("library-films")).toBe("/all");
  });

  it("prefers the pivot under the current ID", () => {
    window.localStorage.setItem("beamfall.libraryPivot.library-library-films", "/all");
    writeLibraryPivot("library-films", "/collections");
    expect(readLibraryPivot("library-films")).toBe("/collections");
  });

  it("keeps two Libraries' pivots separate when one ID matches the other's old node ID", () => {
    window.localStorage.setItem("beamfall.libraryPivot.library-library-films", "/all");
    expect(readLibraryPivot("library-library-films")).toBe("");
    writeLibraryPivot("library-library-films", "/feeds");
    expect(readLibraryPivot("library-films")).toBe("/all");
    expect(readLibraryPivot("library-library-films")).toBe("/feeds");
  });

  it("falls back to Recommended for a node with nothing remembered", () => {
    expect(readLibraryPivot("home")).toBe("");
  });
});
