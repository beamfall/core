import { beforeEach, describe, expect, it, vi } from "vitest";
import { setIdentityEpochSignal } from "@/lib/api";
import { DEFAULT_BROWSE_STATE, type LibraryItemsRequest } from "./libraryBrowse";
import { nextInPlayQueue, playQueueHandle, playQueueTitle, startPlayQueue } from "./playQueue";

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

  it("leaves out items a locked ceiling blocks, on the first page and later ones", async () => {
    const locked = (id: string) => ({ id, kind: "media", title: `Title ${id}`, locked: true });
    fetchLibraryItems.mockImplementation(async ({ offset }: LibraryItemsRequest) =>
      offset === 0
        ? { ...page(["b"], 5), items: [locked("a"), ...page(["b"], 5).items, locked("c")] }
        : { ...page(["e"], 5), items: [locked("d"), ...page(["e"], 5).items] },
    );

    // The first item is locked: the queue opens on the first playable one.
    expect(await startPlayQueue(request)).toEqual({ id: "b", title: "Title b" });
    // The locked items between b and e never become the declared next item.
    expect(await nextInPlayQueue("b")).toBe("e");
    expect(fetchLibraryItems).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 3 }));
    expect(await nextInPlayQueue("e")).toBe("");
  });

  it("starts on a later page when the first holds nothing playable", async () => {
    fetchLibraryItems.mockImplementation(async ({ offset }: LibraryItemsRequest) =>
      offset === 0
        ? { ...page([], 2), items: [{ id: "a", kind: "media", title: "Title a", locked: true }] }
        : page(["b"], 2),
    );

    expect(await startPlayQueue(request)).toEqual({ id: "b", title: "Title b" });
    expect(await nextInPlayQueue("b")).toBe("");
  });

  it("plays nothing when every item in the set is locked", async () => {
    fetchLibraryItems.mockResolvedValue({
      ...page([], 1),
      items: [{ id: "a", kind: "media", title: "Title a", locked: true }],
    });

    expect(await startPlayQueue(request)).toBeNull();
    expect(playQueueHandle()).toBeNull();
  });

  it("keeps the titles of queued items for the player", async () => {
    fetchLibraryItems.mockImplementation(async ({ offset }: LibraryItemsRequest) =>
      offset === 0 ? page(["a", "b"], 3) : page(["c"], 3),
    );
    await startPlayQueue(request);

    expect(playQueueTitle("b")).toBe("Title b");
    expect(playQueueTitle("c")).toBe("");
    await nextInPlayQueue("a");
    await nextInPlayQueue("b");
    expect(playQueueTitle("c")).toBe("Title c");
    expect(playQueueTitle("elsewhere")).toBe("");
  });

  it("reads a last-watched set whole at the start and never again", async () => {
    // Playing reorders a last-watched set, and each progress write lands on its
    // own schedule, so the queue walks the set as it stood at Play all.
    let order = ["a", "b", "c", "d", "e", "f"];
    fetchLibraryItems.mockImplementation(async ({ offset }: LibraryItemsRequest) =>
      page(order.slice(offset, offset + 2), order.length),
    );
    await startPlayQueue({ ...request, state: { ...DEFAULT_BROWSE_STATE, sort: "last-watched" } });
    const reads = fetchLibraryItems.mock.calls.length;

    expect(await nextInPlayQueue("a")).toBe("b");
    // a's write has landed, b's has not.
    order = ["b", "c", "d", "e", "f", "a"];
    expect(await nextInPlayQueue("b")).toBe("c");
    // b's and c's writes land together before the next read.
    order = ["d", "e", "f", "a", "b", "c"];
    expect(await nextInPlayQueue("c")).toBe("d");
    expect(await nextInPlayQueue("d")).toBe("e");
    expect(await nextInPlayQueue("e")).toBe("f");
    expect(await nextInPlayQueue("f")).toBe("");
    expect(fetchLibraryItems.mock.calls.length).toBe(reads);
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

  it("starts nothing once its caller has gone", async () => {
    // The viewer left the browse page while the first read was in flight.
    const left = new AbortController();
    fetchLibraryItems.mockImplementation(async () => {
      left.abort();
      return page(["a", "b"], 2);
    });

    expect(await startPlayQueue(request, left.signal)).toBeNull();
    expect(playQueueHandle()).toBeNull();
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
