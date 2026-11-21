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

  it("falls back to Recommended for a node with nothing remembered", () => {
    expect(readLibraryPivot("home")).toBe("");
  });
});
