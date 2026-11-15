import { describe, expect, it } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { unavailableReasonMessage } from "./playback";
import {
  playQueueOf,
  queueNeighbours,
  queueOfClass,
  queueWindow,
  readPlayerNavState,
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

describe("unavailableReasonMessage", () => {
  const messages = { mediaFileMissing: "file gone", mediaNotAvailable: "not here" };
  it("names each server reason and leaves unknown ones to the generic copy", () => {
    expect(unavailableReasonMessage("media-file-unavailable", messages)).toBe("file gone");
    expect(unavailableReasonMessage("media-unavailable", messages)).toBe("not here");
    expect(unavailableReasonMessage("something-new", messages)).toBeUndefined();
    expect(unavailableReasonMessage(undefined, messages)).toBeUndefined();
  });
});
