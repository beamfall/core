import { beforeEach, describe, expect, it, vi } from "vitest";
import { setIdentityEpochSignal } from "@/lib/api";
import { DEFAULT_BROWSE_STATE, type LibraryItemsRequest } from "./libraryBrowse";
import { nextInPlayQueue, playQueueHandle, startPlayQueue } from "./playQueue";

const fetchLibraryItems = vi.hoisted(() => vi.fn());
vi.mock("./libraryBrowse", async (original) => ({
  ...(await original<typeof import("./libraryBrowse")>()),
  fetchLibraryItems,
}));

const request = { libraryID: "library-1", state: DEFAULT_BROWSE_STATE };

function page(ids: string[], total: number) {
  return {
    total,
    items: ids.map((id) => ({ id, kind: "media", title: `Title ${id}` })),
    facets: { groupings: [], decades: [], qualities: [] },
  };
}

describe("play queue", () => {
  beforeEach(() => {
    setIdentityEpochSignal(undefined);
    fetchLibraryItems.mockReset();
  });

  it("walks the filtered set in order and loads later pages on demand", async () => {
    fetchLibraryItems.mockImplementation(async ({ offset }: LibraryItemsRequest) =>
      offset === 0 ? page(["a", "b"], 3) : page(["c"], 3),
    );

    expect(await startPlayQueue(request)).toEqual({ id: "a", title: "Title a" });
    expect(await nextInPlayQueue("a")).toBe("b");
    expect(await nextInPlayQueue("b")).toBe("c");
    expect(fetchLibraryItems).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 2 }));
    expect(await nextInPlayQueue("c")).toBe("");
  });

  it("ends the queue when playback starts outside it", async () => {
    fetchLibraryItems.mockResolvedValue(page(["a", "b", "c"], 3));
    await startPlayQueue(request);

    expect(await nextInPlayQueue("elsewhere")).toBe("");
    // Returning to a queue item later does not resume the abandoned queue.
    expect(await nextInPlayQueue("a")).toBe("");
  });

  it("keeps a newer queue when an older playback request resumes", async () => {
    // A playback request issued before Play all, still waiting on its probe.
    const issuedFor = playQueueHandle();
    fetchLibraryItems.mockResolvedValue(page(["a", "b", "c"], 3));
    await startPlayQueue(request);

    expect(await nextInPlayQueue("elsewhere", issuedFor)).toBe("");
    expect(await nextInPlayQueue("a")).toBe("b");
  });

  it("clears the queue when the filtered set is empty", async () => {
    fetchLibraryItems.mockResolvedValue(page([], 0));

    expect(await startPlayQueue(request)).toBeNull();
    expect(await nextInPlayQueue("a")).toBe("");
  });

  it("keeps every item when playback shrinks a watch-filtered set", async () => {
    // Unwatched filter: each finished item leaves the server's set.
    let remaining = ["a", "b", "c", "d", "e"];
    fetchLibraryItems.mockImplementation(async ({ offset }: LibraryItemsRequest) =>
      page(remaining.slice(offset, offset + 2), remaining.length),
    );

    expect((await startPlayQueue(request))?.id).toBe("a");
    expect(await nextInPlayQueue("a")).toBe("b");
    remaining = ["c", "d", "e"]; // a and b were watched
    expect(await nextInPlayQueue("b")).toBe("c");
    expect(await nextInPlayQueue("c")).toBe("d");
    remaining = ["e"];
    expect(await nextInPlayQueue("d")).toBe("e");
    expect(await nextInPlayQueue("e")).toBe("");
  });

  it("keeps every item when the set shrinks again between a read and its retry", async () => {
    // b's progress write lands after the first later read, before the retry.
    const sets = [
      ["a", "b", "c", "d", "e"],
      ["b", "c", "d", "e"],
      ["c", "d", "e"],
    ];
    let read = 0;
    fetchLibraryItems.mockImplementation(async ({ offset }: LibraryItemsRequest) => {
      const remaining = sets[Math.min(read++, sets.length - 1)];
      return page(remaining.slice(offset, offset + 2), remaining.length);
    });

    expect((await startPlayQueue(request))?.id).toBe("a");
    expect(await nextInPlayQueue("a")).toBe("b");
    expect(await nextInPlayQueue("b")).toBe("c");
    expect(await nextInPlayQueue("c")).toBe("d");
    expect(await nextInPlayQueue("d")).toBe("e");
    expect(await nextInPlayQueue("e")).toBe("");
  });

  it("does not repeat or skip when a last-watched order moves played items", async () => {
    // Oldest-watched first: a played item moves behind the other watched ones.
    let order = ["a", "b", "c", "d"];
    fetchLibraryItems.mockImplementation(async ({ offset }: LibraryItemsRequest) =>
      page(order.slice(offset, offset + 2), order.length),
    );
    const lastWatched = {
      ...request,
      state: { ...DEFAULT_BROWSE_STATE, sort: "last-watched" as const },
    };

    expect((await startPlayQueue(lastWatched))?.id).toBe("a");
    expect(await nextInPlayQueue("a")).toBe("b");
    order = ["c", "d", "a", "b"];
    expect(await nextInPlayQueue("b")).toBe("c");
    expect(await nextInPlayQueue("c")).toBe("d");
    order = ["a", "b", "c", "d"];
    expect(await nextInPlayQueue("d")).toBe("");
  });

  it("is dropped when the identity changes", async () => {
    const first = new AbortController();
    setIdentityEpochSignal(first.signal);
    fetchLibraryItems.mockResolvedValue(page(["a", "b"], 2));
    await startPlayQueue(request);

    setIdentityEpochSignal(new AbortController().signal);
    expect(await nextInPlayQueue("a")).toBe("");
  });
});
