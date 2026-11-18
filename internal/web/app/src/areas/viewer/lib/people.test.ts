import { describe, expect, it } from "vitest";
import type { MediaCredit, SourceItem } from "@/lib/graphql/types";
import { castGroups, creditPart, filmographyOrder, nameInitial } from "./people";

const person = (entityID: string, attributes?: Record<string, string>): MediaCredit => ({
  entityID,
  title: `Name ${entityID}`,
  kind: "person",
  archetype: "person",
  relation: "credited-in",
  attributes,
});

describe("castGroups", () => {
  it("keeps person credits in server order, once each, grouped by department", () => {
    const groups = castGroups([
      person("a", { department: "Acting" }),
      { ...person("box"), archetype: "container" },
      person("b", { department: "Directing" }),
      person("c", { department: "Acting" }),
      person("a", { department: "Directing" }),
    ]);
    expect(groups.map((g) => [g.department, g.credits.map((c) => c.entityID)])).toEqual([
      ["Acting", ["a", "c"]],
      ["Directing", ["b"]],
    ]);
  });

  it("puts credits without a department in one group", () => {
    expect(castGroups([person("a"), person("b")])).toEqual([
      { department: "", credits: [person("a"), person("b")] },
    ]);
    expect(castGroups(undefined)).toEqual([]);
  });
});

describe("creditPart", () => {
  it("prefers the character, then the role, then the job", () => {
    expect(creditPart(person("a", { character: " Hero ", role: "Lead" }))).toBe("Hero");
    expect(creditPart(person("a", { character: " ", role: "Lead" }))).toBe("Lead");
    expect(creditPart(person("a", { job: "Director" }))).toBe("Director");
    expect(creditPart(person("a"))).toBe("");
  });
});

describe("nameInitial", () => {
  it("returns the first letter, uppercased", () => {
    expect(nameInitial(" émile")).toBe("É");
    expect(nameInitial("")).toBe("");
  });
});

describe("filmographyOrder", () => {
  it("sorts newest year first, undated last, and keeps ties in server order", () => {
    const items: SourceItem[] = [
      { id: "old", kind: "media", title: "Old", year: 1999 },
      { id: "undated", kind: "media", title: "Undated" },
      { id: "new", kind: "media", title: "New", year: 2015 },
      { id: "new-2", kind: "media", title: "New 2", year: 2015 },
    ];
    expect(filmographyOrder(items).map((item) => item.id)).toEqual([
      "new",
      "new-2",
      "old",
      "undated",
    ]);
  });
});
