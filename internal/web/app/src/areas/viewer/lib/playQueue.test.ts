import { beforeEach, describe, expect, it, vi } from "vitest";
import { DEFAULT_BROWSE_STATE, type LibraryItemsRequest } from "./libraryBrowse";
import { nextInPlayQueue, startPlayQueue } from "./playQueue";

const fetchLibraryItems = vi.hoisted(() => vi.fn());
vi.mock("./libraryBrowse", async (original) => ({
  ...(await original<typeof import("./libraryBrowse")>()),
  fetchLibraryItems,
}));

const request = { libraryID: "library-1", state: DEFAULT_BROWSE_STATE, genreKind: "genre" };

function page(ids: string[], total: number) {
  return {
    total,
    items: ids.map((id) => ({ id, kind: "media", title: id })),
    facets: { entities: [], decades: [], qualities: [] },
  };
}

describe("play queue", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    fetchLibraryItems.mockReset();
  });

  it("walks the filtered set in order and loads later pages on demand", async () => {
    fetchLibraryItems.mockImplementation(async ({ offset }: LibraryItemsRequest) =>
      offset === 0 ? page(["a", "b"], 3) : page(["c"], 3),
    );

    expect(await startPlayQueue(request)).toBe("a");
    expect(await nextInPlayQueue("a")).toBe("b");
    expect(await nextInPlayQueue("b")).toBe("c");
    expect(fetchLibraryItems).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 2 }));
    expect(await nextInPlayQueue("c")).toBe("");
  });

  it("offers no successor for playback started outside the queue", async () => {
    fetchLibraryItems.mockResolvedValue(page(["a", "b"], 2));
    await startPlayQueue(request);

    expect(await nextInPlayQueue("elsewhere")).toBe("");
  });

  it("clears the queue when the filtered set is empty", async () => {
    fetchLibraryItems.mockResolvedValue(page([], 0));

    expect(await startPlayQueue(request)).toBe("");
    expect(await nextInPlayQueue("a")).toBe("");
  });
});
