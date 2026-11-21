import { describe, expect, it } from "vitest";
import type { EntityInfo, SourceItem } from "@/lib/graphql/types";
import {
  ancestorOf,
  groupHeading,
  isRankedShelf,
  itemRuntimeSeconds,
  nextUpName,
  rankShelfItems,
  rowOrdinals,
  siblingContainers,
  similarContainers,
  textOverview,
  withoutItem,
} from "./containerPage";

const entityItem = (id: string, title: string, extra: Partial<SourceItem> = {}): SourceItem => ({
  id,
  kind: "entity",
  title,
  ...extra,
});
const media = (id: string, title: string, subtitle?: string): SourceItem => ({
  id,
  kind: "media",
  title,
  subtitle: subtitle ? { en: subtitle } : undefined,
});

describe("ancestorOf (CORE-2510)", () => {
  const part: EntityInfo = {
    id: "p2",
    kind: "part",
    archetype: "container",
    title: "Part 2",
    fields: [],
    relatedEntities: [
      {
        kind: "maker",
        archetype: "person",
        items: [entityItem("m1", "Maker", { engagement: { totalCount: 50 } })],
      },
      {
        kind: "whole",
        archetype: "container",
        items: [entityItem("w1", "Whole", { engagement: { totalCount: 20 } })],
      },
    ],
  };

  it("finds the larger related container of another kind", () => {
    expect(ancestorOf(part, 10)?.id).toBe("w1");
  });

  it("never takes a container of the page's own kind as its ancestor", () => {
    const peer = {
      kind: "part",
      archetype: "container",
      items: [entityItem("p1", "Part 1", { engagement: { totalCount: 30 } })],
    };
    expect(
      ancestorOf({ ...part, relatedEntities: [peer, ...(part.relatedEntities ?? [])] }, 10)?.id,
    ).toBe("w1");
  });

  it("finds none without own Media, for a non-container, or when no container is larger", () => {
    expect(ancestorOf(part, 0)).toBeNull();
    expect(ancestorOf({ ...part, archetype: "person" }, 10)).toBeNull();
    expect(ancestorOf(part, 21)).toBeNull();
    expect(ancestorOf(null, 10)).toBeNull();
  });

  it("drops the ancestor from the container groups", () => {
    const groups = withoutItem(part.relatedEntities ?? [], "w1");
    expect(groups.map((group) => group.kind)).toEqual(["maker"]);
    expect(withoutItem(part.relatedEntities ?? [], undefined)).toHaveLength(2);
  });
});

describe("groupHeading (CORE-2831)", () => {
  it("heads a group with the plugin's plural, then its display name, then the kind", () => {
    const items: SourceItem[] = [];
    expect(
      groupHeading(
        {
          kind: "album",
          kindDisplayName: { en: "Album", fr: "Album" },
          kindPluralDisplayName: { en: "Albums", fr: "Albums (fr)" },
          items,
        },
        "fr",
      ),
    ).toBe("Albums (fr)");
    expect(groupHeading({ kind: "album", kindDisplayName: { en: "Album" }, items }, "en")).toBe(
      "Album",
    );
    expect(groupHeading({ kind: "album", items }, "en")).toBe("album");
  });
});

describe("siblingContainers (CORE-2522)", () => {
  const whole: EntityInfo = {
    id: "w1",
    kind: "whole",
    title: "Whole",
    fields: [],
    relatedEntities: [
      { kind: "maker", items: [entityItem("p1", "Not a part")] },
      {
        kind: "part",
        items: [entityItem("p1", "Part 1"), entityItem("p2", "Part 2"), entityItem("p1", "Part 1")],
      },
    ],
  };

  it("lists the ancestor's Entities of the page's kind in the server's order, each once", () => {
    const siblings = siblingContainers(whole, { id: "p2", kind: "part" });
    expect(siblings?.group.kind).toBe("part");
    expect(siblings?.items.map((item) => item.id)).toEqual(["p1", "p2"]);
  });

  it("offers no switcher for a lone container or an unrelated page", () => {
    const lone: EntityInfo = {
      ...whole,
      relatedEntities: [{ kind: "part", items: [entityItem("p2", "Part 2")] }],
    };
    expect(siblingContainers(lone, { id: "p2", kind: "part" })).toBeNull();
    expect(siblingContainers(whole, { id: "p9", kind: "part" })).toBeNull();
    expect(siblingContainers(null, { id: "p2", kind: "part" })).toBeNull();
  });
});

describe("row ordinals and the On Deck name (CORE-2510)", () => {
  it("keeps each row's own ordinal, without its title or the parts every sibling shares", () => {
    const ordinals = rowOrdinals(
      [
        media("m1", "Countdown", "Show · S1 · E1 · Countdown"),
        media("m2", "Liftoff", "Show · S1 · E2 · Liftoff"),
        media("m3", "No subtitle"),
      ],
      "en",
    );
    expect(ordinals.get("m1")).toBe("E1");
    expect(ordinals.get("m2")).toBe("E2");
    expect(ordinals.get("m3")).toBe("");
  });

  it("keeps a lone subtitle whole apart from the title", () => {
    expect(rowOrdinals([media("m1", "Countdown", "S1 · E1 · Countdown")], "en").get("m1")).toBe(
      "S1 · E1",
    );
  });

  it("names the next child once", () => {
    expect(nextUpName(media("m1", "Countdown", "S1 · E1 · Countdown"), "en")).toBe(
      "S1 · E1 · Countdown",
    );
    expect(nextUpName(media("m1", "Countdown", "S1 · E1"), "en")).toBe("S1 · E1 · Countdown");
    expect(nextUpName(media("m1", "Countdown"), "en")).toBe("Countdown");
  });

  it("reads the runtime from the item, then its engagement, ignoring under a minute", () => {
    expect(itemRuntimeSeconds({ ...media("m", "x"), durationSeconds: 1800 })).toBe(1800);
    expect(itemRuntimeSeconds({ ...media("m", "x"), engagement: { durationSeconds: 600 } })).toBe(
      600,
    );
    expect(itemRuntimeSeconds({ ...media("m", "x"), durationSeconds: 30 })).toBeUndefined();
    expect(itemRuntimeSeconds(media("m", "x"))).toBeUndefined();
  });
});

describe("textOverview (CORE-2522)", () => {
  const long = "A long overview sentence that keeps going. ".repeat(4).trim();
  const fields = [
    { name: "studio", type: "text", value: "North" },
    { name: "overview", type: "text", value: long },
    { name: "year", type: "number", value: "2004" },
  ];

  it("reads the first long free-text field as the overview when there is no summary", () => {
    const split = textOverview("", fields);
    expect(split.overview).toBe(long);
    expect(split.fields.map((field) => field.name)).toEqual(["studio", "year"]);
  });

  it("keeps the builtin summary and short text facts as they are", () => {
    expect(textOverview("Summary.", fields)).toEqual({ overview: "Summary.", fields });
    expect(textOverview("", [fields[0], fields[2]]).overview).toBe("");
  });
});

describe("similarContainers (CORE-2512)", () => {
  const through = (id: string, ids: string[]): EntityInfo => ({
    id,
    kind: "genre",
    title: id,
    fields: [],
    relatedEntities: [
      { kind: "whole", items: ids.map((item) => entityItem(item, item.toUpperCase())) },
      { kind: "film", items: [entityItem("f1", "A film")] },
    ],
  });

  it("ranks same-kind Entities by how many genres, networks and people they share", () => {
    const ranked = similarContainers(
      { id: "self", kind: "whole" },
      [
        through("g1", ["self", "a", "b"]),
        through("g2", ["b", "c", "b"]),
        null,
        through("p1", ["c", "b"]),
      ],
      10,
    );
    expect(ranked.map((item) => item.id)).toEqual(["b", "c", "a"]);
  });

  it("caps the rail and finds nothing through nothing", () => {
    expect(
      similarContainers({ id: "self", kind: "whole" }, [through("g1", ["a", "b", "c"])], 2),
    ).toHaveLength(2);
    expect(similarContainers({ id: "self", kind: "whole" }, [], 10)).toEqual([]);
  });
});

describe("catalog shelf ranking (CORE-2512)", () => {
  it("ranks only catalog shelves", () => {
    expect(isRankedShelf({ kind: "shelf", source: { kind: "entity-items" } })).toBe(true);
    expect(isRankedShelf({ kind: "shelf", source: { kind: "all-items" } })).toBe(true);
    expect(isRankedShelf({ kind: "shelf", source: { kind: "continue-watching" } })).toBe(false);
    expect(isRankedShelf({ kind: "grid", source: { kind: "entity-items" } })).toBe(false);
  });

  it("puts in-progress first, then unstarted, then finished, newer first, never A-Z", () => {
    const ranked = rankShelfItems([
      entityItem("alpha", "Alpha", {
        year: 2001,
        engagement: { totalCount: 5, remainingCount: 0 },
      }),
      entityItem("bravo", "Bravo", { year: 2010 }),
      entityItem("charlie", "Charlie", { year: 2020 }),
      entityItem("delta", "Delta", {
        year: 1999,
        engagement: { totalCount: 5, remainingCount: 2 },
      }),
      entityItem("echo", "Echo", { engagement: { positionSeconds: 300, durationSeconds: 3000 } }),
    ]);
    expect(ranked.map((item) => item.id)).toEqual(["delta", "echo", "charlie", "bravo", "alpha"]);
  });
});
