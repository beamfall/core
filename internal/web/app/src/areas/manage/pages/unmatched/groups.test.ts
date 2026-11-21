import { describe, expect, it } from "vitest";
import type { UnmatchedItemInfo } from "@/lib/graphql/types";
import { folderHint, groupUnmatched, parseFolderName } from "./groups";

function row(over: Partial<UnmatchedItemInfo>): UnmatchedItemInfo {
  return {
    mediaID: "m",
    title: "t",
    pluginID: "p",
    scraperID: "s",
    confidence: 0.5,
    threshold: 0.75,
    ...over,
  };
}

describe("groupUnmatched", () => {
  it("folds rows sharing source and candidate, largest group first", () => {
    const groups = groupUnmatched([
      row({ mediaID: "solo", candidateID: "x", failureReason: "below-threshold" }),
      row({ mediaID: "a", candidateID: "c1", failureReason: "ambiguous", confidence: 0.8 }),
      row({ mediaID: "b", candidateID: "c1", failureReason: "ambiguous", confidence: 0.9 }),
      row({ mediaID: "c", candidateID: "c1", failureReason: "below-threshold" }),
    ]);
    expect(groups.map((g) => g.items.map((i) => i.mediaID))).toEqual([["a", "b", "c"], ["solo"]]);
    expect(groups[0].reason).toBe("ambiguous");
    expect(groups[0].confidence).toBe(0.9);
  });

  it("groups by candidate title without an id, and never merges rows with no guess", () => {
    const groups = groupUnmatched([
      row({ mediaID: "a", candidateTitle: "Name" }),
      row({ mediaID: "b", candidateTitle: "Name" }),
      row({ mediaID: "c" }),
      row({ mediaID: "d" }),
      row({ mediaID: "e", candidateTitle: "Name", scraperID: "other" }),
    ]);
    expect(groups.map((g) => g.items.length)).toEqual([2, 1, 1, 1]);
  });
});

describe("parseFolderName", () => {
  it("reads a bracketed year", () => {
    expect(parseFolderName("The Outsider (2020)")).toEqual({
      folder: "The Outsider (2020)",
      title: "The Outsider",
      year: 2020,
      qualifier: undefined,
    });
  });

  it("reads a qualifier and a dotted trailing year", () => {
    expect(parseFolderName("The Office (US)")).toMatchObject({
      title: "The Office",
      qualifier: "US",
    });
    expect(parseFolderName("Some.Name.1999")).toMatchObject({ title: "Some Name", year: 1999 });
    expect(parseFolderName("Name [UK] (2005) {tmdb-123}")).toMatchObject({
      title: "Name",
      year: 2005,
      qualifier: "UK",
    });
  });
});

describe("folderHint", () => {
  it("skips numbered subfolders up to the folder that names the entry", () => {
    expect(folderHint("/data/lib/The Outsider (2020)/Disc 01/part.mkv")).toMatchObject({
      title: "The Outsider",
      year: 2020,
    });
    expect(folderHint("/data/lib/The Office (US)/S02/file.mkv")).toMatchObject({
      title: "The Office",
      qualifier: "US",
    });
  });

  it("prefers a year-bearing folder even when it is numbered, and handles bare paths", () => {
    expect(folderHint("/lib/Alien 3 (1992)/file.mkv")).toMatchObject({
      title: "Alien 3",
      year: 1992,
    });
    expect(folderHint("file.mkv")).toBeUndefined();
    expect(folderHint(undefined)).toBeUndefined();
  });
  it("keeps the nearest folder over a dated grandparent", () => {
    expect(folderHint("/archive/Collection (1990)/North (US)/part.mkv")).toMatchObject({
      title: "North",
      qualifier: "US",
      year: undefined,
    });
  });

  it("keeps a numbered title folder unless its parent is dated", () => {
    expect(folderHint("/data/Alien 3/file.mkv")).toMatchObject({ title: "Alien 3" });
    expect(folderHint("/data/Heat 2/Extras 1/file.mkv")).toMatchObject({ title: "Extras 1" });
    expect(folderHint("/data/Heat (1995)/Extras 1/file.mkv")).toMatchObject({
      title: "Heat",
      year: 1995,
    });
  });

  it("climbs past a numbered folder under a qualified parent or a sibling marker", () => {
    expect(folderHint("/media/Northwind (US)/Season 2/file.mkv")).toMatchObject({
      title: "Northwind",
      qualifier: "US",
    });
    expect(folderHint("/media/Northwind/Book 2/file.mkv")).toMatchObject({ title: "Northwind" });
  });
});
