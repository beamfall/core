import { describe, expect, it } from "vitest";
import type { SourceItem } from "@/lib/graphql/types";
import {
  childThumbURL,
  groupChildrenByKind,
  nextUpChild,
  nextUpChildContainer,
  nextUpFinished,
  rolledContainerCandidates,
  rolledContainerLeft,
} from "./containerChildren";

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

describe("nextUpChildContainer", () => {
  const box = (id: string, remainingCount?: number, extra: Partial<SourceItem> = {}) =>
    item(id, {
      kind: "entity",
      engagement: remainingCount === undefined ? undefined : { remainingCount },
      ...extra,
    });

  it("picks the first container with children left, skipping other archetypes", () => {
    const next = nextUpChildContainer([
      { archetype: "person", items: [box("p")] },
      { archetype: "container", items: [box("s1", 0), box("s2", 3), box("s3", 5)] },
    ]);
    expect(next?.id).toBe("s2");
  });

  it("falls back to the first when every child container is finished", () => {
    expect(
      nextUpChildContainer([{ archetype: "container", items: [box("s1", 0), box("s2", 0)] }])?.id,
    ).toBe("s1");
  });

  it("treats an omitted zero remaining count, or completed, as finished", () => {
    const finished = box("s1", undefined, { engagement: { totalCount: 4 } });
    const completed = box("s2", undefined, { engagement: { completed: true } });
    expect(
      nextUpChildContainer([{ archetype: "container", items: [finished, completed, box("s3", 2)] }])
        ?.id,
    ).toBe("s3");
    expect(
      nextUpChildContainer([{ archetype: "container", items: [box("s1"), box("s2", 2)] }])?.id,
    ).toBe("s1");
  });

  it("skips locked children and returns null without a container group", () => {
    expect(
      nextUpChildContainer([
        { archetype: "container", items: [box("s1", 2, { locked: true }), box("s2", 2)] },
      ])?.id,
    ).toBe("s2");
    expect(nextUpChildContainer([{ archetype: "person", items: [box("p")] }])).toBeNull();
    expect(nextUpChildContainer(undefined)).toBeNull();
  });
});

describe("rolledContainerCandidates (CORE-3058)", () => {
  const card = (id: string, remainingCount?: number, locked?: boolean): SourceItem => ({
    id,
    kind: "entity",
    title: id,
    locked,
    engagement: { totalCount: 3, ...(remainingCount ? { remainingCount } : {}) },
  });

  const ids = (items: SourceItem[]) => items.map((i) => i.id);

  it("tries unlocked container cards with children left, else the first", () => {
    const film = item("f1");
    expect(
      ids(
        rolledContainerCandidates([
          film,
          card("a"),
          card("b", 2, true),
          card("c", 1),
          card("d", 2),
        ]),
      ),
    ).toEqual(["c", "d"]);
    expect(ids(rolledContainerCandidates([film, card("a"), card("b")]))).toEqual(["a"]);
    expect(rolledContainerCandidates([film])).toEqual([]);
    expect(rolledContainerLeft([film, card("a"), card("b", 2, true)])).toBe(false);
    expect(rolledContainerLeft([film, card("c", 1)])).toBe(true);
  });
});

describe("nextUpFinished", () => {
  it("is true only for a finished item played again", () => {
    const done = item("a", { engagement: { completed: true } });
    expect(nextUpFinished({ item: done, resume: false })).toBe(true);
    expect(nextUpFinished({ item: done, resume: true })).toBe(false);
    expect(nextUpFinished({ item: item("b"), resume: false })).toBe(false);
  });
});
