import { describe, expect, it } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { unavailableReasonMessage } from "./playback";
import { playQueueOf, queueNeighbours, readPlayerNavState } from "./watchQueue";

const item = (id: string, kind: string) =>
  ({ id, title: `T-${id}`, kind }) as unknown as SourceItem;

describe("watchQueue (CORE-1143)", () => {
  it("keeps only playable Media items, in order", () => {
    expect(playQueueOf([item("a", "media"), item("e", "entity"), item("b", "media")])).toEqual([
      { id: "a", title: "T-a" },
      { id: "b", title: "T-b" },
    ]);
  });

  it("caps a huge list so history state stays small", () => {
    const items = Array.from({ length: 900 }, (_, i) => item(`m${i}`, "media"));
    expect(playQueueOf(items)).toHaveLength(500);
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
    expect(readPlayerNavState(null)).toEqual({});
    expect(readPlayerNavState("x")).toEqual({});
    expect(
      readPlayerNavState({ title: 4, queue: [{ id: "a", title: "A" }, { id: 1 }, null, "b"] }),
    ).toEqual({ title: undefined, queue: [{ id: "a", title: "A" }] });
    expect(readPlayerNavState({ title: "Song" })).toEqual({ title: "Song", queue: undefined });
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
