import { describe, expect, it, vi } from "vitest";
import { graphql } from "@/lib/api";
import type { EntityInfo, SourceItem } from "@/lib/graphql/types";
import { fetchMediaForEntity } from "@/lib/graphql/viewer";
import {
  entityFacts,
  entitySummary,
  groupCredits,
  heroFields,
  heroGroups,
  unwatchedCount,
  withItemCount,
} from "./entityHero";

vi.mock("@/lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api")>();
  return { ...actual, graphql: vi.fn() };
});

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
    expect(facts.categories.flatMap((group) => group.shown.map((credit) => credit.title))).toEqual([
      "A",
    ]);
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

  it("counts only the Entity's own unfinished Media", () => {
    const media = (id: string, completed?: boolean, locked?: boolean): SourceItem => ({
      id,
      kind: "media",
      title: id,
      locked,
      engagement: { completed },
    });
    expect(unwatchedCount([media("a", true), media("b"), media("c", false, true)])).toBe(1);
    // Two relations to the same Media count it once.
    expect(unwatchedCount([media("b"), media("b"), media("d")])).toBe(2);
    expect(unwatchedCount([])).toBe(0);
    expect(unwatchedCount(null)).toBe(0);
  });

  it("leaves locked-mode Media out through the real mediaForEntity read", async () => {
    // The server flags Media above a locked-mode ceiling (store
    // MediaForEntity); the read must ask for that flag so the badge skips it.
    vi.mocked(graphql).mockResolvedValue({
      mediaForEntity: [
        { id: "m1", kind: "media", title: "Done", engagement: { completed: true } },
        { id: "m2", kind: "media", title: "Above ceiling", locked: true },
      ],
    });

    const items = await fetchMediaForEntity("e1");

    expect(vi.mocked(graphql).mock.calls[0][0]).toMatch(
      /mediaForEntity\([^)]*\) \{[^}]*\blocked\b/,
    );
    expect(unwatchedCount(items)).toBe(0);
  });

  it("returns a Media item reached by two relations once, in its first position", async () => {
    vi.mocked(graphql).mockResolvedValue({
      mediaForEntity: [
        { id: "m1", kind: "media", title: "One" },
        { id: "m2", kind: "media", title: "Two" },
        { id: "m1", kind: "media", title: "One" },
      ],
    });

    const items = await fetchMediaForEntity("e1");

    expect(items.map((item) => item.id)).toEqual(["m1", "m2"]);
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
