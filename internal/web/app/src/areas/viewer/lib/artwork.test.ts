import { describe, expect, it } from "vitest";
import { artworkBucketURL, artworkSrcSet } from "./artwork";

describe("artworkBucketURL", () => {
  it("appends the bucket query, joining an existing query with &", () => {
    expect(artworkBucketURL("/artwork/a", "w320")).toBe("/artwork/a?bucket=w320");
    expect(artworkBucketURL("/artwork/a?v=2", "w1280")).toBe("/artwork/a?v=2&bucket=w1280");
  });

  it("returns empty for missing artwork", () => {
    expect(artworkBucketURL("", "w320")).toBe("");
  });
});

describe("artworkSrcSet", () => {
  it("lists each bucket with its width descriptor", () => {
    expect(artworkSrcSet("/artwork/a", ["w320", "w640"])).toBe(
      "/artwork/a?bucket=w320 320w, /artwork/a?bucket=w640 640w",
    );
    expect(artworkSrcSet("/artwork/a?v=2", ["w640", "w1280"])).toBe(
      "/artwork/a?v=2&bucket=w640 640w, /artwork/a?v=2&bucket=w1280 1280w",
    );
  });

  it("returns empty for missing artwork", () => {
    expect(artworkSrcSet("", ["w320", "w640"])).toBe("");
  });
});
