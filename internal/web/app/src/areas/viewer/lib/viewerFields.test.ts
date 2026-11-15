import { describe, expect, it } from "vitest";
import type { EntityFieldInfo, GenericMediaInfo } from "@/lib/graphql/types";
import { detailFacts, runtimeParts, viewerFacingFields } from "./viewerFields";

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
      field({ name: "blank", value: "  ", label: { en: "Blank" } }),
      field({ name: "empty-label", label: "" }),
      field({ name: "network", label: "Network" }),
    ];
    expect(viewerFacingFields(fields).map((f) => f.name)).toEqual(["tagline", "network"]);
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
    ],
  };

  it("collects year, runtime, rating and up to three distinct categories", () => {
    expect(detailFacts(media, [field({ name: "certification", value: " R " })], 3600)).toEqual({
      year: 1999,
      runtimeSeconds: 3600,
      rating: "R",
      categories: ["One", "Two", "Three"],
    });
  });

  it("omits what is unknown", () => {
    expect(detailFacts({ id: "m", title: "T", favorited: false }, [], 30)).toEqual({
      categories: [],
    });
    expect(detailFacts(null, [])).toEqual({ categories: [] });
  });
});

describe("runtimeParts", () => {
  it("rounds to the minute", () => {
    expect(runtimeParts(5400)).toEqual({ hours: 1, minutes: 30 });
    expect(runtimeParts(2690)).toEqual({ hours: 0, minutes: 45 });
  });
});
