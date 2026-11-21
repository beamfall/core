import { describe, expect, it } from "vitest";
import { clampZoom, imageNeighbours, isImageClass, nextZoom } from "./imageViewer";

describe("imageViewer helpers", () => {
  const queue = [
    { id: "a", mediaClass: "image" },
    { id: "clip", mediaClass: "video" },
    { id: "b", mediaClass: "image" },
    { id: "c" },
  ];

  it("steps between images and skips other classes", () => {
    const around = imageNeighbours(queue as never, "b");
    expect(around.previous?.id).toBe("a");
    expect(around.next?.id).toBe("c");
    expect(around.position).toBe(2);
    expect(around.total).toBe(3);
  });

  it("has no neighbours outside the queue", () => {
    const around = imageNeighbours(queue as never, "missing");
    expect(around.previous).toBeUndefined();
    expect(around.next).toBeUndefined();
  });

  it("cycles and clamps zoom", () => {
    expect(nextZoom(1)).toBe(2);
    expect(nextZoom(4)).toBe(1);
    expect(clampZoom(9)).toBe(4);
    expect(clampZoom(0.2)).toBe(1);
    expect(isImageClass("image")).toBe(true);
    expect(isImageClass("video")).toBe(false);
  });
});
