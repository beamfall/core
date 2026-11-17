import { describe, expect, it } from "vitest";
import type { LibraryHealthInfo } from "@/lib/graphql/types";
import { healthAttention, libraryFoldersLink, unmatchedTotal } from "./overviewAttention";

function health(counts: Record<string, number>): LibraryHealthInfo {
  return { counts } as unknown as LibraryHealthInfo;
}

describe("overviewAttention (CORE-2310)", () => {
  it("sums each health dimension over every Library and keeps only non-zero ones", () => {
    const rows = healthAttention([
      health({ unmatched: 4, broken: 0, duplicate: 9 }),
      health({ unmatched: 1, "scrape-failure": 2 }),
    ]);
    expect(rows.map((row) => [row.dimension, row.count, row.to])).toEqual([
      ["unmatched", 5, "/manage/unmatched"],
      ["scrape-failure", 2, "/console/health"],
    ]);
  });

  it("reports nothing while health is unknown", () => {
    expect(healthAttention(null)).toEqual([]);
    expect(unmatchedTotal(null)).toBeNull();
  });

  it("counts every unmatched item, not one page of the queue", () => {
    expect(unmatchedTotal([health({ unmatched: 180 }), health({ unmatched: 70 })])).toBe(250);
    expect(unmatchedTotal([])).toBe(0);
  });

  it("links to one Library's folders (CORE-2309)", () => {
    expect(libraryFoldersLink("lib-1")).toBe("/console/libraries#folders-lib-1");
  });
});
