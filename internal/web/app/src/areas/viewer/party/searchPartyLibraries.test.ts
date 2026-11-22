import { describe, expect, it, vi } from "vitest";
import { type SearchResultItem, searchResult } from "@/lib/graphql";
import { searchPartyLibraries } from "./HostLive";

vi.mock("@/lib/graphql", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/graphql")>();
  return { ...actual, searchResult: vi.fn() };
});

function hit(id: string, libraryID: string): SearchResultItem {
  return { id, kind: "media", title: id, libraryID } as SearchResultItem;
}

describe("searchPartyLibraries", () => {
  it("pages past other Libraries' hits to find matches in the party's Library", async () => {
    vi.mocked(searchResult)
      .mockResolvedValueOnce({
        mode: "keyword",
        hasMore: true,
        restrictedResultsPresent: false,
        items: Array.from({ length: 200 }, (_, i) => hit(`movie-${i}`, "movies")),
      })
      .mockResolvedValueOnce({
        mode: "keyword",
        hasMore: false,
        restrictedResultsPresent: false,
        items: [hit("song", "karaoke"), hit("movie-x", "movies")],
      });

    const items = await searchPartyLibraries(
      "lanterns",
      new Set(["karaoke"]),
      new AbortController().signal,
    );

    expect(items.map((item) => item.id)).toEqual(["song"]);
    expect(searchResult).toHaveBeenNthCalledWith(1, "lanterns", "keyword", expect.anything(), {
      offset: 0,
      limit: 200,
    });
    expect(searchResult).toHaveBeenNthCalledWith(2, "lanterns", "keyword", expect.anything(), {
      offset: 200,
      limit: 200,
    });
  });
});
