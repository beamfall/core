import { describe, expect, it } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import { childThumbURL, groupChildrenByKind, nextUpChild } from "./containerChildren";

const item = (id: string, extra: Partial<SourceItem> = {}): SourceItem => ({
  id,
  kind: "media",
  title: id,
  ...extra,
});

describe("groupChildrenByKind", () => {
  it("keeps the server order within and across kinds", () => {
    const groups = groupChildrenByKind([
      item("a"),
      item("b", { kind: "extra" }),
      item("c"),
      item("d", { kind: "extra" }),
    ]);
    expect(groups.map((g) => [g.kind, g.items.map((i) => i.id)])).toEqual([
      ["media", ["a", "c"]],
      ["extra", ["b", "d"]],
    ]);
  });
});

describe("nextUpChild", () => {
  it("prefers a child in progress", () => {
    const next = nextUpChild([
      item("a"),
      item("b", { engagement: { positionSeconds: 30, durationSeconds: 100 } }),
    ]);
    expect(next).toEqual({ item: expect.objectContaining({ id: "b" }), resume: true });
  });

  it("falls back to the first unfinished, then the first, child", () => {
    const done = { engagement: { completed: true } };
    expect(nextUpChild([item("a", done), item("b")])?.item.id).toBe("b");
    expect(nextUpChild([item("a", done), item("b", done)])).toEqual({
      item: expect.objectContaining({ id: "a" }),
      resume: false,
    });
  });

  it("skips locked and non-media children", () => {
    expect(nextUpChild([item("a", { locked: true }), item("b", { kind: "entity" })])).toBeNull();
    expect(nextUpChild([])).toBeNull();
  });
});

describe("childThumbURL", () => {
  it("uses the thumb slot, then the backdrop, then the poster", () => {
    expect(childThumbURL(item("a", { artworkSlots: { thumb: "/t", backdrop: "/b" } }))).toBe("/t");
    expect(childThumbURL(item("a", { artworkSlots: { backdrop: "/b" } }))).toBe("/b");
    expect(childThumbURL(item("a", { artwork: { poster: "/p" } }))).toBe("/p");
    expect(childThumbURL(item("a"))).toBe("");
  });
});
