import { describe, expect, it } from "vitest";
import {
  clampZoom,
  filmstripWindow,
  isClipClass,
  isImageClass,
  nextZoom,
  trashStepTarget,
  viewerNeighbours,
} from "./imageViewer";

describe("imageViewer helpers", () => {
  const queue = [
    { id: "a", title: "A", mediaClass: "image" },
    { id: "track", title: "T", mediaClass: "audio" },
    { id: "clip", title: "Clip", mediaClass: "video" },
    { id: "b", title: "B", mediaClass: "image" },
    { id: "c", title: "C" },
  ];

  it("steps through images and clips and skips other classes", () => {
    const around = viewerNeighbours(queue, "b");
    expect(around.previous?.id).toBe("clip");
    expect(around.next?.id).toBe("c");
    expect(around.position).toBe(3);
    expect(around.total).toBe(4);
    expect(around.items.map((item) => item.id)).toEqual(["a", "clip", "b", "c"]);
  });

  it("leaves trashed items out of stepping", () => {
    const around = viewerNeighbours(queue, "b", new Set(["clip"]));
    expect(around.previous?.id).toBe("a");
    expect(around.total).toBe(3);
    expect(viewerNeighbours(queue, "clip", new Set(["clip"])).index).toBe(-1);
  });

  it("has no neighbours outside the queue", () => {
    const around = viewerNeighbours(queue, "missing");
    expect(around.index).toBe(-1);
    expect(around.previous).toBeUndefined();
    expect(around.next).toBeUndefined();
    expect(viewerNeighbours(undefined, "a").total).toBe(0);
  });

  it("windows the filmstrip around the current item", () => {
    const items = Array.from({ length: 20 }, (_, i) => i);
    expect(filmstripWindow(items, 0, 3)).toEqual([0, 1, 2, 3]);
    expect(filmstripWindow(items, 10, 3)).toEqual([7, 8, 9, 10, 11, 12, 13]);
    expect(filmstripWindow(items, 19, 3)).toEqual([16, 17, 18, 19]);
    expect(filmstripWindow(items, -1, 3)).toEqual([]);
    expect(filmstripWindow([1], 0, 3)).toEqual([]);
  });

  it("steps on after a trash: next, else previous, else nowhere", () => {
    const items = viewerNeighbours(queue, "a").items;
    expect(trashStepTarget(items, 1)).toEqual({ target: items[2], direction: "next" });
    expect(trashStepTarget(items, 3)).toEqual({ target: items[2], direction: "previous" });
    expect(trashStepTarget(items.slice(0, 1), 0)).toBeNull();
    expect(trashStepTarget(items, -1)).toBeNull();
  });

  it("classes items and cycles and clamps zoom", () => {
    expect(nextZoom(1)).toBe(2);
    expect(nextZoom(4)).toBe(1);
    expect(clampZoom(9)).toBe(4);
    expect(clampZoom(0.2)).toBe(1);
    expect(isImageClass("image")).toBe(true);
    expect(isImageClass("video")).toBe(false);
    expect(isClipClass("video")).toBe(true);
    expect(isClipClass(undefined)).toBe(false);
  });
});
