import { describe, expect, it } from "vitest";
import type { MediaCredit, SourceItem } from "@/lib/graphql/types";
import { RELATED_CATEGORIES, RELATED_PEOPLE, rankRelated, relatedSignals } from "./related";

const item = (id: string): SourceItem => ({ id, kind: "media", title: id.toUpperCase() });
const credit = (entityID: string, archetype: string): MediaCredit => ({
  entityID,
  title: entityID,
  kind: archetype,
  archetype,
  relation: "r",
});

describe("relatedSignals (CORE-2444)", () => {
  it("bounds the category and person credits it reads, skipping containers", () => {
    const credits = [
      credit("box", "container"),
      ...Array.from({ length: RELATED_CATEGORIES + 2 }, (_, i) => credit(`c${i}`, "category")),
      ...Array.from({ length: RELATED_PEOPLE + 2 }, (_, i) => credit(`p${i}`, "person")),
    ];
    const { categories, people } = relatedSignals(credits);
    expect(categories.map((c) => c.entityID)).toEqual(["c0", "c1", "c2", "c3"]);
    expect(people.map((c) => c.entityID)).toEqual(["p0", "p1", "p2"]);
  });
});

describe("rankRelated (CORE-2444)", () => {
  it("ranks by shared signals, keeps first-seen order on ties and drops the current item", () => {
    const ranked = rankRelated(
      "self",
      [
        [item("a"), item("b"), item("self")],
        [item("b"), item("c")],
      ],
      [[item("c"), item("a")]],
    );
    expect(ranked.map((i) => i.id)).toEqual(["a", "b", "c"]);
  });

  it("requires a shared category: people alone do not qualify", () => {
    const ranked = rankRelated("self", [[item("a")]], [[item("p"), item("a")], [item("p")]]);
    expect(ranked.map((i) => i.id)).toEqual(["a"]);
  });

  it("counts an item once per signal even when a list repeats it", () => {
    const ranked = rankRelated("self", [[item("a"), item("a")], [item("b")], [item("b")]], []);
    expect(ranked.map((i) => i.id)).toEqual(["b", "a"]);
  });
});
