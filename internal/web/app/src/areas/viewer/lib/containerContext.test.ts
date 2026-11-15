import { describe, expect, it } from "vitest";
import type { GenericMediaInfo, SourceItem } from "@/lib/graphql/types";
import { ancestorItems, containerCredits, nearestContainer, nextAfter } from "./containerContext";

const item = (id: string, over: Partial<SourceItem> = {}): SourceItem => ({
  id,
  kind: "media",
  title: id,
  ...over,
});

describe("containerCredits", () => {
  it("keeps container-archetype credits once each, in server order", () => {
    const media: GenericMediaInfo = {
      id: "m",
      title: "M",
      favorited: false,
      credits: [
        { entityID: "g", title: "Genre", kind: "k1", archetype: "category", relation: "r" },
        {
          entityID: "c",
          title: "Box",
          kind: "k2",
          kindDisplayName: { en: "Box kind" },
          archetype: "container",
          relation: "r",
        },
        { entityID: "c", title: "Box", kind: "k2", archetype: "container", relation: "r2" },
      ],
    };
    expect(containerCredits(media)).toEqual([
      { id: "c", title: "Box", kindLabel: { en: "Box kind" } },
    ]);
    expect(containerCredits(undefined)).toEqual([]);
  });
});

describe("nextAfter", () => {
  it("returns the following item, or null at the end or when absent", () => {
    const list = [item("a"), item("b"), item("c")];
    expect(nextAfter(list, "a")?.id).toBe("b");
    expect(nextAfter(list, "c")).toBeNull();
    expect(nextAfter(list, "x")).toBeNull();
  });
});

describe("nearestContainer", () => {
  it("prefers the smallest list holding the item", () => {
    const big = { link: { id: "big", title: "Big" }, items: [item("a"), item("b"), item("m")] };
    const small = { link: { id: "small", title: "Small" }, items: [item("m"), item("n")] };
    const other = { link: { id: "other", title: "Other" }, items: [item("z")] };
    expect(nearestContainer([big, small, other], "m")?.link.id).toBe("small");
    expect(nearestContainer([other], "m")).toBeNull();
  });
});

describe("ancestorItems", () => {
  it("keeps ordered containers of another kind holding at least as many items", () => {
    const related = [
      {
        items: [
          item("outer", { kind: "o", engagement: { totalCount: 12 } }),
          item("sibling", { kind: "inner", engagement: { totalCount: 20 } }),
          item("small", { kind: "o", engagement: { totalCount: 2 } }),
          item("plain", { kind: "p" }),
        ],
      },
    ];
    expect(
      ancestorItems(related, { id: "self", kind: "inner" }, 6).map((entry) => entry.id),
    ).toEqual(["outer"]);
    expect(ancestorItems(undefined, { id: "self", kind: "inner" }, 6)).toEqual([]);
  });
});
