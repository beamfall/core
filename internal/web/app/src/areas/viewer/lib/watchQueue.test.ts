import { afterEach, describe, expect, it, vi } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { unavailableReasonMessage } from "./playback";
import {
  dispatchAddToQueueToActivePlayer,
  fullCatalogQueueFor,
  listsPlaysAlone,
  newStartID,
  pinActiveCatalogQueue,
  playQueueOf,
  queueForPlaying,
  queueNeighbours,
  queueOfClass,
  queueWindow,
  readPlayerNavState,
  rememberFullCatalogQueue,
  resetPlayNextForTest,
  sectionQueue,
  setActivePlayingMediaID,
  setActivePlayNextHandler,
  windowedQueueState,
  withQueueClasses,
} from "./watchQueue";

const item = (id: string, kind: string) =>
  ({ id, title: `T-${id}`, kind }) as unknown as SourceItem;

describe("watchQueue (CORE-1143)", () => {
  it("keeps only playable Media items, in order", () => {
    expect(playQueueOf([item("a", "media"), item("e", "entity"), item("b", "media")])).toEqual([
      { id: "a", title: "T-a" },
      { id: "b", title: "T-b" },
    ]);
  });

  it("steps over locked items, as the Play all queue does", () => {
    const locked = { ...item("l", "media"), locked: true } as unknown as SourceItem;
    expect(playQueueOf([item("a", "media"), locked, item("b", "media")])).toEqual([
      { id: "a", title: "T-a" },
      { id: "b", title: "T-b" },
    ]);
  });

  it("windows a huge queue around the selected item so history state stays small", () => {
    const queue = playQueueOf(Array.from({ length: 900 }, (_, i) => item(`m${i}`, "media")));
    expect(queue).toHaveLength(900);

    const deep = queueWindow(queue, "m600");
    expect(deep).toHaveLength(500);
    expect(deep[0].id).toBe("m350");
    expect(queueNeighbours(deep, "m600")).toEqual({ previous: queue[599], next: queue[601] });

    expect(queueWindow(queue, "m10")[0].id).toBe("m0");
    expect(queueWindow(queue, "m899").at(-1)?.id).toBe("m899");
    expect(queueWindow(queue, "missing")[0].id).toBe("m0");
    expect(queueWindow(queue.slice(0, 3), "m1")).toEqual(queue.slice(0, 3));
  });

  it("finds the neighbours either side, and none outside the queue", () => {
    const queue = [
      { id: "a", title: "A" },
      { id: "b", title: "B" },
      { id: "c", title: "C" },
    ];
    expect(queueNeighbours(queue, "b")).toEqual({ previous: queue[0], next: queue[2] });
    expect(queueNeighbours(queue, "a")).toEqual({ previous: undefined, next: queue[1] });
    expect(queueNeighbours(queue, "z")).toEqual({});
    expect(queueNeighbours(undefined, "a")).toEqual({});
  });

  it("reads untyped history state defensively", () => {
    expect(readPlayerNavState(null, "v")).toEqual({});
    expect(readPlayerNavState("x", "v")).toEqual({});
    expect(
      readPlayerNavState(
        { title: 4, queue: [{ id: "a", title: "A" }, { id: 1 }, null, "b"], queueIdentity: "v" },
        "v",
      ),
    ).toEqual({ title: undefined, queue: [{ id: "a", title: "A" }], queueIdentity: "v" });
    expect(readPlayerNavState({ title: "Song" }, "v")).toEqual({ title: "Song", queue: undefined });
  });

  it("drops a queue listed for another viewer identity, or with none", () => {
    const queue = [{ id: "a", title: "A" }];
    expect(
      readPlayerNavState({ queue, queueIdentity: "revealed" }, "locked").queue,
    ).toBeUndefined();
    expect(readPlayerNavState({ queue }, "v").queue).toBeUndefined();
    expect(readPlayerNavState({ queue, queueIdentity: "v" }, null).queue).toBeUndefined();
  });
});

describe("start tracks in nav state (CORE-2442)", () => {
  it("carries audio and caption picks for their Media, with Off as null", () => {
    expect(
      readPlayerNavState(
        { tracks: { mediaID: "m1", audioIndex: 2, captionID: "muxed-main-3" } },
        "v",
      ).tracks,
    ).toEqual({ mediaID: "m1", audioIndex: 2, captionID: "muxed-main-3" });
    expect(readPlayerNavState({ tracks: { mediaID: "m1", captionID: null } }, null).tracks).toEqual(
      { mediaID: "m1", captionID: null },
    );
  });

  it("carries the Version picked with them, with Auto as an empty id (P2-3)", () => {
    expect(readPlayerNavState({ tracks: { mediaID: "m1", versionID: "va" } }, "v").tracks).toEqual({
      mediaID: "m1",
      versionID: "va",
    });
    expect(
      readPlayerNavState({ tracks: { mediaID: "m1", versionID: "", audioIndex: 2 } }, "v").tracks,
    ).toEqual({ mediaID: "m1", versionID: "", audioIndex: 2 });
    expect(
      readPlayerNavState({ tracks: { mediaID: "m1", versionID: 7, audioIndex: 2 } }, "v").tracks,
    ).toEqual({ mediaID: "m1", audioIndex: 2 });
  });

  it("drops malformed picks and picks with nothing in them", () => {
    for (const tracks of [
      null,
      "m1",
      { audioIndex: 2 },
      { mediaID: "", audioIndex: 2 },
      { mediaID: "m1" },
      { mediaID: "m1", audioIndex: -1, captionID: "" },
      { mediaID: "m1", audioIndex: 1.5, captionID: 4 },
      { mediaID: "m1", versionID: null },
    ]) {
      expect(readPlayerNavState({ title: "T", tracks }, "v")).toEqual({
        title: "T",
        queue: undefined,
      });
    }
  });
});

describe("playback start identity", () => {
  it("carries a start's id, drops a malformed one, and mints a new id each time", () => {
    expect(readPlayerNavState({ title: "T", startID: "s1" }, "v").startID).toBe("s1");
    for (const startID of ["", 7, null]) {
      expect(readPlayerNavState({ title: "T", startID }, "v")).not.toHaveProperty("startID");
    }
    expect(newStartID()).not.toBe(newStartID());
  });
});

describe("class-narrowed play queue (CORE-1490)", () => {
  const classed = (id: string, mediaClass?: string) =>
    ({ id, title: `T-${id}`, kind: "media", mediaClass }) as unknown as SourceItem;

  it("records the class a row reports, and nothing for a row that reports none", () => {
    expect(playQueueOf([classed("a", "audio"), classed("b")])).toEqual([
      { id: "a", title: "T-a", mediaClass: "audio" },
      { id: "b", title: "T-b" },
    ]);
    expect(playQueueOf([classed("b")])[0]).not.toHaveProperty("mediaClass");
  });

  it("queues only items of the playing item's class from a mixed list", () => {
    const queue = playQueueOf([
      classed("t1", "audio"),
      classed("v1", "video"),
      classed("t2", "audio"),
      classed("v2", "video"),
    ]);
    expect(queueOfClass(queue, "audio", "t1").map((item) => item.id)).toEqual(["t1", "t2"]);
    expect(queueOfClass(queue, "video", "v2").map((item) => item.id)).toEqual(["v1", "v2"]);
  });

  it("keeps entries of unknown class and always the playing item", () => {
    const queue = [
      { id: "t1", title: "1", mediaClass: "audio" },
      { id: "x", title: "x" },
      { id: "v", title: "v", mediaClass: "video" },
    ];
    expect(queueOfClass(queue, "audio", "t1").map((item) => item.id)).toEqual(["t1", "x"]);
    // The playing item stays even when its listed class disagrees.
    expect(queueOfClass(queue, "audio", "v").map((item) => item.id)).toEqual(["t1", "x", "v"]);
  });

  it("leaves the queue alone while the playing item's class is unknown", () => {
    const queue = [
      { id: "a", title: "a", mediaClass: "audio" },
      { id: "v", title: "v", mediaClass: "video" },
    ];
    expect(queueOfClass(queue, undefined, "a")).toBe(queue);
    expect(queueOfClass(queue, "audio", "missing").map((item) => item.id)).toEqual(["a"]);
  });

  it("records learned classes without overriding a listed one", () => {
    const queue = [
      { id: "a", title: "a" },
      { id: "b", title: "b", mediaClass: "audio" },
    ];
    expect(withQueueClasses(queue, { a: "video", b: "video" })).toEqual([
      { id: "a", title: "a", mediaClass: "video" },
      { id: "b", title: "b", mediaClass: "audio" },
    ]);
    expect(withQueueClasses(queue, { b: "video", z: "audio" })).toBe(queue);
  });

  it("round-trips a queue entry's class through nav state", () => {
    expect(
      readPlayerNavState(
        {
          queue: [
            { id: "a", title: "A", mediaClass: "audio" },
            { id: "b", title: "B", mediaClass: 3 },
          ],
          queueIdentity: "v",
        },
        "v",
      ).queue,
    ).toEqual([
      { id: "a", title: "A", mediaClass: "audio" },
      { id: "b", title: "B" },
    ]);
  });
});

describe("section queue (CORE-3013)", () => {
  const items = [item("a", "media"), item("b", "media")];

  it("queues no siblings for a per-container Source, so none runs on", () => {
    expect(sectionQueue("next-up", items)).toBeUndefined();
    expect(sectionQueue("continue-watching", items)).toBeUndefined();
  });

  it("queues a library Source's playable items", () => {
    expect(sectionQueue("recently-added", items)?.map((entry) => entry.id)).toEqual(["a", "b"]);
    expect(sectionQueue(undefined, items)).toHaveLength(2);
  });
});

describe("plays-alone queue (CORE-3147)", () => {
  const alone = (id: string) =>
    ({ id, title: `T-${id}`, kind: "media", playsAlone: true }) as unknown as SourceItem;

  it("carries the flag from the card onto the queue and through nav state", () => {
    const queue = playQueueOf([alone("b1"), item("t1", "media")]);
    expect(queue).toEqual([
      { id: "b1", title: "T-b1", playsAlone: true },
      { id: "t1", title: "T-t1" },
    ]);
    const nav = readPlayerNavState({ queue, queueIdentity: "me" }, "me");
    expect(nav.queue).toEqual(queue);
  });

  it("plays a plays-alone item as a one-item queue", () => {
    const queue = playQueueOf([alone("b1"), alone("b2"), alone("b3")]);
    expect(queueForPlaying(queue, "b2")).toEqual([{ id: "b2", title: "T-b2", playsAlone: true }]);
    expect(queueNeighbours(queueForPlaying(queue, "b2"), "b2")).toEqual({
      previous: undefined,
      next: undefined,
    });
  });

  it("never runs on into a plays-alone item, and leaves other queues unchanged", () => {
    const mixed = playQueueOf([item("t1", "media"), alone("b1"), item("t2", "media")]);
    expect(queueForPlaying(mixed, "t1").map((entry) => entry.id)).toEqual(["t1", "t2"]);
    const tracks = playQueueOf([item("t1", "media"), item("t2", "media")]);
    expect(queueForPlaying(tracks, "t1")).toBe(tracks);
  });

  it("names a listing with a plays-alone Media item", () => {
    expect(listsPlaysAlone([item("t1", "media"), alone("b1")])).toBe(true);
    expect(listsPlaysAlone([item("t1", "media")])).toBe(false);
  });
});

describe("unavailableReasonMessage", () => {
  const messages = { mediaFileMissing: "file gone", mediaNotAvailable: "not here" };
  it("names each server reason and leaves unknown ones to the generic copy", () => {
    expect(unavailableReasonMessage("media-file-unavailable", messages)).toBe("file gone");
    expect(unavailableReasonMessage("media-unavailable", messages)).toBe("not here");
    expect(unavailableReasonMessage("something-new", messages)).toBeUndefined();
    expect(unavailableReasonMessage(undefined, messages)).toBeUndefined();
  });
});

describe("CORE-2845 untruncated catalog memory and queue placement dispatch", () => {
  afterEach(() => {
    resetPlayNextForTest();
  });

  it("recovers full >500-item catalog order even after queueWindow truncation", () => {
    const full = playQueueOf(Array.from({ length: 650 }, (_, i) => item(`m${i}`, "media")));
    const windowed = queueWindow(full, "m0");
    expect(windowed).toHaveLength(500);

    const catalogKey = rememberFullCatalogQueue("viewer-1", windowed, full);
    expect(catalogKey).toBeTruthy();
    expect(fullCatalogQueueFor("viewer-1", windowed, catalogKey)).toEqual(full);
    expect(fullCatalogQueueFor("viewer-2", windowed, catalogKey)).toEqual(windowed);

    const nav = windowedQueueState("viewer-2", full, "m0");
    expect(nav.queue).toHaveLength(500);
    expect(nav.catalogKey).toBeTruthy();
    expect(fullCatalogQueueFor("viewer-2", nav.queue, nav.catalogKey)).toEqual(full);
  });

  it("dispatches Add to queue with end placement to the active player for matching identity", async () => {
    const handler = vi.fn(async () => true);
    setActivePlayNextHandler(handler);
    setActivePlayingMediaID("m-current");

    expect(
      await dispatchAddToQueueToActivePlayer(
        { id: "m-end", title: "End track", mediaClass: "audio" },
        "viewer-1",
      ),
    ).toBe(true);
    expect(handler).toHaveBeenCalledWith(
      { id: "m-end", title: "End track", mediaClass: "audio" },
      "viewer-1",
      "end",
    );
  });

  it("retains the first window across 520 sibling card renders and pins active playback across unrelated catalogs", () => {
    const album = playQueueOf(Array.from({ length: 520 }, (_, i) => item(`alb-${i}`, "media")));
    const firstCard = windowedQueueState("viewer-1", album, "alb-0");
    for (let i = 1; i < album.length; i++) {
      windowedQueueState("viewer-1", album, `alb-${i}`);
    }
    expect(firstCard.queue).toHaveLength(500);
    expect(fullCatalogQueueFor("viewer-1", firstCard.queue, firstCard.catalogKey)).toHaveLength(
      520,
    );

    // Browsing 20 unrelated >500-item collections does not evict the pinned active album.
    pinActiveCatalogQueue("viewer-1", firstCard.queue, firstCard.catalogKey);
    for (let c = 0; c < 20; c++) {
      const other = playQueueOf(Array.from({ length: 510 }, (_, i) => item(`c${c}-${i}`, "media")));
      windowedQueueState("viewer-1", other, `c${c}-0`);
    }
    expect(fullCatalogQueueFor("viewer-1", firstCard.queue, firstCard.catalogKey)).toHaveLength(
      520,
    );
  });

  it("never matches an untruncated 500-track playlist sharing the cached 520-track album slice", () => {
    const album = playQueueOf(Array.from({ length: 520 }, (_, i) => item(`shared-${i}`, "media")));
    const albumCard = windowedQueueState("viewer-1", album, "shared-0");
    expect(fullCatalogQueueFor("viewer-1", albumCard.queue, albumCard.catalogKey)).toHaveLength(
      520,
    );

    const exact500Playlist = windowedQueueState("viewer-1", album.slice(0, 500), "shared-0");
    expect(exact500Playlist.catalogKey).toBeUndefined();
    expect(
      fullCatalogQueueFor("viewer-1", exact500Playlist.queue, exact500Playlist.catalogKey),
    ).toHaveLength(500);
  });

  it("issues distinct catalogKey namespaces across resets / reloads so restored history never collides", () => {
    const albumA = playQueueOf(Array.from({ length: 520 }, (_, i) => item(`a-${i}`, "media")));
    const albumB = playQueueOf(Array.from({ length: 520 }, (_, i) => item(`b-${i}`, "media")));
    const navA = windowedQueueState("viewer-1", albumA, "a-0");
    expect(navA.catalogKey).toBeTruthy();

    // Simulate reload resetting in-memory state.
    resetPlayNextForTest();
    const navB = windowedQueueState("viewer-1", albumB, "b-0");
    expect(navB.catalogKey).toBeTruthy();
    expect(navB.catalogKey).not.toBe(navA.catalogKey);
    expect(fullCatalogQueueFor("viewer-1", navA.queue, navA.catalogKey)).toEqual(navA.queue);
  });
});
