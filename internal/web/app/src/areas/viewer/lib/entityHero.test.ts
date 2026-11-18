import { describe, expect, it } from "vitest";
import type { EntityInfo, RelatedEntityGroup, SourceItem } from "@/lib/graphql/types";
import {
  descendantContainerGroups,
  entityFacts,
  entitySummary,
  groupCredits,
  heroFields,
  heroGroups,
  unwatchedCount,
  withItemCount,
} from "./entityHero";

const entity: EntityInfo = {
  id: "e1",
  kind: "set",
  title: "Set",
  fields: [
    { name: "year", type: "number", value: "1999", label: "Year" },
    { name: "summary", type: "text", value: " Overview ", label: "Summary" },
    { name: "community-rating", type: "number", value: "7.5", label: "Score" },
    { name: "community-rating-votes", type: "number", value: "12", label: "Votes" },
    { name: "network", type: "text", value: "Channel", label: "Network" },
  ],
  relatedEntities: [
    { kind: "genre", archetype: "category", items: [{ id: "g1", kind: "entity", title: "A" }] },
    {
      kind: "cast",
      kindDisplayName: { en: "Cast" },
      archetype: "person",
      items: [{ id: "p1", kind: "entity", title: "P", artworkSlots: { poster: "/a/p" } }],
    },
    { kind: "part", archetype: "container", items: [{ id: "b1", kind: "entity", title: "B" }] },
    { kind: "empty", items: [] },
  ],
};

describe("entityHero", () => {
  it("reads facts, summary and the leftover fields", () => {
    const facts = entityFacts(entity);
    expect(facts.year).toBe(1999);
    expect(facts.score).toMatchObject({ value: 7.5, votes: 12 });
    expect(facts.categories).toEqual(["A"]);
    expect(entitySummary(entity)).toBe("Overview");
    expect(heroFields(entity, facts.year).map((f) => f.name)).toEqual(["network"]);
    expect(entityFacts(null)).toEqual({ categories: [] });
  });

  it("splits groups by archetype and maps people to credits", () => {
    const groups = heroGroups(entity.relatedEntities);
    expect(groups.containers.map((g) => g.kind)).toEqual(["part"]);
    expect(groups.people.map((g) => g.kind)).toEqual(["cast"]);
    expect(groups.other.map((g) => g.kind)).toEqual(["genre"]);
    expect(groupCredits(groups.people, "en")).toEqual([
      expect.objectContaining({
        entityID: "p1",
        archetype: "person",
        attributes: { department: "Cast" },
        artworkSlots: { poster: "/a/p" },
      }),
    ]);
  });

  it("counts own unfinished Media, else child containers' remaining counts", () => {
    const media = (id: string, completed?: boolean, locked?: boolean): SourceItem => ({
      id,
      kind: "media",
      title: id,
      locked,
      engagement: { completed },
    });
    expect(unwatchedCount([media("a", true), media("b"), media("c", false, true)], [])).toBe(1);
    expect(
      unwatchedCount(
        [],
        [
          {
            kind: "part",
            archetype: "container",
            items: [
              { id: "b1", kind: "entity", title: "b1", engagement: { remainingCount: 2 } },
              { id: "b2", kind: "entity", title: "b2", engagement: { remainingCount: 3 } },
            ],
          },
          {
            kind: "x",
            items: [{ id: "x", kind: "entity", title: "x", engagement: { remainingCount: 9 } }],
          },
        ],
      ),
    ).toBe(5);
    expect(unwatchedCount(null, null)).toBe(0);
  });

  it("counts only child containers, not the Entity's own parent, in the badge", () => {
    // Related groups carry both relation directions: a parent container holds
    // everything this Entity holds, so its remaining count is not this
    // Entity's to report.
    const container = (id: string, remaining: number, total: number) => ({
      id,
      kind: "entity",
      title: id,
      engagement: { remainingCount: remaining, totalCount: total },
    });
    const parent: RelatedEntityGroup = {
      kind: "whole",
      archetype: "container",
      items: [container("w", 5, 20)],
    };
    const children: RelatedEntityGroup = {
      kind: "part",
      archetype: "container",
      items: [container("b1", 1, 4), container("b2", 2, 6)],
    };
    expect(unwatchedCount([], [parent, children])).toBe(3);
    expect(unwatchedCount([], [children, parent])).toBe(3);
    expect(descendantContainerGroups([parent, children])).toEqual([children]);
    // One container group reads as children; so does a tie where every group
    // would read as a parent.
    expect(unwatchedCount([], [parent])).toBe(5);
    const twin: RelatedEntityGroup = { ...parent, kind: "twin", items: [container("t", 4, 20)] };
    expect(unwatchedCount([], [parent, twin])).toBe(9);
  });

  it("adds an item count to a child card's subline", () => {
    const text = (count: number) => `${count} items`;
    const item: SourceItem = { id: "b", kind: "entity", title: "B", engagement: { totalCount: 4 } };
    expect(withItemCount(item, text, "en").subtitle).toBe("4 items");
    expect(withItemCount({ ...item, subtitle: "Sub" }, text, "en").subtitle).toBe("Sub · 4 items");
    const unknown = { ...item, engagement: {} };
    expect(withItemCount(unknown, text, "en")).toBe(unknown);
  });
});
