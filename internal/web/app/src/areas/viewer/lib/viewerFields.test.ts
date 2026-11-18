import { describe, expect, it } from "vitest";
import type { EntityFieldInfo, GenericMediaInfo } from "@/lib/graphql/types";
import {
  detailCreditRows,
  detailFacts,
  fieldDisplayValue,
  runtimeParts,
  viewerFacingFields,
} from "./viewerFields";

const field = (over: Partial<EntityFieldInfo>): EntityFieldInfo => ({
  name: "f",
  type: "text",
  value: "v",
  label: { en: "F" },
  ...over,
});

describe("viewerFacingFields", () => {
  it("keeps declared fields in server order and drops bookkeeping", () => {
    const fields = [
      field({ name: "tagline", label: { en: "Tagline" } }),
      field({ name: "index", label: undefined, type: "number" }),
      field({ name: "class", label: { en: "Class" } }),
      field({ name: "hydrated", type: "flag", label: { en: "Grouped Locally" } }),
      field({ name: "certification", label: { en: "Certification" } }),
      field({ name: "certification-display", label: { en: "Rating" } }),
      field({ name: "community-rating", type: "number", value: "7", label: { en: "TMDB rating" } }),
      field({
        name: "community-rating-votes",
        type: "number",
        value: "9",
        label: { en: "TMDB votes" },
      }),
      field({ name: "blank", value: "  ", label: { en: "Blank" } }),
      field({ name: "empty-label", label: "" }),
      field({ name: "network", label: "Network" }),
    ];
    expect(viewerFacingFields(fields).map((f) => f.name)).toEqual(["tagline", "network"]);
  });
});

describe("viewerFacingFields drops what is internal or already shown (CORE-2319)", () => {
  it("drops internal content addresses", () => {
    const fields = [
      field({
        name: "source",
        label: "Hydrated From",
        value: "sha256:9f2c4a1b7e3d5f60a1b2;cel;plan=4",
      }),
      field({ name: "tagline", label: "Tagline", value: "A sha256 story" }),
    ];
    expect(viewerFacingFields(fields).map((f) => f.name)).toEqual(["tagline"]);
  });

  it("drops a field equal to the title, a bare-year date and a repeated year", () => {
    const fields = [
      field({ name: "original", label: "Original Title", value: "  barbie " }),
      field({
        name: "released",
        label: "Release Date",
        type: "date",
        value: "2013-01-01T00:00:00Z",
      }),
      field({ name: "y", label: "Year", type: "number", value: "2013" }),
      field({ name: "premiere", label: "Premiere", type: "date", value: "2013-05-09" }),
      field({ name: "minutes", label: "Runtime", type: "number", value: "180" }),
    ];
    const shown = { title: "Barbie", year: 2013, runtimeSeconds: 10_800 };
    expect(viewerFacingFields(fields, shown).map((f) => f.name)).toEqual(["premiere"]);
    // Without the facts to compare against, nothing is guessed away.
    expect(viewerFacingFields(fields).map((f) => f.name)).toHaveLength(5);
  });

  it("drops an ordinal the heading already carries", () => {
    const fields = [
      field({ name: "n", label: "Number", type: "number", value: "1" }),
      field({ name: "count", label: "Count", type: "number", value: "10" }),
    ];
    expect(viewerFacingFields(fields, { title: "Season 1" }).map((f) => f.name)).toEqual(["count"]);
  });
});

describe("detailFacts", () => {
  const media: GenericMediaInfo = {
    id: "m1",
    title: "T",
    year: 1999,
    favorited: false,
    credits: [
      { entityID: "a", title: "One", kind: "k", archetype: "category", relation: "r" },
      { entityID: "b", title: "One", kind: "k", archetype: "category", relation: "r" },
      { entityID: "c", title: "Box", kind: "k", archetype: "container", relation: "r" },
      { entityID: "d", title: "Two", kind: "k", archetype: "category", relation: "r" },
      { entityID: "e", title: "Three", kind: "k", archetype: "category", relation: "r" },
      { entityID: "f", title: "Four", kind: "k", archetype: "category", relation: "r" },
      { entityID: "g", title: "Alpha", kind: "j", archetype: "category", relation: "r" },
      { entityID: "h", title: "Beta", kind: "i", archetype: "category", relation: "r" },
    ],
  };

  it("collects year, runtime, rating and two distinct categories of the first two kinds (CORE-2504)", () => {
    expect(detailFacts(media, [field({ name: "certification", value: " R " })], 3600)).toEqual({
      year: 1999,
      runtimeSeconds: 3600,
      rating: "R",
      categories: [
        {
          kind: "k",
          shown: [
            { entityID: "a", title: "One" },
            { entityID: "d", title: "Two" },
          ],
          more: 2,
        },
        { kind: "j", shown: [{ entityID: "g", title: "Alpha" }], more: 0 },
      ],
    });
  });

  it("prefers the display rating and reads the community score (CORE-2399)", () => {
    const facts = detailFacts(null, [
      field({ name: "certification", value: "R" }),
      field({ name: "certification-display", value: "TV-MA" }),
      field({
        name: "community-rating",
        type: "number",
        value: "7.25",
        label: { en: "TMDB rating" },
      }),
      field({ name: "community-rating-votes", type: "number", value: "1200" }),
    ]);
    expect(facts.rating).toBe("TV-MA");
    expect(facts.score).toEqual({ value: 7.25, votes: 1200, label: { en: "TMDB rating" } });
  });

  it("drops a score nobody voted for or outside 0-10", () => {
    for (const [score, votes] of [
      ["0", "0"],
      ["7.1", "0"],
      ["7.1", ""],
      ["11", "40"],
      ["n/a", "40"],
    ]) {
      const fields = [field({ name: "community-rating", type: "number", value: score })];
      if (votes)
        fields.push(field({ name: "community-rating-votes", type: "number", value: votes }));
      expect(detailFacts(null, fields).score, `${score}/${votes}`).toBeUndefined();
    }
  });

  it("omits what is unknown", () => {
    expect(detailFacts({ id: "m", title: "T", favorited: false }, [], 30)).toEqual({
      categories: [],
    });
    expect(detailFacts(null, [])).toEqual({ categories: [] });
  });
});

describe("detailCreditRows (CORE-2508)", () => {
  it("lists each category kind under its display name, then person credits by job", () => {
    const rows = detailCreditRows([
      {
        entityID: "c1",
        title: "Cat A",
        kind: "k1",
        kindDisplayName: { en: "Kind One" },
        archetype: "category",
        relation: "r",
      },
      {
        entityID: "p1",
        title: "Lead",
        kind: "p",
        archetype: "person",
        relation: "r",
        attributes: { character: "Hero" },
      },
      {
        entityID: "d1",
        title: "Maker",
        kind: "p",
        archetype: "person",
        relation: "r",
        attributes: { job: "Lead Maker" },
      },
      { entityID: "c2", title: "Cat B", kind: "k2", archetype: "category", relation: "r" },
      {
        entityID: "c3",
        title: "Cat C",
        kind: "k1",
        kindDisplayName: { en: "Kind One" },
        archetype: "category",
        relation: "r",
      },
      {
        entityID: "d2",
        title: "Other",
        kind: "p",
        archetype: "person",
        relation: "r",
        attributes: { job: " Lead Maker " },
      },
      {
        entityID: "",
        title: "Unlinked",
        kind: "p",
        archetype: "person",
        relation: "r",
        attributes: { job: "Aide" },
      },
    ]);
    expect(rows).toEqual([
      {
        key: "kind:k1",
        label: { en: "Kind One" },
        credits: [
          { entityID: "c1", title: "Cat A" },
          { entityID: "c3", title: "Cat C" },
        ],
      },
      { key: "kind:k2", label: "k2", credits: [{ entityID: "c2", title: "Cat B" }] },
      {
        key: "job:Lead Maker",
        label: "Lead Maker",
        credits: [
          { entityID: "d1", title: "Maker" },
          { entityID: "d2", title: "Other" },
        ],
      },
      { key: "job:Aide", label: "Aide", credits: [{ entityID: "", title: "Unlinked" }] },
    ]);
    expect(detailCreditRows(undefined)).toEqual([]);
  });
});

describe("runtimeParts", () => {
  it("rounds to the minute", () => {
    expect(runtimeParts(5400)).toEqual({ hours: 1, minutes: 30 });
    expect(runtimeParts(2690)).toEqual({ hours: 0, minutes: 45 });
  });
});

describe("fieldDisplayValue", () => {
  it("shows a date field as a calendar date in the display locale", () => {
    expect(fieldDisplayValue({ type: "date", value: "2025-05-09T00:00:00Z" }, "en-US")).toBe(
      "May 9, 2025",
    );
    expect(fieldDisplayValue({ type: "date", value: "2025-05-09" }, "en-GB")).toBe("9 May 2025");
  });

  it("keeps the stored day whatever the time of day", () => {
    expect(fieldDisplayValue({ type: "date", value: "2025-05-09T23:30:00-08:00" }, "en-US")).toBe(
      "May 9, 2025",
    );
  });

  it("leaves other types and unparseable dates as stored", () => {
    expect(fieldDisplayValue({ type: "text", value: "2025-05-09T00:00:00Z" }, "en-US")).toBe(
      "2025-05-09T00:00:00Z",
    );
    expect(fieldDisplayValue({ type: "date", value: "Spring 2025" }, "en-US")).toBe("Spring 2025");
    expect(fieldDisplayValue({ type: "date", value: "2025-02-31" }, "en-US")).toBe("2025-02-31");
    expect(fieldDisplayValue({ type: "date", value: "0001-05-09" }, "en-US")).toBe("0001-05-09");
  });
});
